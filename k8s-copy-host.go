package container

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// CopyToHost copies a file from a pod to the host using a single streaming connection.
// It uses 'cat' to stream the file content directly to stdout, which is then written to the destination file.
// This is significantly faster than chunked transfer as it avoids the overhead of
// multiple process startups and base64 encoding/decoding.
func (r KubernetesRuntime) CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	// 1. Check file size first (just for logging/debug)
	fileSize, err := r.getRemoteFileSize(src, podOrContainerName, mainContainerName, namespace)
	if err == nil && r.config.Debug {
		fmt.Printf("📦 Iniciando download de %s (%.2f MB)\n", src, float64(fileSize)/(1024*1024))
	}

	// 2. Setup execution command: cat <file>
	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "cat", src)
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)

	// 3. Get stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("erro ao criar pipe de stdout: %w", err)
	}

	// Capture stderr for debugging
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// 4. Start command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("erro ao iniciar comando de cópia: %w", err)
	}

	// 5. Create destination file
	outFile, err := os.Create(dst)
	if err != nil {
		// Try to kill process if file create fails
		_ = cmd.Process.Kill()
		return fmt.Errorf("erro ao criar arquivo de destino: %w", err)
	}
	defer outFile.Close()

	// 6. Copy content directly from stdout to file
	copied, err := io.Copy(outFile, stdout)
	if err != nil {
		return fmt.Errorf("erro ao escrever dados no arquivo: %w", err)
	}

	// 7. Wait for command to complete and check exit code
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("comando de cópia falhou: %w, stderr: %s", err, stderr.String())
	}

	if r.config.Debug {
		fmt.Printf("✅ Download concluído: %d bytes copiados\n", copied)
	}

	// Verify size match if we got the size initially
	if fileSize > 0 && copied != fileSize {
		// Just a warning, not an error, as file might have changed
		if r.config.Debug {
			fmt.Printf("⚠️  Aviso: Tamanho copiado (%d) difere do tamanho original (%d)\n", copied, fileSize)
		}
	}

	return nil
}

// getRemoteFileSize gets the size of a file in the pod using stat command
func (r KubernetesRuntime) getRemoteFileSize(filePath, podOrContainerName, mainContainerName, namespace string) (int64, error) {
	// Use stat to get file size: stat -c %s /path/to/file
	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "stat", "-c", "%s", filePath)
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("erro ao executar stat: %w. stderr: %s", err, stderr.String())
	}

	sizeStr := strings.TrimSpace(stdout.String())
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("erro ao parsear tamanho do arquivo '%s': %w", sizeStr, err)
	}

	return size, nil
}
