package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type KubernetesRuntime struct {
	config TContainerRuntimeConfig
}

// -------------------- Factory --------------------

func NewKubernetesRuntimeFactory(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	kubectlBinPath, err := getKubectlBinPath()
	if err != nil {
		return nil, err
	}
	config.CommandBinPath = kubectlBinPath

	return KubernetesRuntime{config: config}, nil
}

func getKubectlBinPath() (string, error) {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return "", fmt.Errorf("não encontrei o binário do kubectl no PATH")
	}
	return path, nil
}

// -------------------- Comandos base --------------------

func (r KubernetesRuntime) buildKubectlArgs(args ...string) []string {
	finalArgs := []string{}
	// Suporte futuro a context, namespace, etc.
	finalArgs = append(finalArgs, args...)
	return finalArgs
}

func (r KubernetesRuntime) buildKubectlCmd(captureOutput bool, args ...string) *exec.Cmd {
	return r.buildKubectlCmdWithContext(context.Background(), captureOutput, args...)
}

func (r KubernetesRuntime) buildKubectlCmdWithContext(ctx context.Context, captureOutput bool, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.config.CommandBinPath, r.buildKubectlArgs(args...)...)
	if !captureOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}

func addNamespaceArg(namespace string, args []string) []string {
	if namespace == "" || len(args) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0])
	out = append(out, "-n", namespace)
	out = append(out, args[1:]...)
	return out
}

// -------------------- Métodos principais --------------------

// Up cria o pod/deployment a partir de um manifesto YAML
func (r KubernetesRuntime) Up(podOrContainerName, namespace, manifestFile string, waitContainerRunning bool) error {
	args := addNamespaceArg(namespace, []string{"apply", "-f", manifestFile})
	cmd := r.buildKubectlCmd(false, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao aplicar manifesto: %w", err)
	}

	if waitContainerRunning {
		if err := r.WaitContainerRunning(podOrContainerName, namespace, 120*time.Second); err != nil {
			return fmt.Errorf("o pod %s não ficou pronto: %w", podOrContainerName, err)
		}
	}

	return nil
}

func (r KubernetesRuntime) Down(podOrContainerName, namespace string) error {
	deletePodArgs := addNamespaceArg(namespace, []string{"delete", "pod", podOrContainerName, "--ignore-not-found", "--grace-period", "3"})
	cmd := r.buildKubectlCmd(false, deletePodArgs...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao deletar pod %s: %w", podOrContainerName, err)
	}
	deleteSvcArgs := addNamespaceArg(namespace, []string{"delete", "svc", podOrContainerName, "--ignore-not-found"})
	cmd = r.buildKubectlCmd(false, deleteSvcArgs...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao deletar svc %s: %w", podOrContainerName, err)
	}
	return nil
}

func (r KubernetesRuntime) IsContainerRunning(podOrContainerName, namespace string) (bool, error) {
	var stdout, stderr bytes.Buffer

	for attempt := 1; attempt <= 2; attempt++ {
		args := addNamespaceArg(namespace, []string{"get", "pod", podOrContainerName, "-o", "jsonpath={.status.phase}"})
		cmd := r.buildKubectlCmd(true, args...)
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		if err == nil {
			status := strings.TrimSpace(stdout.String())
			return (status == "Running" || status == "Succeeded"), nil
		}

		stderrStr := strings.TrimSpace(stderr.String())

		// Se for erro de "not found", pode parar imediatamente
		if strings.Contains(stderrStr, "NotFound") {
			return false, nil
		}

		// Retry simples
		time.Sleep(1 * time.Second)
	}

	return false, fmt.Errorf("não foi possível verificar o estado do pod '%s' após 2 tentativas", podOrContainerName)
}

func (r KubernetesRuntime) WaitContainerRunning(podOrContainerName, namespace string, timeout time.Duration) error {
	const interval = 2 * time.Second
	start := time.Now()
	time.Sleep(interval)
	for {
		running, _ := r.IsContainerRunning(podOrContainerName, namespace)
		if running {
			args := addNamespaceArg(namespace, []string{"get", "pod", podOrContainerName, "-o", "jsonpath={.status.containerStatuses[0].ready}"})
			cmd := r.buildKubectlCmd(true, args...)
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stdout
			_ = cmd.Run()

			if strings.TrimSpace(stdout.String()) == "true" {
				return nil
			}
		}

		if time.Since(start) > timeout {
			return fmt.Errorf("timeout aguardando pod '%s' ficar pronto", podOrContainerName)
		}
		time.Sleep(interval)
	}
}

func (r KubernetesRuntime) StopContainer(podOrContainerName, namespace string) error {
	args := addNamespaceArg(namespace, []string{"delete", "pod", podOrContainerName})
	cmd := r.buildKubectlCmd(false, args...)
	return cmd.Run()
}

func (r KubernetesRuntime) ShowLogs(podOrContainerName, namespace string) error {
	args := addNamespaceArg(namespace, []string{"logs", "-f", podOrContainerName})
	cmd := r.buildKubectlCmd(false, args...)
	return cmd.Run()
}

func (r KubernetesRuntime) ExecInContainer(podOrContainerName, namespace string, cmdArgs []string) ([]byte, error) {
	args := append([]string{"exec", podOrContainerName, "--"}, cmdArgs...)
	args = addNamespaceArg(namespace, args)
	cmd := r.buildKubectlCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("erro ao executar comando no pod: %w. Stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// -------------------- Utilidades --------------------

func (r KubernetesRuntime) GetContainerIP(podOrContainerName, namespace string) (string, error) {
	args := addNamespaceArg(namespace, []string{"get", "pod", podOrContainerName, "-o", "jsonpath={.status.podIP}"})
	cmd := r.buildKubectlCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("falha ao obter IP do pod %s: %w. Stderr: %s", podOrContainerName, err, stderr.String())
	}

	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "", fmt.Errorf("não foi possível obter IP do pod %s", podOrContainerName)
	}

	return ip, nil
}

func (r KubernetesRuntime) CopyToContainer(srcPath, podOrContainerName, namespace, destPath string) error {
	srcPath = filepath.ToSlash(srcPath)
	destDir := path.Dir(destPath)
	tmpName := filepath.Base(destPath) + ".tmp"
	tmpDestPath := path.Join(destDir, tmpName)

	// Copia o arquivo para o container com nome temporário
	copyArgs := addNamespaceArg(namespace, []string{"cp", srcPath, fmt.Sprintf("%s:%s", podOrContainerName, tmpDestPath)})
	copyCmd := r.buildKubectlCmd(false, copyArgs...)
	copyCmd.Stdout = os.Stdout
	copyCmd.Stderr = os.Stderr
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar arquivo temporário para o pod: %w", err)
	}

	// Move o arquivo dentro do container (rename atômico)
	mvArgs := addNamespaceArg(namespace, []string{"exec", podOrContainerName, "--", "mv", "-f", tmpDestPath, destPath})
	mvCmd := r.buildKubectlCmd(false, mvArgs...)
	mvCmd.Stdout = os.Stdout
	mvCmd.Stderr = os.Stderr
	if err := mvCmd.Run(); err != nil {
		return fmt.Errorf("erro ao mover arquivo dentro do pod: %w", err)
	}

	return nil
}

func (r KubernetesRuntime) CopyToHost(src, podOrContainerName, namespace, dst string) error {
	args := addNamespaceArg(namespace, []string{"cp", fmt.Sprintf("%s:%s", podOrContainerName, src), dst})
	cmd := r.buildKubectlCmd(false, args...)

	// Cria buffers separados para stdout e stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// Redireciona stderr, mas filtra apenas o warning do tar
	cmd.Stderr = io.Discard // descarta tudo de stderr, incluindo o tar warning

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar arquivo do pod: %w", err)
	}

	return nil
}

func (r KubernetesRuntime) WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podOrContainerName, namespace string) (bool, error) {
	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutChan:
			return false, fmt.Errorf("timeout esperando arquivo %s aparecer no pod %s", fileName, podOrContainerName)
		case <-ticker.C:
			running, _ := r.IsContainerRunning(podOrContainerName, namespace)
			if running {
				_, err := r.ExecInContainer(podOrContainerName, namespace, []string{"test", "-f", fileName})
				if err == nil {
					return true, nil
				}
			} else {
				return false, ErrContainerNotFound
			}
		}
	}
}

func (r KubernetesRuntime) GetStorageClassList() ([]TStorageClass, error) {
	cmd := r.buildKubectlCmd(true, "get", "storageclass", "-o", "json")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errOutput := strings.TrimSpace(stderr.String())
		return nil, fmt.Errorf("erro ao listar storageclasses: %w. Stderr: %s", err, errOutput)
	}

	type storageClassMetadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	}

	type storageClassItem struct {
		Metadata storageClassMetadata `json:"metadata"`
	}

	var scList struct {
		Items []storageClassItem `json:"items"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &scList); err != nil {
		return nil, fmt.Errorf("erro ao interpretar storageclasses: %w", err)
	}

	result := make([]TStorageClass, 0, len(scList.Items))
	for _, item := range scList.Items {
		isDefault := false
		if annotations := item.Metadata.Annotations; annotations != nil {
			for _, key := range []string{
				"storageclass.kubernetes.io/is-default-class",
				"storageclass.beta.kubernetes.io/is-default-class",
			} {
				if value, ok := annotations[key]; ok && strings.EqualFold(value, "true") {
					isDefault = true
					break
				}
			}
		}

		result = append(result, TStorageClass{
			Name:      item.Metadata.Name,
			IsDefault: isDefault,
		})
	}

	return result, nil
}

// -------------------- Métodos não usados no Kubernetes --------------------

func (r KubernetesRuntime) CreateNetwork(networkName, subnet, ipRange, gateway, label string) error {
	// Kubernetes não usa redes customizadas como Docker; pode ser ignorado
	return nil
}

func (r KubernetesRuntime) IsNetworkExist(networkName string) bool {
	// Não aplicável
	return true
}

func (r KubernetesRuntime) CreateVolume(volumeName string) error {
	// Kubernetes usa PersistentVolumeClaim, mas podemos ignorar por ora
	return nil
}

func (r KubernetesRuntime) IsVolumeExist(volumeName string) bool {
	// Não aplicável
	return true
}
