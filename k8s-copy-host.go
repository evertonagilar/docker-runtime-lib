package container

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
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
		// For large files (> 30MB), use chunked transfer
		const chunkSizeLimit = 30 * 1024 * 1024 // 30MB
		if fileSize > chunkSizeLimit {
			return r.copyToHostUsingChunks(src, podOrContainerName, mainContainerName, namespace, dst, fileSize)
		}
		return r.copyToHostUsingTar(src, podOrContainerName, mainContainerName, namespace, dst)
	}
	return r.copyToHostUsingRsync(src, podOrContainerName, mainContainerName, namespace, dst)
}

func (r KubernetesRuntime) copyToHostUsingTar(
	src,
	pod,
	container,
	namespace,
	dst string,
) error {

	fmt.Println("📦 Usando tar seguro (cross-platform)")

	// --- kubectl exec tar ---
	execArgs := []string{"exec", pod}

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

	execArgs = append(execArgs,
		"--",
		"tar", "-cf", "-",
		"-C", srcDir,
		srcFile,
	)

	kubectlCmd := r.buildKubectlCmd(true, execArgs...)

	fmt.Printf("🔨 Executando: %s %s\n",
		kubectlCmd.Path,
		strings.Join(kubectlCmd.Args[1:], " "),
	)

	// --- arquivo tar ---
	tmpTar := dst + ".tar"

	outFile, err := os.Create(tmpTar)
	if err != nil {
		return fmt.Errorf("erro ao criar tar temporário: %w", err)
	}

	kubectlCmd.Stdout = outFile
	kubectlCmd.Stderr = os.Stderr

	fmt.Printf("⏳ Gravando stream em: %s\n", tmpTar)

	// 🔴 1) EXECUTA kubectl
	if err := kubectlCmd.Run(); err != nil {
		outFile.Close()
		return fmt.Errorf("erro ao executar kubectl: %w", err)
	}

	// 🔴 2) FECHA O ARQUIVO (ESSENCIAL!)
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("erro ao fechar tar: %w", err)
	}

	// 🔴 3) AGUARDA SYNC DO FILESYSTEM (Windows)
	if runtime.GOOS == "windows" {
		time.Sleep(1 * time.Second)
	}

	// --- tar local (Windows nativo) ---
	tarPath := "tar"
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = "C:\\Windows"
		}
		tarPath = filepath.Join(systemRoot, "System32", "tar.exe")
	}

	extractDir := filepath.Dir(dst)

	tarCmd := exec.Command(
		tarPath,
		"-xf", tmpTar,
		"-C", extractDir,
	)

	fmt.Printf("📂 Executando: %s %s\n",
		tarCmd.Path,
		strings.Join(tarCmd.Args[1:], " "),
	)

	tarCmd.Stdout = os.Stdout
	tarCmd.Stderr = os.Stderr

	// 🔴 3) EXTRAI
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("erro ao extrair tar: %w", err)
	}

	// --- limpeza ---
	_ = os.Remove(tmpTar)

	fmt.Printf("✅ Arquivo copiado com sucesso: %s → %s\n", src, dst)
	return nil
}

// copyToHostUsingChunks transfers large files by splitting into 30MB chunks
func (r KubernetesRuntime) copyToHostUsingChunks(
	src,
	pod,
	container,
	namespace,
	dst string,
	fileSize int64,
) error {

	fmt.Printf("📦 Arquivo grande detectado (%.2f MB) - usando transferência em chunks\n", float64(fileSize)/(1024*1024))

	const chunkSize = 30 * 1024 * 1024 // 30MB
	numChunks := (fileSize + chunkSize - 1) / chunkSize

	fmt.Printf("📊 Dividindo em %d chunks de ~30MB\n", numChunks)

	// --- 1) Split file in pod ---
	srcDir := "/"
	srcFile := src
	if idx := strings.LastIndex(src, "/"); idx >= 0 {
		srcDir = src[:idx]
		if srcDir == "" {
			srcDir = "/"
		}
		srcFile = src[idx+1:]
	}

	chunkPrefix := fmt.Sprintf("/tmp/chunk_%s_", srcFile)

	// Split command in pod
	splitArgs := []string{"exec", pod}
	if container != "" {
		splitArgs = append(splitArgs, "-c", container)
	}
	if namespace != "" {
		splitArgs = append(splitArgs, "-n", namespace)
	}
	splitArgs = append(splitArgs, "--", "split", "-b", "30M", src, chunkPrefix)

	splitCmd := r.buildKubectlCmd(true, splitArgs...)
	splitCmd.Stderr = os.Stderr

	fmt.Printf("🔪 Dividindo arquivo no pod...\n")
	if err := splitCmd.Run(); err != nil {
		return fmt.Errorf("erro ao dividir arquivo: %w", err)
	}

	// --- 2) List chunks in pod ---
	listArgs := []string{"exec", pod}
	if container != "" {
		listArgs = append(listArgs, "-c", container)
	}
	if namespace != "" {
		listArgs = append(listArgs, "-n", namespace)
	}
	listArgs = append(listArgs, "--", "ls", "-1", "/tmp/")

	listCmd := r.buildKubectlCmd(true, listArgs...)
	output, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("erro ao listar chunks: %w", err)
	}

	// Filter chunk files
	var chunks []string
	prefix := fmt.Sprintf("chunk_%s_", srcFile)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			chunks = append(chunks, "/tmp/"+line)
		}
	}

	if len(chunks) == 0 {
		return fmt.Errorf("nenhum chunk encontrado")
	}

	fmt.Printf("📋 Encontrados %d chunks\n", len(chunks))

	// --- 3) Transfer each chunk ---
	localChunks := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		fmt.Printf("⬇️  Transferindo chunk %d/%d...\n", i+1, len(chunks))

		localChunk := dst + fmt.Sprintf(".chunk%03d", i)
		err := r.copyToHostUsingTar(chunk, pod, container, namespace, localChunk)
		if err != nil {
			// Cleanup on error
			for _, lc := range localChunks {
				os.Remove(lc)
			}
			return fmt.Errorf("erro ao transferir chunk %d: %w", i, err)
		}
		localChunks = append(localChunks, localChunk)
	}

	// --- 4) Reassemble in Go ---
	fmt.Printf("🔗 Juntando chunks...\n")

	outFile, err := os.Create(dst)
	if err != nil {
		// Cleanup
		for _, lc := range localChunks {
			os.Remove(lc)
		}
		return fmt.Errorf("erro ao criar arquivo final: %w", err)
	}
	defer outFile.Close()

	for i, localChunk := range localChunks {
		chunkFile, err := os.Open(localChunk)
		if err != nil {
			return fmt.Errorf("erro ao abrir chunk %d: %w", i, err)
		}

		written, err := io.Copy(outFile, chunkFile)
		chunkFile.Close()

		if err != nil {
			return fmt.Errorf("erro ao copiar chunk %d: %w", i, err)
		}

		if r.config.Debug {
			fmt.Printf("  Chunk %d: %d bytes\n", i, written)
		}
	}

	// --- 5) Cleanup ---
	fmt.Printf("🧹 Limpando chunks temporários...\n")

	// Remove local chunks
	for _, lc := range localChunks {
		os.Remove(lc)
	}

	// Remove chunks from pod
	for _, chunk := range chunks {
		rmArgs := []string{"exec", pod}
		if container != "" {
			rmArgs = append(rmArgs, "-c", container)
		}
		if namespace != "" {
			rmArgs = append(rmArgs, "-n", namespace)
		}
		rmArgs = append(rmArgs, "--", "rm", "-f", chunk)

		rmCmd := r.buildKubectlCmd(true, rmArgs...)
		rmCmd.Run() // Ignore errors
	}

	fmt.Printf("✅ Arquivo copiado com sucesso: %s → %s (%.2f MB)\n", src, dst, float64(fileSize)/(1024*1024))
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
