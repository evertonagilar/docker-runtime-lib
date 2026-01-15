package container

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	chunkSize = 10 * 1024 * 1024 // 10MB chunks
)

// CopyToHost copies a file from a pod to the host using chunked transfer with hash verification.
// This ensures binary integrity by splitting large files into smaller chunks.
func (r KubernetesRuntime) CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	dst = normalizeCopyDstPath(dst)

	// 1. Get file size
	fileSize, err := r.getRemoteFileSize(src, podOrContainerName, mainContainerName, namespace)
	if err != nil {
		return fmt.Errorf("erro ao obter tamanho do arquivo: %w", err)
	}

	if r.config.Debug {
		fmt.Printf("📦 Arquivo a copiar: %s (%d bytes)\n", src, fileSize)
	}

	// 2. Calculate number of chunks
	numChunks := int((fileSize + chunkSize - 1) / chunkSize)

	if r.config.Debug {
		fmt.Printf("📦 Dividindo em %d chunks de ~%d MB\n", numChunks, chunkSize/(1024*1024))
	}

	// 3. Create temporary directory for chunks
	tmpDir, err := os.MkdirTemp("", "k8s-copy-*")
	if err != nil {
		return fmt.Errorf("erro ao criar diretório temporário: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 4. Copy each chunk
	for i := 0; i < numChunks; i++ {
		offset := int64(i) * chunkSize
		size := chunkSize
		if offset+int64(size) > fileSize {
			size = int(fileSize - offset)
		}

		if err := r.copyChunk(src, podOrContainerName, mainContainerName, namespace, tmpDir, i, offset, int64(size)); err != nil {
			return fmt.Errorf("erro ao copiar chunk %d: %w", i, err)
		}

		if r.config.Debug {
			fmt.Printf("✅ Chunk %d/%d copiado e verificado\n", i+1, numChunks)
		}
	}

	// 5. Reassemble chunks
	if err := r.reassembleChunks(tmpDir, numChunks, dst); err != nil {
		return fmt.Errorf("erro ao remontar arquivo: %w", err)
	}

	if r.config.Debug {
		fmt.Printf("✅ Arquivo copiado com sucesso: %s -> %s\n", src, dst)
	}

	return nil
}

// copyChunk copies a single chunk from the pod and verifies its hash
func (r KubernetesRuntime) copyChunk(src, podOrContainerName, mainContainerName, namespace, tmpDir string, chunkIndex int, offset, size int64) error {
	chunkFile := filepath.Join(tmpDir, fmt.Sprintf("chunk_%04d", chunkIndex))

	// Step 1: Extract chunk and encode to base64
	script := fmt.Sprintf("dd if=%s bs=1 skip=%d count=%d 2>/dev/null | base64 -w 0", src, offset, size)

	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "sh", "-c", script)
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("erro ao extrair chunk: %w", err)
	}

	base64Data := strings.TrimSpace(string(output))

	// Step 2: Calculate MD5 of the same chunk on remote
	md5Script := fmt.Sprintf("dd if=%s bs=1 skip=%d count=%d 2>/dev/null | md5sum | awk '{print $1}'", src, offset, size)

	md5Args := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		md5Args = append(md5Args, "-c", mainContainerName)
	}
	md5Args = append(md5Args, "--", "sh", "-c", md5Script)
	md5Args = addNamespaceArg(namespace, md5Args)

	md5Cmd := r.buildKubectlCmd(true, md5Args...)
	md5Output, err := md5Cmd.Output()
	if err != nil {
		return fmt.Errorf("erro ao calcular MD5 remoto: %w", err)
	}

	remoteMD5 := strings.TrimSpace(string(md5Output))

	// Decode base64
	decoded := make([]byte, len(base64Data)*3/4+3)
	n, err := decodeBase64(base64Data, decoded)
	if err != nil {
		return fmt.Errorf("erro ao decodificar base64: %w", err)
	}
	decoded = decoded[:n]

	// Calculate local MD5
	localMD5 := md5.Sum(decoded)
	localMD5Str := hex.EncodeToString(localMD5[:])

	// Verify hash
	if localMD5Str != remoteMD5 {
		return fmt.Errorf("hash mismatch: local=%s remote=%s", localMD5Str, remoteMD5)
	}

	// Write chunk
	if err := os.WriteFile(chunkFile, decoded, 0644); err != nil {
		return fmt.Errorf("erro ao escrever chunk: %w", err)
	}

	return nil
}

// reassembleChunks combines all chunks into the final file
func (r KubernetesRuntime) reassembleChunks(tmpDir string, numChunks int, dst string) error {
	outFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("erro ao criar arquivo de destino: %w", err)
	}
	defer outFile.Close()

	for i := 0; i < numChunks; i++ {
		chunkFile := filepath.Join(tmpDir, fmt.Sprintf("chunk_%04d", i))

		chunk, err := os.Open(chunkFile)
		if err != nil {
			return fmt.Errorf("erro ao abrir chunk %d: %w", i, err)
		}

		if _, err := io.Copy(outFile, chunk); err != nil {
			chunk.Close()
			return fmt.Errorf("erro ao copiar chunk %d: %w", i, err)
		}
		chunk.Close()
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
		return 0, fmt.Errorf("erro ao executar stat: %w", err)
	}

	sizeStr := strings.TrimSpace(string(output))
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("erro ao parsear tamanho '%s': %w", sizeStr, err)
	}

	return size, nil
}

// decodeBase64 decodes base64 string into byte slice
func decodeBase64(s string, dst []byte) (int, error) {
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	n := 0
	for i := 0; i < len(s); i += 4 {
		if i+4 > len(s) {
			break
		}

		var val uint32
		for j := 0; j < 4; j++ {
			c := s[i+j]
			var v byte
			if c >= 'A' && c <= 'Z' {
				v = c - 'A'
			} else if c >= 'a' && c <= 'z' {
				v = c - 'a' + 26
			} else if c >= '0' && c <= '9' {
				v = c - '0' + 52
			} else if c == '+' {
				v = 62
			} else if c == '/' {
				v = 63
			} else if c == '=' {
				break
			} else {
				return 0, fmt.Errorf("invalid base64 character: %c", c)
			}
			val = (val << 6) | uint32(v)
		}

		dst[n] = byte(val >> 16)
		dst[n+1] = byte(val >> 8)
		dst[n+2] = byte(val)
		n += 3
	}

	return n, nil
}
