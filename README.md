# 🐳 Container Runtime Library

This project provides a Go interface (`TContainerRuntime`) and a default Docker implementation (`DockerRuntime`) to abstract container runtime operations.  
It enables developers to interact with containers (start, stop, copy files, run commands, etc.) in a consistent way, regardless of the underlying runtime.

## ⚡ Features

- ⬆️ Start and stop containers
- 🖥️ Execute commands inside containers
- 📂 Copy files into containers
- 🏃 Run ad-hoc commands in temporary containers
- 📜 Show logs of running containers
- 🔍 Verify container status

## API Reference

The main interface is:

```go
type TContainerRuntime interface {
    Up(containerName, composeFile string, WaitContainerRunning bool) error
    Down(containerName string) error
    CopyToContainer(srcPath, containerName, destPath string) error
    IsContainerRunning(containerName string) (bool, error)
    StopContainer(containerName string) error
    ShowLogs(containerName string) error
    Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumes []TVolume, otherOptionsList []string, namespace, podOrContainerName, storageClass string) error
    ExecInContainer(containerName string, cmd []string) ([]byte, error)
}

type TVolume struct {
    HostPath     string
    MountPath    string
    ReadOnly     bool
    Size         string
    StorageClass string
}
```

### Function Descriptions

- **Up(containerName, composeFile string, WaitContainerRunning bool) error**  
  Starts the containers defined in the provided Docker Compose file.

- **Down(containerName string) error**  
  Stops and removes the containers.

- **CopyToContainer(srcPath, containerName, destPath string) error**  
  Copies a file or directory from the host machine to the specified container.

- **IsContainerRunning(containerName string) (bool, error)**  
  Checks whether a given container is currently running.

- **WaitContainerRunning(containerName string, timeout time.Duration) error**  
  Waits until the specified container is running, or returns an error if the timeout is reached.

- **StopContainer(containerName string) error**  
  Stops the specified running container.

- **ShowLogs(containerName string) error**  
  Streams the logs from the specified container.

- **Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumes []TVolume, otherOptionsList []string, namespace, podOrContainerName, storageClass string) error**  
  Runs an ad-hoc command in a new container, allowing a custom entrypoint plus configurable working directory, per-volume settings (including size, read-only flag, and storage class overrides), UID/GID, namespace overrides, and additional options.

- **ExecInContainer(containerName string, cmd []string) ([]byte, error)**  
  Executes a command inside an already running container and returns its output.

## License

MIT License
