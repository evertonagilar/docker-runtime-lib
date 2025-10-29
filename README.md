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
    Up(podOrContainerName, namespace, manifestFile string, waitContainerRunning bool) error
    Down(podOrContainerName, namespace string, force bool) error
    CopyToContainer(src, podOrContainerName, mainContainerName, namespace, dst string) error
    IsContainerRunning(podOrContainerName, namespace string) (bool, error)
    StopContainer(podOrContainerName, namespace string) error
    ShowLogs(podOrContainerName, mainContainerName, namespace string) error
    Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumes []TVolume, otherOptionsList []string, namespace, podOrContainerName, mainContainerName, storageClass string) error
    ExecInContainer(podOrContainerName, mainContainerName, namespace string, cmd []string) ([]byte, error)
}

type TVolume struct {
    HostPath     string
    MountPath    string
    ReadOnly     bool
    Size         string
    StorageClass string
    InMemory     bool
}
```

### Function Descriptions

- **Up(podOrContainerName, namespace, manifestFile string, waitContainerRunning bool) error**  
  Applies the manifest (Docker Compose or Kubernetes) and optionally waits until the workload is running.

- **Down(podOrContainerName, namespace string, force bool) error**  
  Stops and removes the container or pod, forcing deletion when `force` is `true`.

- **CopyToContainer(src, podOrContainerName, mainContainerName, namespace, dst string) error**  
  Copies a file or directory from the host machine to the specified container.

- **IsContainerRunning(podOrContainerName, namespace string) (bool, error)**  
  Checks whether a given container or pod is currently running.

- **WaitContainerRunning(podOrContainerName, namespace string, timeout time.Duration) error**  
  Waits until the specified workload is running, or returns an error if the timeout is reached.

- **StopContainer(podOrContainerName, namespace string) error**  
  Stops the specified running container.

- **ShowLogs(podOrContainerName, mainContainerName, namespace string) error**  
  Streams the logs from the specified container; when using Docker, `mainContainerName` should stay empty.

- **Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumes []TVolume, otherOptionsList []string, namespace, podOrContainerName, mainContainerName, storageClass string) error**  
  Runs an ad-hoc command in a new container, allowing a custom entrypoint plus configurable working directory, per-volume settings (including size, read-only flag, and storage class overrides), UID/GID, namespace overrides, and additional options.

- **ExecInContainer(podOrContainerName, mainContainerName, namespace string, cmd []string) ([]byte, error)**  
  Executes a command inside an already running container and returns its output; for Docker the `mainContainerName` parameter should remain empty.

## License

MIT License
