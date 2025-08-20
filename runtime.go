package container

import (
	"fmt"
	"time"
)

type TDockerConfig struct {
	RemoteHost    string
	TLSCaCertPath string
	TLSCertPath   string
	TLSKeyPath    string
	dockerBinPath string
}

type TContainerRuntime interface {
	Up(containerName, composeFile string, WaitContainerRunning bool) error
	Down(containerName string) error
	CopyToContainer(srcPath, containerName, destPath string) error
	IsContainerRunning(containerName string) (bool, error)
	WaitContainerRunning(containerName string, timeout time.Duration) error
	StopContainer(containerName string) error
	ShowLogs(containerName string) error
	Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error
	ExecInContainer(containerName string, cmd []string) ([]byte, error)
	GetContainerIP(containerName string) (string, error)
	CreateNetwork(networkName string) error
	CreateVolume(volumeName string) error
}

// NewDockerRuntime cria uma instância de DockerRuntime local
func NewDockerRuntime() (TContainerRuntime, error) {
	dockerConfig := TDockerConfig{}
	return NewDockerRuntimeCustom(dockerConfig)
}

// NewDockerRuntimeCustom cria uma instância de DockerRuntime com conexão TLS e valida se o Docker está presente.
func NewDockerRuntimeCustom(dockerConfig TDockerConfig) (TContainerRuntime, error) {
	dockerBinPath, err := getDockerBinPath()
	if err != nil {
		return nil, fmt.Errorf("Docker não encontrado: %w", err)
	}
	dockerConfig.dockerBinPath = dockerBinPath

	// Valida os caminhos TLS
	if err := validateTLSPaths(dockerConfig); err != nil {
		return nil, err
	}

	return DockerRuntime{config: dockerConfig}, nil
}
