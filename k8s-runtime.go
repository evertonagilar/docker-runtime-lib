package container

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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
	config.commandBinPath = kubectlBinPath

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
	cmd := exec.Command(r.config.commandBinPath, r.buildKubectlArgs(args...)...)
	if !captureOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}

// -------------------- Métodos principais --------------------

// Up cria o pod/deployment a partir de um manifesto YAML
func (r KubernetesRuntime) Up(containerName, manifestFile string, WaitContainerRunning bool) error {
	cmd := r.buildKubectlCmd(false, "apply", "-f", manifestFile)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao aplicar manifesto: %w", err)
	}

	if WaitContainerRunning {
		if err := r.WaitContainerRunning(containerName, 120*time.Second); err != nil {
			return fmt.Errorf("o pod %s não ficou pronto: %w", containerName, err)
		}
	}

	return nil
}

func (r KubernetesRuntime) Down(containerName string) error {
	cmd := r.buildKubectlCmd(false, "delete", "pod", containerName, "--ignore-not-found")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao deletar pod %s: %w", containerName, err)
	}
	return nil
}

func (r KubernetesRuntime) IsContainerRunning(containerName string) (bool, error) {
	cmd := r.buildKubectlCmd(true, "get", "pod", containerName, "-o", "jsonpath={.status.phase}")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return false, nil
	}

	status := strings.TrimSpace(stdout.String())
	return status == "Running", nil
}

func (r KubernetesRuntime) WaitContainerRunning(containerName string, timeout time.Duration) error {
	timeoutChan := time.After(timeout)
	tick := time.Tick(2 * time.Second)
	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timeout esperando pod %s ficar Running", containerName)
		case <-tick:
			running, _ := r.IsContainerRunning(containerName)
			if running {
				return nil
			}
		}
	}
}

func (r KubernetesRuntime) StopContainer(containerName string) error {
	cmd := r.buildKubectlCmd(false, "delete", "pod", containerName)
	return cmd.Run()
}

func (r KubernetesRuntime) ShowLogs(containerName string) error {
	cmd := r.buildKubectlCmd(false, "logs", "-f", containerName)
	return cmd.Run()
}

func (r KubernetesRuntime) ExecInContainer(containerName string, cmdArgs []string) ([]byte, error) {
	args := append([]string{"exec", containerName, "--"}, cmdArgs...)
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

func (r KubernetesRuntime) GetContainerIP(containerName string) (string, error) {
	cmd := r.buildKubectlCmd(true, "get", "pod", containerName, "-o", "jsonpath={.status.podIP}")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("falha ao obter IP do pod %s: %w. Stderr: %s", containerName, err, stderr.String())
	}

	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "", fmt.Errorf("não foi possível obter IP do pod %s", containerName)
	}

	return ip, nil
}

func (r KubernetesRuntime) CopyToContainer(srcPath, containerName, destPath string) error {
	srcPath = filepath.ToSlash(srcPath)
	cmd := r.buildKubectlCmd(false, "cp", srcPath, fmt.Sprintf("%s:%s", containerName, destPath))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar arquivo para o pod: %w", err)
	}
	return nil
}

func (r KubernetesRuntime) CopyToHost(src, containerName, dst string) error {
	cmd := r.buildKubectlCmd(false, "cp", fmt.Sprintf("%s:%s", containerName, src), dst)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar arquivo do pod: %w", err)
	}
	return nil
}

func (r KubernetesRuntime) WaitForFile(fileName string, timeout time.Duration, interval time.Duration, containerName string) (bool, error) {
	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutChan:
			return false, fmt.Errorf("timeout esperando arquivo %s aparecer no pod %s", fileName, containerName)
		case <-ticker.C:
			running, _ := r.IsContainerRunning(containerName)
			if running {
				_, err := r.ExecInContainer(containerName, []string{"test", "-f", fileName})
				if err == nil {
					return true, nil
				}
			} else {
				return false, ErrContainerNotFound
			}
		}
	}
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
