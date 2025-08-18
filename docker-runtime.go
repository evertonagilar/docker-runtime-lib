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
	config TDockerConfig
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
	cmd := exec.Command(r.config.dockerBinPath, r.buildDockerArgs(args...)...)
	if !captureOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}

func (r DockerRuntime) Up(containerName, composeFile string, WaitContainerRunning bool) error {
	cmd := r.buildDockerCmd(false, "compose", "-f", composeFile, "up", "-d")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao executar docker-compose up: %w", err)
	}

	if WaitContainerRunning {
		if err := r.WaitContainerRunning(containerName, 60*time.Second); err != nil {
			return fmt.Errorf("container não subiu corretamente: %w", err)
		}
	}

	return nil
}

func (r DockerRuntime) Down(containerName string) error {
	stopCmd := r.buildDockerCmd(false, "stop", containerName)
	if err := stopCmd.Run(); err != nil {
		return fmt.Errorf("falha ao parar container: %w", err)
	}

	rmCmd := r.buildDockerCmd(false, "rm", containerName)
	if err := rmCmd.Run(); err != nil {
		return fmt.Errorf("falha ao remover container: %w", err)
	}

	return nil
}

func (r DockerRuntime) CopyToContainer(srcFileName, containerName, dstFileName string) error {
	destDir := path.Dir(dstFileName)
	tempName := filepath.Base(dstFileName) + ".tmp"
	tmpDestPath := path.Join(destDir, tempName)
	srcFileName = filepath.ToSlash(srcFileName)

	copyCmd := r.buildDockerCmd(false, "cp", "-L", "-q", srcFileName, fmt.Sprintf("%s:%s", containerName, tmpDestPath))
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar para o container: %w", err)
	}

	mvCmd := r.buildDockerCmd(false, "exec", containerName, "mv", tmpDestPath, dstFileName)
	if err := mvCmd.Run(); err != nil {
		return fmt.Errorf("erro ao mover arquivo dentro do container: %w", err)
	}

	return nil
}

func (r DockerRuntime) IsContainerRunning(containerName string) (bool, error) {
	cmd := exec.Command(r.config.dockerBinPath, r.buildDockerArgs("inspect", "-f", "{{.State.Running}}", containerName)...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return false, nil
	}

	return strings.TrimSpace(stdout.String()) == "true", nil
}

func (r DockerRuntime) WaitContainerRunning(containerName string, timeout time.Duration) error {
	timeoutChan := time.After(timeout)
	tick := time.Tick(1 * time.Second)
	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timeout esperando container %s subir", containerName)
		case <-tick:
			running, _ := r.IsContainerRunning(containerName)
			if running {
				return nil
			}
		}
	}
}

func (r DockerRuntime) StopContainer(containerName string) error {
	cmd := r.buildDockerCmd(false, "stop", containerName)
	return cmd.Run()
}

func (r DockerRuntime) ShowLogs(containerName string) error {
	cmd := r.buildDockerCmd(false, "logs", "-f", containerName)
	return cmd.Run()
}

func (r DockerRuntime) Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error {
	args := []string{"run", "--rm"}

	if runtime.GOOS != "windows" {
		if uid != "" && uid != "0" {
			args = append(args, "-e", "HOST_UID="+uid)
		}
		if gid != "" && gid != "0" {
			args = append(args, "-e", "HOST_GID="+gid)
		}
	}

	for _, v := range volumeList {
		args = append(args, "-v", v)
	}
	if chDir != "" {
		args = append(args, "-w", chDir)
	}
	args = append(args, otherOptionsList...)
	args = append(args, image)
	args = append(args, "bash", "-c", cmdStr)

	if debug {
		fmt.Printf("🔨 Comando docker: %s %s\n", r.config.dockerBinPath, strings.Join(args, " "))
	}

	cmd := r.buildDockerCmd(false, args...)
	return cmd.Run()
}

func (r DockerRuntime) ExecInContainer(containerName string, cmdArgs []string) ([]byte, error) {
	args := append([]string{"exec", containerName}, cmdArgs...)
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

// -------------------- Auxiliares --------------------

func getDockerBinPath() (string, error) {
	path, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("não encontrei o binário do docker no PATH")
	}
	return path, nil
}

func validateTLSPaths(cfg TDockerConfig) error {
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
