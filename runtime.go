package container

import (
	"errors"
	"time"
)

var ErrContainerNotFound = errors.New("container não encontrado")

type ContainerStatus string

const (
	ContainerStatusUnknown   ContainerStatus = "unknown"
	ContainerStatusNotFound  ContainerStatus = "not_found"
	ContainerStatusRunning   ContainerStatus = "running"
	ContainerStatusStopped   ContainerStatus = "stopped"
	ContainerStatusPending   ContainerStatus = "pending"
	ContainerStatusFailed    ContainerStatus = "failed"
	ContainerStatusSucceeded ContainerStatus = "succeeded"
	ContainerStatusPaused    ContainerStatus = "paused"
)

type TContainerRuntimeConfig struct {
	Image               string
	PodName             string
	Namespace           string
	Env                 []string
	Volumes             []TVolume
	Ports               map[string]string // ex: {"8080/tcp": "8080", "8787/tcp": "8787"}
	NetworkName         string
	RemoteHost          string
	TLSCaCertPath       string
	TLSCertPath         string
	TLSKeyPath          string
	MemoryLimitMB       int
	CpuLimit            float64
	CommandBinPath      string
	Workspace           string
	Debug               bool
	ImagePullSecretName string
	Kubeconfig          string
}

type TVolume struct {
	HostPath     string
	MountPath    string
	ReadOnly     bool
	Size         string
	StorageClass string
	InMemory     bool // para Kubernetes usa EmptyDir e medium=Memory
}

type TStorageClass struct {
	Name      string
	IsDefault bool
	IsDinamic bool
}

type TContainerRuntime interface {
	Up(podOrContainerName, namespace, manifestFile string, waitContainerRunning bool) error
	Down(podOrContainerName, namespace string, force bool) error
	Apply(namespace, manifestFile string, force bool) error
	Delete(namespace, manifestFile string, force bool) error
	GetContainerStatus(podOrContainerName, namespace string) (ContainerStatus, error)
	IsContainerRunning(podOrContainerName, namespace string) (bool, error)
	WaitContainerRunning(podOrContainerName, namespace string, timeout time.Duration) error
	StopContainer(podOrContainerName, namespace string) error
	ShowLogs(podOrContainerName, mainContainerName, namespace string) error
	Run(cmdStr, entrypoint, chDir, image, uid, gid string,
		volumes []TVolume, otherOptionsList []string, namespace,
		podOrContainerName, mainContainerName, storageClass string) error
	ExecInContainer(podOrContainerName, mainContainerName, namespace string, cmd []string) ([]byte, error)
	GetContainerIP(podOrContainerName, namespace string) (string, error)
	CreateNetwork(networkName, subnet, ipRange, gateway, label string) error
	CreateVolume(volumeName string) error
	IsVolumeExist(volumeName string) bool
	IsNetworkExist(networkName string) bool
	CopyToContainer(src, podOrContainerName, mainContainerName, namespace, dst string) error
	CopyToContainerIncremental(srcDir, podOrContainerName, mainContainerName, namespace, dstPath string, debug bool) error
	CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error
	WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podOrContainerName, mainContainerName, namespace string) (bool, error)
	GetStorageClassList() ([]TStorageClass, error)
	GetClusterApiServerHost() string
	IsNamespaceExist(namespace string) (bool, error)
	CreateNamespace(namespace string) error
}

func NewDockerRuntime(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	return NewDockerRuntimeFactory(config)
}

func NewKubernetesRuntime(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	return NewKubernetesRuntimeFactory(config)
}
