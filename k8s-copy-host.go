package container

import (
	"fmt"
)

// CopyToHost copies a file from a pod to the host using kubectl cp.
// Uses --retries=6 for reliability on Windows and other platforms.
func (r KubernetesRuntime) CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	dst = normalizeCopyDstPath(dst)

	// Build kubectl cp command with retries
	// Format: kubectl cp <namespace>/<pod>:<src> <dst> --retries=6
	podPath := fmt.Sprintf("%s:%s", podOrContainerName, src)

	cpArgs := []string{"cp", podPath, dst, "--retries=6"}
	if mainContainerName != "" {
		cpArgs = append(cpArgs, "-c", mainContainerName)
	}
	cpArgs = addNamespaceArg(namespace, cpArgs)

	cmd := r.buildKubectlCmd(false, cpArgs...)

	if err := r.runKubectlCommand(cmd, "erro ao copiar arquivo do pod"); err != nil {
		return err
	}

	if r.config.Debug {
		fmt.Printf("✅ Arquivo copiado com sucesso: %s -> %s\n", src, dst)
	}

	return nil
}
