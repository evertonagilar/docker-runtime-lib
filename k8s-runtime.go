package container

import (
	"bytes"
	"fmt"
	"io"
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
	cmd := exec.Command(r.config.CommandBinPath, r.buildKubectlArgs(args...)...)
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
	var stdout, stderr bytes.Buffer
	var err error

	for attempt := 1; attempt <= 2; attempt++ {
		cmd := r.buildKubectlCmd(true, "get", "pod", containerName, "-o", "jsonpath={.status.phase}")
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err == nil {
			status := strings.TrimSpace(stdout.String())
			return status == "Running", nil
		}

		// Se falhou na primeira tentativa, aguarda e tenta novamente
		if attempt == 1 {
			fmt.Printf("⚠️  Falha ao verificar pod '%s' (tentativa 1): %v\n", containerName, err)
			time.Sleep(1 * time.Second)
			continue
		}
	}

	// Se chegou aqui, tentou 2x sem sucesso
	return false, nil
}

func (r KubernetesRuntime) WaitContainerRunning(containerName string, timeout time.Duration) error {
	const interval = 2 * time.Second
	start := time.Now()
	time.Sleep(interval)
	for {
		running, _ := r.IsContainerRunning(containerName)
		if running {
			cmd := r.buildKubectlCmd(true, "get", "pod", containerName, "-o", "jsonpath={.status.containerStatuses[0].ready}")
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stdout
			_ = cmd.Run()

			if strings.TrimSpace(stdout.String()) == "true" {
				fmt.Printf("✅ Pod '%s' está em execução.\n", containerName)
				return nil
			}
		}

		if time.Since(start) > timeout {
			return fmt.Errorf("timeout aguardando pod '%s' ficar pronto", containerName)
		}
		time.Sleep(interval)
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
	fullCmd := []string{"cp", srcPath, fmt.Sprintf("%s:%s", containerName, destPath)}

	fmt.Printf("📤 Executando kubectl %s\n", strings.Join(fullCmd, " "))

	cmd := r.buildKubectlCmd(false, fullCmd...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("❌ Erro ao executar kubectl cp (%.2fs): %v\n", elapsed.Seconds(), err)
		return fmt.Errorf("erro ao copiar arquivo para o pod: %w", err)
	}

	fmt.Printf("✅ Cópia concluída em %.2fs\n", elapsed.Seconds())
	return nil
}

func (r KubernetesRuntime) CopyToHost(src, containerName, dst string) error {
	cmd := r.buildKubectlCmd(false, "cp", fmt.Sprintf("%s:%s", containerName, src), dst)

	// Cria buffers separados para stdout e stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// Redireciona stderr, mas filtra apenas o warning do tar
	cmd.Stderr = io.Discard // descarta tudo de stderr, incluindo o tar warning

	fmt.Printf("📋 Comando kubectl que será executado: %s\n", strings.Join(cmd.Args, " "))
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
