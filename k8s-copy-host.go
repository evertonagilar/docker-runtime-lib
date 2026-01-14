package container

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CopyToHost copies a file from a pod to the host using a single streaming connection.
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

	// 2. Prepare tar command string
	// tar -cf - -C <dir> <filename>
	srcDir := filepath.Dir(src)
	srcFile := filepath.Base(src)
	tarCmd := fmt.Sprintf("tar cf - -C %s %s", srcDir, srcFile)

	// 3. Setup execution command
	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "sh", "-c", tarCmd)
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)

	// 4. Get stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("erro ao criar pipe de stdout: %w", err)
	}

	// Capture stderr for debugging
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// 5. Start command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("erro ao iniciar comando de cópia: %w", err)
	}

	// 6. Process the tar stream
	tr := tar.NewReader(stdout)

	// We expect only one file (or multiple if directory, but CopyToHost implies single target usually)
	// We'll extract the first matching entry to dst local file
	found := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Wait for command to finish to get exit code
			_ = cmd.Wait()
			return fmt.Errorf("erro ao ler stream tar: %w (stderr: %s)", err, stderr.String())
		}

		if header.Name == srcFile {
			found = true

			// Open destination file
			outFile, err := os.Create(dst)
			if err != nil {
				_ = cmd.Wait()
				return fmt.Errorf("erro ao criar arquivo de destino: %w", err)
			}

			// Copy content directly
			copied, err := io.Copy(outFile, tr)
			outFile.Close()
			if err != nil {
				_ = cmd.Wait()
				return fmt.Errorf("erro ao escrever dados no arquivo: %w", err)
			}

			if r.config.Debug {
				fmt.Printf("✅ Download concluído: %d bytes copiados\n", copied)
			}

			// We found and copied our file, we can stop
			break
		}
	}

	// 7. Wait for command to complete
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("comando de cópia falhou: %w, stderr: %s", err, stderr.String())
	}

	if !found {
		// If we didn't find the file in the tar stream (e.g. empty or wrong path)
		// Try fallback with cat if tar failed silently? No, cmd.Wait should have caught it.
		// If tar succeeds but empty, maybe file doesn't exist?
		return fmt.Errorf("arquivo or diretório não encontrado no stream")
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
