package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

func (r KubernetesRuntime) Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error {
	ctx := context.Background()

	runCfg := extractRunSettings(otherOptionsList)
	envs := buildEnvMap(uid, gid, debug)

	commandSequence := append([]string{"/bin/bash", "-c"}, cmdStr)

	manifest, err := generateManifest(runCfg, image, chDir, commandSequence, envs, volumeList)
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

	kubectlArgs := []string{"apply", "-f", tmpFile.Name()}
	if debug {
		fmt.Printf("🔨 Comando kubectl: %s %s\n", r.config.CommandBinPath, strings.Join(r.buildKubectlArgs(kubectlArgs...), " "))
	}

	cmd := r.buildKubectlCmdWithContext(ctx, false, kubectlArgs...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao aplicar manifesto: %w", err)
	}

	return nil
}

func buildEnvMap(uid, gid string, debug bool) map[string]string {
	envs := map[string]string{
		"UID": uid,
		"GID": gid,
	}
	if debug {
		envs["DEBUG"] = "true"
	}
	return envs
}

type runSettings struct {
	PodName   string
	Namespace string
}

func extractRunSettings(options []string) runSettings {
	cfg := runSettings{
		PodName:   "main",
		Namespace: "default",
	}

	for _, opt := range options {
		switch {
		case strings.HasPrefix(opt, "--name="):
			cfg.PodName = strings.TrimPrefix(opt, "--name=")
		case strings.HasPrefix(opt, "--namespace="):
			cfg.Namespace = strings.TrimPrefix(opt, "--namespace=")
		}
	}

	return cfg
}

func generateManifest(runCfg runSettings, image, workingDir string, command []string, envs map[string]string, volumeList []string) ([]byte, error) {
	data, err := buildManifestData(runCfg, image, workingDir, command, envs, volumeList)
	if err != nil {
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

type manifestData struct {
	Namespace  string
	PodName    string
	Image      string
	WorkingDir string
	Command    []string
	Env        []envEntry
	Volumes    []volumeEntry
}

type envEntry struct {
	Name  string
	Value string
}

type volumeEntry struct {
	Name      string
	MountPath string
	HostPath  string
	ReadOnly  bool
}

func buildManifestData(runCfg runSettings, image, workingDir string, command []string, envs map[string]string, volumeList []string) (manifestData, error) {
	envEntries := make([]envEntry, 0, len(envs))
	keys := make([]string, 0, len(envs))
	for k := range envs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		envEntries = append(envEntries, envEntry{Name: k, Value: envs[k]})
	}

	volumes := make([]volumeEntry, 0, len(volumeList))
	for idx, rawVolume := range volumeList {
		parts := strings.Split(rawVolume, ":")
		if len(parts) < 2 {
			return manifestData{}, fmt.Errorf("volume inválido: %s (esperado formato hostPath:mountPath[:mode])", rawVolume)
		}

		mode := "rw"
		if len(parts) >= 3 && parts[2] != "" {
			mode = parts[2]
		}

		volumes = append(volumes, volumeEntry{
			Name:      fmt.Sprintf("vol-%d", idx),
			MountPath: parts[1],
			HostPath:  filepath.Join("/sigctl", parts[0]),
			ReadOnly:  strings.EqualFold(mode, "ro"),
		})
	}

	return manifestData{
		Namespace:  runCfg.Namespace,
		PodName:    runCfg.PodName,
		Image:      image,
		WorkingDir: workingDir,
		Command:    command,
		Env:        envEntries,
		Volumes:    volumes,
	}, nil
}

func formatList(items []string) string {
	return fmt.Sprintf("[%s]", strings.Join(QuoteList(items), ", "))
}

const kubernetesManifestTemplate = `apiVersion: v1
kind: Namespace
metadata:
  name: {{.Namespace}}
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
  containers:
    - name: main
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
      hostPath:
        path: {{.HostPath}}
        type: DirectoryOrCreate
{{- end }}
{{- else }}
  volumes: []
{{- end }}
`
