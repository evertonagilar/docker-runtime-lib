package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// CopyToHost copies a file from a pod to the host using rsync over kubectl exec.
// This ensures binary integrity and handles large files efficiently.
func (r KubernetesRuntime) CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	dst = normalizeCopyDstPath(dst)

	// Get file size for progress reporting
	fileSize, _ := r.getRemoteFileSize(src, podOrContainerName, mainContainerName, namespace)

	if r.config.Debug && fileSize > 0 {
		fmt.Printf("📦 Copiando arquivo: %s (%.2f MB)\n", src, float64(fileSize)/(1024*1024))
	}

	// Create destination directory if needed
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("erro ao criar diretório de destino: %w", err)
	}

	// Build rsync command using kubectl exec as transport
	// rsync -av --progress -e "kubectl exec -i POD -- " :SRC DST
	rsyncCmd := []string{
		"/usr/bin/rsync",
		"-av",
		"--progress",
		"-e",
	}

	// Build kubectl exec command for rsync transport
	kubectlExec := fmt.Sprintf("kubectl exec -i %s", podOrContainerName)
	if mainContainerName != "" {
		kubectlExec += fmt.Sprintf(" -c %s", mainContainerName)
	}
	if namespace != "" {
		kubectlExec += fmt.Sprintf(" -n %s", namespace)
	}
	if r.config.Kubeconfig != "" {
		kubectlExec += fmt.Sprintf(" --kubeconfig %s", r.config.Kubeconfig)
	}
	kubectlExec += " --"

	rsyncCmd = append(rsyncCmd, kubectlExec)
	rsyncCmd = append(rsyncCmd, fmt.Sprintf(":%s", src))
	rsyncCmd = append(rsyncCmd, dst)

	if r.config.Debug {
		fmt.Printf("🔨 Comando rsync: %s\n", strings.Join(rsyncCmd, " "))
	}

	// Execute rsync
	cmd := exec.Command(rsyncCmd[0], rsyncCmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao executar rsync: %w", err)
	}

	if r.config.Debug {
		fmt.Printf("✅ Arquivo copiado com sucesso: %s -> %s\n", src, dst)
	}

	return nil
}

// getRemoteFileSize gets the size of a file in the pod using stat command
func (r KubernetesRuntime) getRemoteFileSize(filePath, podOrContainerName, mainContainerName, namespace string) (int64, error) {
	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "stat", "-c", "%s", filePath)
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	sizeStr := strings.TrimSpace(string(output))
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0, err
	}

	return size, nil
}
