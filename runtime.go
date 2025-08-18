package container

import "time"

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
}
