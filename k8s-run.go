package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/template"
)

func (r KubernetesRuntime) Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumes []TVolume, otherOptionsList []string, namespace, podName, containerName, storageClass string) error {
	ctx := context.Background()

	defaultNamespace := namespace
	if defaultNamespace == "" {
		defaultNamespace = r.config.Namespace
	}
	defaultPod := podName
	if defaultPod == "" {
		defaultPod = r.config.PodName
	}

	runCfg := extractRunSettings(defaultNamespace, defaultPod, otherOptionsList)
	envs := buildEnvMap(uid, gid, r.config.Debug)

	if entrypoint == "" {
		entrypoint = "/bin/bash"
	}
	commandSequence := []string{entrypoint, "-c", cmdStr}

	manifest, err := generateManifest(runCfg, image, chDir, commandSequence, envs, volumes, storageClass, podName, containerName, r.config.ImagePullSecretName)
	if err != nil {
		return fmt.Errorf("erro ao gerar manifesto: %w", err)
	}

	tmpFile, err := os.CreateTemp("", fmt.Sprintf("%s-*.yaml", runCfg.PodName))
	if err != nil {
		return fmt.Errorf("erro ao criar arquivo temporário para manifesto: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.Write(manifest); err != nil {
		tmpFile.Close()
		return fmt.Errorf("erro ao escrever manifesto temporário: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("erro ao fechar manifesto temporário: %w", err)
	}

	kubectlArgs := addNamespaceArg(runCfg.Namespace, []string{"apply", "-f", tmpFile.Name()})
	cmd := r.buildKubectlCmdWithContext(ctx, true, kubectlArgs...)
	if err := r.runKubectlCommand(cmd, "erro ao aplicar manifesto"); err != nil {
		return err
	}

	return nil
}

func buildEnvMap(uid, gid string, debugEnabled bool) map[string]string {
	envs := map[string]string{
		"UID": uid,
		"GID": gid,
	}
	if debugEnabled {
		envs["DEBUG"] = "true"
	}
	return envs
}

type runSettings struct {
	PodName       string
	ContainerName string
	Namespace     string
	MemoryRequest string
	MemoryLimit   string
}

func extractRunSettings(defaultNamespace, defaultPod string, options []string) runSettings {
	if defaultPod == "" {
		defaultPod = "main"
	}
	if defaultNamespace == "" {
		defaultNamespace = "default"
	}

	cfg := runSettings{
		PodName:   defaultPod,
		Namespace: defaultNamespace,
	}

	for _, opt := range options {
		switch {
		case strings.HasPrefix(opt, "--name="):
			cfg.PodName = strings.TrimPrefix(opt, "--name=")
		case strings.HasPrefix(opt, "--namespace="):
			cfg.Namespace = strings.TrimPrefix(opt, "--namespace=")
		case strings.HasPrefix(opt, "--memory="):
			cfg.MemoryRequest = strings.TrimPrefix(opt, "--memory=")
		case strings.HasPrefix(opt, "--memory-limit="):
			cfg.MemoryLimit = strings.TrimPrefix(opt, "--memory-limit=")
		}
	}

	return cfg
}

func generateManifest(runCfg runSettings, image, workingDir string, command []string, envs map[string]string, volumes []TVolume, storageClass, podName, containerName, imagePullSecretName string) ([]byte, error) {
	data, err := buildManifestData(runCfg, image, workingDir, command, envs, volumes, storageClass, podName, containerName, imagePullSecretName)
	if err != nil {
		return nil, err
	}
	if err := ensurePersistentVolumeClaimRefCleared(data.PersistentVolumes); err != nil {
		return nil, err
	}

	tpl, err := template.New("manifest").Funcs(template.FuncMap{
		"formatList": formatList,
	}).Parse(kubernetesManifestTemplate)
	if err != nil {
		return nil, fmt.Errorf("erro ao preparar template do manifesto: %w", err)
	}

	var builder strings.Builder
	if err := tpl.Execute(&builder, data); err != nil {
		return nil, fmt.Errorf("erro ao renderizar template do manifesto: %w", err)
	}

	return []byte(builder.String()), nil
}

const defaultPersistentVolumeSize = "5Gi"

type manifestData struct {
	Namespace           string
	PodName             string
	ContainerName       string
	Image               string
	WorkingDir          string
	Command             []string
	Env                 []envEntry
	Volumes             []volumeEntry
	PersistentVolumes   []volumeEntry
	ImagePullSecretName string
	MemoryRequest       string
	MemoryLimit         string
}

type envEntry struct {
	Name  string
	Value string
}

type volumeEntry struct {
	Name                      string
	MountPath                 string
	HostPath                  string
	ReadOnly                  bool
	IsPersistent              bool
	IsEmptyDir                bool
	EmptyDirMedium            string
	StorageClass              string
	PersistentVolumeSize      string
	PersistentVolumeName      string
	PersistentVolumeClaimName string
}

func buildManifestData(runCfg runSettings, image, workingDir string, command []string, envs map[string]string, volumes []TVolume, storageClass, podName, containerName, imagePullSecretName string) (manifestData, error) {
	envEntries := make([]envEntry, 0, len(envs))
	keys := make([]string, 0, len(envs))
	for k := range envs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		envEntries = append(envEntries, envEntry{Name: k, Value: envs[k]})
	}

	volumeEntries := make([]volumeEntry, 0, len(volumes))
	persistentVolumes := make([]volumeEntry, 0, len(volumes))
	baseName := runCfg.PodName
	if podName != "" {
		baseName = podName
	}
	baseName = sanitizeKubernetesName(baseName)
	if baseName == "" {
		baseName = "runtime"
	}

	for idx, volume := range volumes {
		effectiveStorageClass := volume.StorageClass
		if effectiveStorageClass == "" {
			effectiveStorageClass = storageClass
		}

		if volume.MountPath == "" {
			return manifestData{}, fmt.Errorf("volume %s inválido: mountPath é obrigatório", volume.MountPath)
		}
		if volume.InMemory {
			if volume.HostPath != "" {
				return manifestData{}, fmt.Errorf("volume %s inválido: hostPath não é suportado para volumes inMemory", volume.MountPath)
			}
		}
		if volume.HostPath == "" && effectiveStorageClass == "" && !volume.InMemory {
			return manifestData{}, fmt.Errorf("volume %s inválido: hostPath é obrigatório quando storageClass não é informado", volume.MountPath)
		}

		entry := volumeEntry{
			Name:      fmt.Sprintf("vol-%d", idx),
			MountPath: volume.MountPath,
			ReadOnly:  volume.ReadOnly,
		}

		if volume.InMemory {
			entry.IsEmptyDir = true
			entry.EmptyDirMedium = "Memory"
		} else {
			if volume.HostPath != "" {
				entry.HostPath = volume.HostPath
			}

			if effectiveStorageClass != "" {
				entry.IsPersistent = true
				entry.StorageClass = effectiveStorageClass
				entry.PersistentVolumeClaimName = fmt.Sprintf("%s-pvc-%d", baseName, idx)
				if volume.HostPath != "" {
					entry.PersistentVolumeName = fmt.Sprintf("%s-pv-%d", baseName, idx)
				}
				size := volume.Size
				if size == "" {
					size = defaultPersistentVolumeSize
				}
				entry.PersistentVolumeSize = size
				persistentVolumes = append(persistentVolumes, entry)
			}
		}
		volumeEntries = append(volumeEntries, entry)
	}

	effectiveContainerName := containerName
	if effectiveContainerName == "" {
		effectiveContainerName = runCfg.PodName
	}
	effectiveContainerName = sanitizeKubernetesName(effectiveContainerName)
	if effectiveContainerName == "" {
		effectiveContainerName = "main"
	}

	return manifestData{
		Namespace:           runCfg.Namespace,
		PodName:             runCfg.PodName,
		ContainerName:       effectiveContainerName,
		Image:               image,
		WorkingDir:          workingDir,
		Command:             command,
		Env:                 envEntries,
		Volumes:             volumeEntries,
		PersistentVolumes:   persistentVolumes,
		ImagePullSecretName: imagePullSecretName,
		MemoryRequest:       runCfg.MemoryRequest,
		MemoryLimit:         runCfg.MemoryLimit,
	}, nil
}

func ensurePersistentVolumeClaimRefCleared(volumes []volumeEntry) error {
	for _, volume := range volumes {
		if volume.PersistentVolumeName == "" {
			continue
		}
		if err := cleanupReleasedPersistentVolume(volume.PersistentVolumeName); err != nil {
			return err
		}
	}
	return nil
}

func cleanupReleasedPersistentVolume(pvName string) error {
	kubectlPath, err := getKubectlBinPath()
	if err != nil {
		// Sem kubectl disponível não há como consultar/atualizar o PV.
		return nil
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(kubectlPath, "get", "pv", pvName, "-o", "json")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// PV inexistente ou cluster inacessível – continuar sem falhar.
		return nil
	}

	var info struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
		Spec struct {
			ClaimRef *json.RawMessage `json:"claimRef"`
		} `json:"spec"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return fmt.Errorf("erro ao analisar dados do PV %s: %w", pvName, err)
	}

	if !strings.EqualFold(info.Status.Phase, "Released") || info.Spec.ClaimRef == nil {
		return nil
	}

	patchPayload := `[{"op":"remove","path":"/spec/claimRef"}]`
	var patchStderr bytes.Buffer
	patchCmd := exec.Command(kubectlPath, "patch", "pv", pvName, "--type=json", "-p", patchPayload)
	patchCmd.Stdout = io.Discard
	patchCmd.Stderr = &patchStderr
	if err := patchCmd.Run(); err != nil {
		msg := strings.TrimSpace(patchStderr.String())
		if strings.Contains(msg, "jsonpatch remove operation does not apply") {
			// claimRef já removido por outra rotina – seguir em frente.
			return nil
		}
		return fmt.Errorf("erro ao limpar claimRef do PV %s: %s", pvName, msg)
	}

	return nil
}

func formatList(items []string) string {
	return fmt.Sprintf("[%s]", strings.Join(QuoteList(items), ", "))
}

func sanitizeKubernetesName(name string) string {
	if name == "" {
		return ""
	}
	name = strings.ToLower(name)

	var builder strings.Builder
	prevDash := false

	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			builder.WriteRune(r)
			prevDash = false
		case r == '-':
			if !prevDash && builder.Len() > 0 {
				builder.WriteRune(r)
				prevDash = true
			}
		default:
			if !prevDash && builder.Len() > 0 {
				builder.WriteRune('-')
				prevDash = true
			}
		}
	}

	sanitized := strings.Trim(builder.String(), "-")
	return sanitized
}

const kubernetesManifestTemplate = `
{{- if .PersistentVolumes }}
{{- range .PersistentVolumes }}
{{- if .HostPath }}
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: {{.PersistentVolumeName}}
  labels:
    app: {{$.PodName}}
spec:
  capacity:
    storage: {{.PersistentVolumeSize}}
  accessModes:
    - ReadWriteOnce
{{- if .StorageClass }}
  storageClassName: {{.StorageClass}}
{{- end }}
  persistentVolumeReclaimPolicy: Delete
  hostPath:
    path: {{.HostPath}}
    type: DirectoryOrCreate
{{- end }}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{.PersistentVolumeClaimName}}
  namespace: {{$.Namespace}}
spec:
{{- if .StorageClass }}
  storageClassName: {{.StorageClass}}
{{- end }}
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: {{.PersistentVolumeSize}}
{{- if .HostPath }}
  volumeName: {{.PersistentVolumeName}}
{{- end }}
{{- end }}
{{- end }}
---
apiVersion: v1
kind: Pod
metadata:
  name: {{.PodName}}
  namespace: {{.Namespace}}
  labels:
    app: {{.PodName}}
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 0
{{- if .ImagePullSecretName }}
  imagePullSecrets:
    - name: {{.ImagePullSecretName}}
{{- end }}
  containers:
    - name: {{.ContainerName}}
      image: {{.Image}}
      workingDir: {{.WorkingDir}}
      command: {{formatList .Command}}
{{- if .Env }}
      env:
{{- range .Env }}
        - name: {{.Name}}
          value: "{{.Value}}"
{{- end }}
{{- else }}
      env: []
{{- end }}
{{- if or .MemoryRequest .MemoryLimit }}
      resources:
{{- if .MemoryRequest }}
        requests:
          memory: {{.MemoryRequest}}
{{- end }}
{{- if .MemoryLimit }}
        limits:
          memory: {{.MemoryLimit}}
{{- end }}
{{- end }}
{{- if .Volumes }}
      volumeMounts:
{{- range .Volumes }}
        - name: {{.Name}}
          mountPath: {{.MountPath}}
          readOnly: {{.ReadOnly}}
{{- end }}
{{- else }}
      volumeMounts: []
{{- end }}
{{- if .Volumes }}
  volumes:
{{- range .Volumes }}
    - name: {{.Name}}
{{- if .IsPersistent }}
      persistentVolumeClaim:
        claimName: {{.PersistentVolumeClaimName}}
{{- else if .IsEmptyDir }}
      emptyDir:
{{- if .EmptyDirMedium }}
        medium: {{.EmptyDirMedium}}
{{- end }}
{{- else }}
      hostPath:
        path: {{.HostPath}}
        type: DirectoryOrCreate
{{- end }}
{{- end }}
{{- else }}
  volumes: []
{{- end }}
`
