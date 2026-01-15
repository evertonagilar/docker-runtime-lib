package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// CopyToHost copies a file from a pod to the host.
// On Windows, uses tar for better compatibility. On Unix/Linux, uses rsync for efficiency.
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

	// Use different approaches for Windows vs Unix/Linux
	if runtime.GOOS == "windows" {
		return r.copyToHostUsingTar(src, podOrContainerName, mainContainerName, namespace, dst)
	}
	return r.copyToHostUsingRsync(src, podOrContainerName, mainContainerName, namespace, dst)
}

func (r KubernetesRuntime) copyToHostUsingTar(src, pod, container, namespace, dst string) error {
	if r.config.Debug {
		fmt.Println("📦 Usando tar seguro (Windows)")
	}

	// kubectl exec args
	execArgs := []string{"exec", "-i", pod}
	if container != "" {
		execArgs = append(execArgs, "-c", container)
	}
	if namespace != "" {
		execArgs = append(execArgs, "-n", namespace)
	}

	srcDir := "/"
	srcFile := src
	if idx := strings.LastIndex(src, "/"); idx >= 0 {
		srcDir = src[:idx]
		if srcDir == "" {
			srcDir = "/"
		}
		srcFile = src[idx+1:]
	}

	execArgs = append(execArgs, "--", "tar", "-cf", "-", "-C", srcDir, srcFile)

	kubectlCmd := r.buildKubectlCmd(true, execArgs...)

	tmpTar := dst + ".tar"
	outFile, err := os.Create(tmpTar)
	if err != nil {
		return err
	}
	defer outFile.Close()

	kubectlCmd.Stdout = outFile
	kubectlCmd.Stderr = os.Stderr

	if r.config.Debug {
		fmt.Println("⏳ Recebendo stream tar...")
	}

	if err := kubectlCmd.Run(); err != nil {
		return fmt.Errorf("erro ao executar kubectl: %w", err)
	}

	// Agora extrai SEM pipe
	tarCmd := exec.Command("tar", "-xf", tmpTar, "-C", filepath.Dir(dst))
	tarCmd.Stdout = os.Stdout
	tarCmd.Stderr = os.Stderr

	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("erro ao extrair tar: %w", err)
	}

	_ = os.Remove(tmpTar)

	if r.config.Debug {
		fmt.Printf("✅ Arquivo copiado com sucesso: %s -> %s\n", src, dst)
	}

	return nil
}

// copyToHostUsingRsync copies a file using rsync (Unix/Linux only)
func (r KubernetesRuntime) copyToHostUsingRsync(src, podOrContainerName, mainContainerName, namespace, dst string) error {
	if r.config.Debug {
		fmt.Printf("🔄 Usando rsync para transferência (Unix/Linux)\n")
	}

	dst = normalizeCopyDstPath(dst)

	// Build rsync command using kubectl exec as transport
	rsyncPath := getRsyncBinPath()
	if r.config.Debug {
		fmt.Printf("🔧 Binário rsync detectado: %s\n", rsyncPath)
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

	rsyncCmd := []string{
		rsyncPath,
		"-av",
		"--progress",
		"-e",
		kubectlExec,
		fmt.Sprintf(":%s", src),
		dst,
	}

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
