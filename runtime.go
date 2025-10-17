package container

import (
	"errors"
	"time"
)

var ErrContainerNotFound = errors.New("container não encontrado")

type TContainerRuntimeConfig struct {
	Image          string
	ContainerName  string
	Namespace      string
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
	Debug          bool
}

type TContainerRuntime interface {
	Up(podOrContainerName, namespace, composeFile string, waitContainerRunning bool) error
	Down(podOrContainerName, namespace string) error
	IsContainerRunning(podOrContainerName, namespace string) (bool, error)
	WaitContainerRunning(podOrContainerName, namespace string, timeout time.Duration) error
	StopContainer(podOrContainerName, namespace string) error
	ShowLogs(podOrContainerName, namespace string) error
	Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumeList, otherOptionsList []string, namespace, podOrContainerName string) error
	ExecInContainer(podOrContainerName, namespace string, cmd []string) ([]byte, error)
	GetContainerIP(podOrContainerName, namespace string) (string, error)
	CreateNetwork(networkName, subnet, ipRange, gateway, label string) error
	CreateVolume(volumeName string) error
	IsVolumeExist(volumeName string) bool
	IsNetworkExist(networkName string) bool
	CopyToContainer(srcPath, podOrContainerName, namespace, destPath string) error
	CopyToHost(src, podOrContainerName, namespace, dst string) error
	WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podOrContainerName, namespace string) (bool, error)
}

func NewDockerRuntime(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	return NewDockerRuntimeFactory(config)
}

func NewKubernetesRuntime(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	return NewKubernetesRuntimeFactory(config)
}
