package container

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// CopyToHost copies a file from a pod to the host using base64 encoding.
// This ensures binary integrity across all platforms including Windows.
func (r KubernetesRuntime) CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	dst = normalizeCopyDstPath(dst)

	// Use base64 encoding to ensure binary integrity
	// Command: base64 -w 0 <file> (Linux) or base64 <file> (may need adjustment for different OS)
	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "sh", "-c", fmt.Sprintf("base64 -w 0 %s 2>/dev/null || base64 %s", src, src))
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)

	// Get stdout
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("erro ao executar comando de cópia: %w", err)
	}

	// Clean the output - remove any whitespace/newlines
	base64Data := strings.TrimSpace(string(output))

	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return fmt.Errorf("erro ao decodificar base64: %w", err)
	}

	// Write to file
	if err := os.WriteFile(dst, decoded, 0644); err != nil {
		return fmt.Errorf("erro ao escrever arquivo: %w", err)
	}

	if r.config.Debug {
		fmt.Printf("✅ Arquivo copiado com sucesso: %s -> %s (%d bytes)\n", src, dst, len(decoded))
	}

	return nil
}
