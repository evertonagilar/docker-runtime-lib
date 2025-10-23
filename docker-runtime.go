package container

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type DockerRuntime struct {
	config TContainerRuntimeConfig
}

func NewDockerRuntimeFactory(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	dockerBinPath, err := getDockerBinPath()
	if err != nil {
		return nil, err
	}
	config.CommandBinPath = dockerBinPath

	// Valida os caminhos TLS
	if err := validateTLSPaths(config); err != nil {
		return nil, err
	}

	return DockerRuntime{config: config}, nil
}

func (r DockerRuntime) buildDockerArgs(args ...string) []string {
	finalArgs := []string{}
	if r.config.RemoteHost != "" {
		finalArgs = append(finalArgs, "--host", r.config.RemoteHost)
	}
	if r.config.TLSCaCertPath != "" {
		finalArgs = append(finalArgs, "--tlscacert", r.config.TLSCaCertPath)
	}
	if r.config.TLSCertPath != "" {
		finalArgs = append(finalArgs, "--tlscert", r.config.TLSCertPath)
	}
	if r.config.TLSKeyPath != "" {
		finalArgs = append(finalArgs, "--tlskey", r.config.TLSKeyPath)
	}
	if r.config.TLSCaCertPath != "" || r.config.TLSCertPath != "" || r.config.TLSKeyPath != "" {
		finalArgs = append(finalArgs, "--tlsverify")
	}
	finalArgs = append(finalArgs, args...)
	return finalArgs
}

// buildDockerCmd cria *exec.Cmd com opção de capturar saída
func (r DockerRuntime) buildDockerCmd(captureOutput bool, args ...string) *exec.Cmd {
	cmd := exec.Command(r.config.CommandBinPath, r.buildDockerArgs(args...)...)
	if !captureOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}

func (r DockerRuntime) Up(podName, namespace, manifestFile string, waitContainerRunning bool) error {
	cmd := r.buildDockerCmd(false, "compose", "-f", manifestFile, "up", "-d")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao executar docker-compose up: %w", err)
	}

	if waitContainerRunning {
		if err := r.WaitContainerRunning(podName, namespace, 60*time.Second); err != nil {
			return fmt.Errorf("container não subiu corretamente: %w", err)
		}
	}

	return nil
}

func (r DockerRuntime) Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumes []TVolume, otherOptionsList []string, namespace, podName, containerName, storageClass string) error {
	_ = namespace
	_ = storageClass
	args := []string{"run"}

	if runtime.GOOS != "windows" {
		if uid != "" && uid != "0" {
			args = append(args, "-e", "HOST_UID="+uid)
		}
		if gid != "" && gid != "0" {
			args = append(args, "-e", "HOST_GID="+gid)
		}
	}

	for _, volume := range volumes {
		if volume.HostPath == "" || volume.MountPath == "" {
			return fmt.Errorf("volume inválido: hostPath e mountPath são obrigatórios")
		}
		mapping := fmt.Sprintf("%s:%s", volume.HostPath, volume.MountPath)
		if volume.ReadOnly {
			mapping = fmt.Sprintf("%s:ro", mapping)
		}
		args = append(args, "-v", mapping)
	}
	if chDir != "" {
		args = append(args, "-w", chDir)
	}
	targetName := podName
	if targetName == "" {
		targetName = containerName
	}
	if targetName != "" {
		args = append(args, "--name", targetName)
	}
	args = append(args, otherOptionsList...)
	args = append(args, image)
	if cmdStr != "" {
		if entrypoint == "" {
			entrypoint = "/usr/bin/bash"
		}
		args = append(args, entrypoint, "-c", cmdStr)
	}

	if r.config.Debug {
		fmt.Printf("🔨 Comando docker: %s %s\n", r.config.CommandBinPath, strings.Join(args, " "))
	}

	cmd := r.buildDockerCmd(false, args...)
	return cmd.Run()
}

func (r DockerRuntime) Down(podName, _ string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, podName)

	stopCmd := r.buildDockerCmd(false, args...)
	if err := stopCmd.Run(); err != nil {
		return fmt.Errorf("falha ao parar container: %w", err)
	}

	return nil
}

func (r DockerRuntime) CopyToContainer(src, podName, containerName, namespace, dst string) error {
	_ = namespace
	targetName := containerName
	if targetName == "" {
		targetName = podName
	}
	if targetName == "" {
		return fmt.Errorf("nome do container não informado")
	}

	destDir := path.Dir(dst)
	tempName := filepath.Base(dst) + ".tmp"
	tmpDestPath := path.Join(destDir, tempName)
	src = filepath.ToSlash(src)

	copyCmd := r.buildDockerCmd(false, "cp", "-L", "-q", src, fmt.Sprintf("%s:%s", targetName, tmpDestPath))
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar para o container: %w", err)
	}

	mvCmd := r.buildDockerCmd(false, "exec", targetName, "mv", tmpDestPath, dst)
	if err := mvCmd.Run(); err != nil {
		return fmt.Errorf("erro ao mover arquivo dentro do container: %w", err)
	}

	return nil
}

func (r DockerRuntime) CopyToHost(src, podName, containerName, namespace, dst string) error {
	_ = namespace
	targetName := containerName
	if targetName == "" {
		targetName = podName
	}
	if targetName == "" {
		return fmt.Errorf("nome do container não informado")
	}

	copyCmd := r.buildDockerCmd(false, "cp", "-L", "-q", fmt.Sprintf("%s:%s", targetName, src), dst)
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar para o container: %w", err)
	}

	return nil
}

func (r DockerRuntime) WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podName, containerName, namespace string) (bool, error) {
	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	targetName := podName
	if targetName == "" {
		targetName = containerName
	}
	if targetName == "" {
		return false, fmt.Errorf("nome do container não informado")
	}
	for {
		select {
		case <-timeoutChan:
			return false, fmt.Errorf("timeout esperando arquivo %s aparecer no container %s", fileName, targetName)
		case <-ticker.C:
			running, _ := r.IsContainerRunning(targetName, namespace)
			if running {
				_, err := r.ExecInContainer(podName, containerName, namespace, []string{"/usr/bin/test", "-f", fileName})
				if err == nil {
					return true, nil
				}
			} else {
				return false, ErrContainerNotFound
			}
		}
	}
}

func (r DockerRuntime) GetContainerStatus(podName, namespace string) (ContainerStatus, error) {
	_ = namespace
	cmd := r.buildDockerCmd(true, "inspect", "-f", "{{.State.Status}}", podName)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrStr, "No such object") || strings.Contains(stderrStr, "Error: No such container") {
			return ContainerStatusNotFound, nil
		}
		return ContainerStatusUnknown, fmt.Errorf("erro ao inspecionar container %s: %w. Stderr: %s", podName, err, stderrStr)
	}

	switch strings.TrimSpace(stdout.String()) {
	case "running":
		return ContainerStatusRunning, nil
	case "created", "restarting", "removing":
		return ContainerStatusPending, nil
	case "exited", "dead":
		return ContainerStatusStopped, nil
	case "paused":
		return ContainerStatusPaused, nil
	default:
		return ContainerStatusUnknown, nil
	}
}

func (r DockerRuntime) IsContainerRunning(podName, namespace string) (bool, error) {
	status, err := r.GetContainerStatus(podName, namespace)
	if err != nil {
		return false, err
	}
	return status == ContainerStatusRunning, nil
}

func (r DockerRuntime) WaitContainerRunning(podName, namespace string, timeout time.Duration) error {
	timeoutChan := time.After(timeout)
	tick := time.Tick(1 * time.Second)
	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timeout esperando container %s subir", podName)
		case <-tick:
			running, _ := r.IsContainerRunning(podName, namespace)
			if running {
				return nil
			}
		}
	}
}

func (r DockerRuntime) StopContainer(podName, namespace string) error {
	_ = namespace
	cmd := r.buildDockerCmd(false, "stop", podName)
	return cmd.Run()
}

func (r DockerRuntime) ShowLogs(podName, containerName, namespace string) error {
	_ = namespace
	_ = containerName
	cmd := r.buildDockerCmd(false, "logs", "-f", podName)
	return cmd.Run()
}

func (r DockerRuntime) ExecInContainer(podName, containerName, namespace string, cmdArgs []string) ([]byte, error) {
	_ = namespace
	targetName := podName
	if targetName == "" {
		targetName = containerName
	}
	if targetName == "" {
		return nil, fmt.Errorf("nome do container não informado")
	}

	args := append([]string{"exec", targetName}, cmdArgs...)
	cmd := r.buildDockerCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("erro ao executar comando no container: %w. Stderr: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (r DockerRuntime) GetContainerIP(podName, namespace string) (string, error) {
	_ = namespace
	cmd := r.buildDockerCmd(true, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", podName)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("falha ao inspecionar container %s: %w. Stderr: %s", podName, err, stderr.String())
	}

	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "", fmt.Errorf("não foi possível obter IP do container %s", podName)
	}

	return ip, nil
}

func (r DockerRuntime) IsVolumeExist(volumeName string) bool {
	inspectCmd := r.buildDockerCmd(true, "volume", "inspect", volumeName)
	var stdout, stderr bytes.Buffer
	inspectCmd.Stdout = &stdout
	inspectCmd.Stderr = &stderr

	if err := inspectCmd.Run(); err == nil {
		return true
	}
	return false
}

func (r DockerRuntime) CreateVolume(volumeName string) error {
	if r.IsVolumeExist(volumeName) {
		return nil
	}

	// Cria volume
	createCmd := r.buildDockerCmd(false, "volume", "create", volumeName)
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("erro ao criar volume %s: %w", volumeName, err)
	}

	return nil
}

// IsNetworkExist verifica se a rede Docker já existe
func (r DockerRuntime) IsNetworkExist(networkName string) bool {
	cmd := r.buildDockerCmd(true, "network", "inspect", networkName)
	if err := cmd.Run(); err == nil {
		return true
	}
	return false
}

func (r DockerRuntime) CreateNetwork(networkName, subnet, ipRange, gateway, label string) error {
	if r.IsNetworkExist(networkName) {
		return nil
	}

	args := []string{"network", "create"}

	if subnet != "" {
		args = append(args, "--subnet="+subnet)
	}
	if ipRange != "" {
		args = append(args, "--ip-range="+ipRange)
	}
	if gateway != "" {
		args = append(args, "--gateway="+gateway)
	}
	if label != "" {
		args = append(args, "--label="+label)
	}

	args = append(args, networkName)

	createCmd := r.buildDockerCmd(false, args...)
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("erro ao criar rede %s: %w", networkName, err)
	}

	return nil
}

// Só existe para Kubernetes, então retorna vazio
func (r DockerRuntime) GetStorageClassList() ([]TStorageClass, error) {
	return []TStorageClass{}, nil
}

// -------------------- Auxiliares --------------------

func getDockerBinPath() (string, error) {
	path, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("não encontrei o binário do docker no PATH")
	}
	return path, nil
}

func validateTLSPaths(cfg TContainerRuntimeConfig) error {
	paths := map[string]string{
		"TLS CA Cert": cfg.TLSCaCertPath,
		"TLS Cert":    cfg.TLSCertPath,
		"TLS Key":     cfg.TLSKeyPath,
	}

	for name, path := range paths {
		if path != "" {
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("%s não encontrado em '%s': %w", name, path, err)
			}
		}
	}
	return nil
}
