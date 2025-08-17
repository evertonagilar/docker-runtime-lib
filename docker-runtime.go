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
)

type DockerRuntime struct{}

var dockerBin string

// getDockerBinPath retorna o caminho do binário docker, cacheando o resultado.
func getDockerBinPath() (string, error) {
	if dockerBin != "" {
		return dockerBin, nil
	}

	path, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("não encontrei o binário do docker no PATH")
	}

	dockerBin = path
	return dockerBin, nil
}

func (r DockerRuntime) Up(composeFile string) error {
	docker, err := getDockerBinPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(docker, "compose", "-f", composeFile, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r DockerRuntime) Down(containerName string) error {
	docker, err := getDockerBinPath()
	if err != nil {
		return err
	}

	// Para o container
	stopCmd := exec.Command(docker, "stop", containerName)
	stopCmd.Stdout = os.Stdout
	stopCmd.Stderr = os.Stderr
	if err := stopCmd.Run(); err != nil {
		return fmt.Errorf("falha ao parar container: %w", err)
	}

	// Remove o container
	rmCmd := exec.Command(docker, "rm", containerName)
	rmCmd.Stdout = os.Stdout
	rmCmd.Stderr = os.Stderr
	if err := rmCmd.Run(); err != nil {
		return fmt.Errorf("falha ao remover container: %w", err)
	}

	return nil
}

func (r DockerRuntime) CopyToContainer(srcFileName, containerName, dstFileName string) error {
	docker, err := getDockerBinPath()
	if err != nil {
		return err
	}

	// Usa o mesmo diretório do destino para evitar problemas de permissão
	destDir := path.Dir(dstFileName)
	tempName := filepath.Base(dstFileName) + ".tmp"
	tmpDestPath := path.Join(destDir, tempName)
	srcFileName = filepath.ToSlash(srcFileName)

	// Etapa 1: copia para o arquivo temporário no destino
	copyCmd := exec.Command(docker, "cp", "-L", "-q", srcFileName, fmt.Sprintf("%s:%s", containerName, tmpDestPath))
	copyCmd.Stdout = os.Stdout
	copyCmd.Stderr = os.Stderr
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar para o container: %w", err)
	}

	// Etapa 2: move de forma atômica para o nome final
	mvCmd := exec.Command(docker, "exec", containerName, "mv", tmpDestPath, dstFileName)
	mvCmd.Stdout = os.Stdout
	mvCmd.Stderr = os.Stderr
	if err := mvCmd.Run(); err != nil {
		return fmt.Errorf("erro ao mover arquivo dentro do container: %w", err)
	}

	return nil
}

func (r DockerRuntime) IsContainerRunning(containerName string) (bool, error) {
	docker, err := getDockerBinPath()
	if err != nil {
		return false, err
	}

	cmd := exec.Command(docker, "inspect", "-f", "{{.State.Running}}", containerName)
	out, err := cmd.Output()
	if err != nil {
		return false, nil // container não existe ou não está rodando
	}
	return string(out) == "true\n", nil
}

func (r DockerRuntime) StopContainer(containerName string) error {
	docker, err := getDockerBinPath()
	if err != nil {
		return err
	}

	cmd := exec.Command(docker, "stop", containerName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r DockerRuntime) ShowLogs(containerName string) error {
	docker, err := getDockerBinPath()
	if err != nil {
		return err
	}

	cmd := exec.Command(docker, "logs", "-f", containerName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r DockerRuntime) Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error {
	docker, err := getDockerBinPath()
	if err != nil {
		return err
	}

	args := []string{"run", "--rm"}
	if runtime.GOOS != "windows" {
		// Só adiciona UID/GID se não forem vazios ou "0"
		if uid != "" && uid != "0" {
			args = append(args, "-e", "HOST_UID="+uid)
		}
		if gid != "" && gid != "0" {
			args = append(args, "-e", "HOST_GID="+gid)
		}
	}

	// Volumes
	for _, v := range volumeList {
		args = append(args, "-v", v)
	}

	// Diretório de trabalho
	if chDir != "" {
		args = append(args, "-w", chDir)
	}

	// Outras opções
	args = append(args, otherOptionsList...)

	// Imagem
	args = append(args, image)

	// Comando final no container
	args = append(args, "bash", "-c", cmdStr)

	if debug {
		fmt.Printf("🔨 Comando docker: %s %s\n", docker, strings.Join(args, " "))
	}

	cmd := exec.Command(docker, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func (d DockerRuntime) ExecInContainer(containerName string, cmd []string) ([]byte, error) {
	docker, err := getDockerBinPath()
	if err != nil {
		return nil, err
	}

	args := append([]string{"exec", containerName}, cmd...)

	command := exec.Command(docker, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err = command.Run()
	if err != nil {
		return nil, fmt.Errorf("erro ao executar comando no container: %w. Stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}
