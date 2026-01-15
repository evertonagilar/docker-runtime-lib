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
	if r.config.Debug {
		fmt.Printf("🔍 CopyToHost iniciado:\n")
		fmt.Printf("   Origem (pod): %s\n", src)
		fmt.Printf("   Pod: %s\n", podOrContainerName)
		if mainContainerName != "" {
			fmt.Printf("   Container: %s\n", mainContainerName)
		}
		if namespace != "" {
			fmt.Printf("   Namespace: %s\n", namespace)
		}
		fmt.Printf("   Destino (host): %s\n", dst)
	}

	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	dst = normalizeCopyDstPath(dst)
	if r.config.Debug && dst != dst {
		fmt.Printf("   Destino normalizado: %s\n", dst)
	}

	// Get file size for progress reporting
	if r.config.Debug {
		fmt.Printf("📏 Obtendo tamanho do arquivo remoto...\n")
	}
	fileSize, err := r.getRemoteFileSize(src, podOrContainerName, mainContainerName, namespace)
	if err != nil && r.config.Debug {
		fmt.Printf("⚠️  Não foi possível obter tamanho do arquivo: %v\n", err)
	}

	if r.config.Debug && fileSize > 0 {
		fmt.Printf("📦 Tamanho do arquivo: %.2f MB (%d bytes)\n", float64(fileSize)/(1024*1024), fileSize)
	}

	// Create destination directory if needed
	dstDir := filepath.Dir(dst)
	if r.config.Debug {
		fmt.Printf("📁 Criando diretório de destino: %s\n", dstDir)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("erro ao criar diretório de destino: %w", err)
	}

	// Build rsync command using kubectl exec as transport
	// rsync -av --progress -e "kubectl exec -i POD -- " :SRC DST
	rsyncPath := getRsyncBinPath()
	if r.config.Debug {
		fmt.Printf("🔧 Binário rsync detectado: %s\n", rsyncPath)
	}

	rsyncCmd := []string{
		rsyncPath,
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
		fmt.Printf("🔨 Executando comando rsync:\n")
		fmt.Printf("   %s\n", strings.Join(rsyncCmd, " "))
	}

	// Execute rsync
	cmd := exec.Command(rsyncCmd[0], rsyncCmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if r.config.Debug {
		fmt.Printf("⏳ Iniciando transferência...\n")
	}

	if err := cmd.Run(); err != nil {
		if r.config.Debug {
			fmt.Printf("❌ Erro ao executar rsync: %v\n", err)
		}
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
