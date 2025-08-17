package container

import "fmt"

type TContainerRuntime interface {
	Up(composeFile string) error
	Down(containerName string) error
	CopyToContainer(srcPath, containerName, destPath string) error
	IsContainerRunning(containerName string) (bool, error)
	StopContainer(containerName string) error
	ShowLogs(containerName string) error
	Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error
	ExecInContainer(containerName string, cmd []string) ([]byte, error)
}

// NewDockerRuntime cria uma instância de DockerRuntime e valida se o Docker está presente.
func NewDockerRuntime() (TContainerRuntime, error) {
	_, err := getDockerBinPath()
	if err != nil {
		return nil, fmt.Errorf("Docker não encontrado: %w", err)
	}

	return DockerRuntime{}, nil
}
