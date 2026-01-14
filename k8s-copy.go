package container

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// CopyToHost copies a file from a pod to the host using chunked transfer with base64 encoding.
// This approach is resilient to network issues and works with large files by downloading them
// in manageable 2MB chunks, retrying failed blocks up to 3 times.
func (r KubernetesRuntime) CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	// Debug: Always print to verify if Debug flag is being passed
	fmt.Printf("🔍 DEBUG FLAG: r.config.Debug = %v\n", r.config.Debug)

	// First, get the file size
	fileSize, err := r.getRemoteFileSize(src, podOrContainerName, mainContainerName, namespace)
	if err != nil {
		return fmt.Errorf("erro ao obter tamanho do arquivo: %w", err)
	}

	if r.config.Debug {
		fmt.Printf("📦 Arquivo %s tem %d bytes (%.2f MB)\n", src, fileSize, float64(fileSize)/(1024*1024))
	}

	// Download file in chunks
	const chunkSize = 2 * 1024 * 1024 // 2MB chunks
	const maxRetries = 3

	outFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("erro ao criar arquivo de destino: %w", err)
	}
	defer outFile.Close()

	var offset int64 = 0
	chunkNum := 0

	for offset < fileSize {
		chunkNum++
		remaining := fileSize - offset
		currentChunkSize := chunkSize
		if remaining < int64(chunkSize) {
			currentChunkSize = int(remaining)
		}

		if r.config.Debug {
			fmt.Printf("📥 Baixando bloco %d: offset=%d, tamanho=%d bytes (%.1f%%)\n",
				chunkNum, offset, currentChunkSize, float64(offset)*100/float64(fileSize))
		}

		// Retry logic for this chunk
		var chunkData []byte
		var lastErr error

		for attempt := 1; attempt <= maxRetries; attempt++ {
			chunkData, lastErr = r.downloadChunk(src, offset, currentChunkSize, podOrContainerName, mainContainerName, namespace)
			if lastErr == nil {
				break
			}
			if attempt < maxRetries {
				if r.config.Debug {
					fmt.Printf("⚠️  Tentativa %d/%d falhou para bloco %d: %v. Tentando novamente...\n",
						attempt, maxRetries, chunkNum, lastErr)
				}
				time.Sleep(time.Duration(attempt) * time.Second) // Backoff
			}
		}

		if lastErr != nil {
			return fmt.Errorf("erro ao baixar bloco %d após %d tentativas: %w", chunkNum, maxRetries, lastErr)
		}

		// Write chunk to file
		if _, err := outFile.Write(chunkData); err != nil {
			return fmt.Errorf("erro ao escrever bloco %d no arquivo: %w", chunkNum, err)
		}

		offset += int64(len(chunkData))
	}

	if r.config.Debug {
		fmt.Printf("✅ Download completo: %d bytes em %d blocos\n", fileSize, chunkNum)
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

// downloadChunk downloads a chunk of a file using dd and base64 encoding.
// It extracts a specific portion of the file starting at offset with the given size.
func (r KubernetesRuntime) downloadChunk(filePath string, offset int64, size int, podOrContainerName, mainContainerName, namespace string) ([]byte, error) {
	// Use dd to extract chunk and base64 to encode it
	// dd if=/path/to/file bs=1 skip=offset count=size 2>/dev/null | base64
	ddCmd := fmt.Sprintf("dd if=%s bs=1 skip=%d count=%d 2>/dev/null | base64", filePath, offset, size)

	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "sh", "-c", ddCmd)
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("erro ao executar dd+base64: %w. stderr: %s", err, stderr.String())
	}

	// Clean base64 content: remove all whitespace and keep only valid base64 characters
	base64Content := stdout.Bytes()
	cleanedBase64 := make([]byte, 0, len(base64Content))
	for _, b := range base64Content {
		// Keep only valid base64 characters: A-Z, a-z, 0-9, +, /, =
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '+' || b == '/' || b == '=' {
			cleanedBase64 = append(cleanedBase64, b)
		}
	}

	// Decode base64 content
	decodedContent, err := base64.StdEncoding.DecodeString(string(cleanedBase64))
	if err != nil {
		return nil, fmt.Errorf("erro ao decodificar base64: %w (tamanho original: %d, limpo: %d)", err, len(base64Content), len(cleanedBase64))
	}

	return decodedContent, nil
}
