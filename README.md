# Container Runtime Library

This project provides a Go interface (`TContainerRuntime`) and a default Docker implementation (`DockerRuntime`) to abstract container runtime operations.  
It enables developers to interact with containers (start, stop, copy files, run commands, etc.) in a consistent way, regardless of the underlying runtime.

## Features

- Start and stop containers
- Execute commands inside containers
- Copy files into containers
- Run ad-hoc commands in temporary containers
- Show logs of running containers
- Verify container status

## Installation

```bash
go get github.com/yourusername/container-runtime
```

## API Reference

The main interface is:

```go
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
```

### Function Descriptions

- **Up(composeFile string) error**  
  Starts the containers defined in the provided Docker Compose file.

- **Down(containerName string) error**  
  Stops and removes the containers.

- **CopyToContainer(srcPath, containerName, destPath string) error**  
  Copies a file or directory from the host machine to the specified container.

- **IsContainerRunning(containerName string) (bool, error)**  
  Checks whether a given container is currently running.

- **StopContainer(containerName string) error**  
  Stops the specified running container.

- **ShowLogs(containerName string) error**  
  Streams the logs from the specified container.

- **Run(cmdStr, chDir, image, uid, gid string, volumeList, otherOptionsList []string, debug bool) error**  
  Runs an ad-hoc command in a new container, with configurable working directory, volumes, UID/GID, and additional options.

- **ExecInContainer(containerName string, cmd []string) ([]byte, error)**  
  Executes a command inside an already running container and returns its output.

## License

MIT License
