package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (r KubernetesRuntime) Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error {
	ctx := context.Background()

	// 🔍 Valores padrão
	podName := "main"
	namespace := "default"

	// 🔍 Extrai o nome do pod de otherOptionsList, ex: "--name=cpdctl-build"
	for _, opt := range otherOptionsList {
		if strings.HasPrefix(opt, "--name=") {
			podName = strings.TrimPrefix(opt, "--name=")
			break
		}
	}

	command := []string{"/bin/bash", "-c"}
	args := []string{cmdStr}

	envs := map[string]string{
		"UID": uid,
		"GID": gid,
	}
	if debug {
		envs["DEBUG"] = "true"
	}

	// 🧩 Gera o manifesto
	manifest, err := generateManifest(podName, namespace, image, command, args, envs, volumeList, chDir)
	if err != nil {
		return fmt.Errorf("erro ao gerar manifesto: %w", err)
	}

	// 💾 Salva o manifesto temporário
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.yaml", podName))
	if err := os.WriteFile(tmpFile, []byte(manifest), 0644); err != nil {
		return fmt.Errorf("erro ao salvar manifesto: %w", err)
	}

	// 🚀 Aplica o manifesto
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", tmpFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if debug {
		fmt.Printf("📦 Aplicando manifesto gerado: %s\n", tmpFile)
		fmt.Printf("%s\n", manifest)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao aplicar manifesto: %w", err)
	}

	return nil
}

func generateManifest(podName, namespace, image string, command, args []string, envs map[string]string, volumeList []string, chDir string) (string, error) {
	type Volume struct {
		Name      string
		HostPath  string
		MountPath string
		MountMode string
	}

	var volumes []Volume

	for i, v := range volumeList {
		// formato esperado: <hostPath>:<mountPath>:<mode>
		parts := strings.Split(v, ":")
		if len(parts) < 2 {
			return "", fmt.Errorf("volume inválido: %s (esperado formato hostPath:mountPath[:mode])", v)
		}
		vol := Volume{
			Name:      fmt.Sprintf("vol-%d", i),
			HostPath:  filepath.Join("/sigctl", parts[0]),
			MountPath: parts[1],
			MountMode: "rw",
		}
		if len(parts) == 3 {
			vol.MountMode = parts[2]
		}
		volumes = append(volumes, vol)
	}

	var envYAML, volumeMountsYAML, volumesYAML strings.Builder

	for k, v := range envs {
		envYAML.WriteString(fmt.Sprintf(`
      - name: %s
        value: "%s"`, k, v))
	}

	for _, vol := range volumes {
		// declara o volumeMounts
		volumeMountsYAML.WriteString(fmt.Sprintf(`
      - name: %s
        mountPath: %s
        readOnly: %t`, vol.Name, vol.MountPath, vol.MountMode == "ro"))

		// declara o volumes
		volumesYAML.WriteString(fmt.Sprintf(`
  - name: %s
    hostPath:
      path: %s
      type: DirectoryOrCreate`, vol.Name, vol.HostPath))
	}

	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  restartPolicy: Never
  containers:
  - name: main
    image: %s
    workingDir: %s
    command: [%s, %s]
    env:%s
    volumeMounts:%s
  volumes:%s`,
		podName,
		namespace,
		image,
		chDir,
		strings.Join(QuoteList(command), ", "),
		strings.Join(QuoteList(args), ", "),
		envYAML.String(),
		volumeMountsYAML.String(),
		volumesYAML.String(),
	)

	return manifest, nil
}
