package container

import (
	"errors"
	"time"
)

var ErrContainerNotFound = errors.New("container não encontrado")

type TContainerRuntimeConfig struct {
	Image          string
	ContainerName  string
	Env            []string
	Volumes        []string
	Ports          map[string]string // ex: {"8080/tcp": "8080", "8787/tcp": "8787"}
	NetworkName    string
	RemoteHost     string
	TLSCaCertPath  string
	TLSCertPath    string
	TLSKeyPath     string
	MemoryLimitMB  int
	CpuLimit       float64
	CommandBinPath string
	Workspace      string
}

type TContainerRuntime interface {
	Up(containerName, composeFile string, WaitContainerRunning bool) error
	Down(containerName string) error
	IsContainerRunning(containerName string) (bool, error)
	WaitContainerRunning(containerName string, timeout time.Duration) error
	StopContainer(containerName string) error
	ShowLogs(containerName string) error
	Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error
	ExecInContainer(containerName string, cmd []string) ([]byte, error)
	GetContainerIP(containerName string) (string, error)
	CreateNetwork(networkName, subnet, ipRange, gateway, label string) error
	CreateVolume(volumeName string) error
	IsVolumeExist(volumeName string) bool
	IsNetworkExist(networkName string) bool
	CopyToContainer(srcPath, containerName, destPath string) error
	CopyToHost(src, containerName, dst string) error
	WaitForFile(fileName string, timeout time.Duration, interval time.Duration, containerName string) (bool, error)
}

func NewDockerRuntime(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	return NewDockerRuntimeFactory(config)
}

func NewKubernetesRuntime(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	return NewKubernetesRuntimeFactory(config)
}
