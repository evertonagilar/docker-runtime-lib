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
	Apply(namespace, manifestFile string, force bool) error
	Delete(namespace, manifestFile string, force bool) error
	GetContainerStatus(podOrContainerName, namespace string) (ContainerStatus, error)
	IsContainerRunning(podOrContainerName, namespace string) (bool, error)
	WaitContainerRunning(podOrContainerName, namespace string, timeout time.Duration) error
	StopContainer(podOrContainerName, namespace string) error
	ShowLogs(podOrContainerName, mainContainerName, namespace string, tail int) error
	Run(cmdStr, entrypoint, chDir, image, uid, gid string,
		volumes []TVolume, otherOptionsList []string, namespace,
		podOrContainerName, mainContainerName, storageClass string) error
	ExecInContainer(podOrContainerName, mainContainerName, namespace string, cmd []string) ([]byte, error)
	GetContainerIP(podOrContainerName, namespace string) (string, error)
	CreateNetwork(networkName, subnet, ipRange, gateway, label string) error
	CreateVolume(volumeName string) error
	IsVolumeExist(volumeName string) bool
	IsNetworkExist(networkName string) bool
	CopyToContainer(src, podOrContainerName, mainContainerName, namespace, dst string, useAtomicCopy bool) error
	CopyToContainerIncremental(srcDir, podOrContainerName, mainContainerName, namespace, dstPath string, debug bool) error
	CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error
	WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podOrContainerName, mainContainerName, namespace string) (bool, error)
	GetStorageClassList() ([]TStorageClass, error)
	GetClusterApiServerHost() string
	IsNamespaceExist(namespace string) (bool, error)
	CreateNamespace(namespace string) error
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
  Applies the manifest (Docker Compose or Kubernetes) and optionally waits until the workload is running. `[Docker, K8s]`

- **Down(podOrContainerName, namespace string, force bool) error**  
  Stops and removes the container or pod, forcing deletion when `force` is `true`. `[Docker, K8s]`

- **CopyToContainer(src, podOrContainerName, mainContainerName, namespace, dst string, useAtomicCopy bool) error**  
  Copies a file or directory from the host machine to the specified container. Supports atomic copy for files (using a temporary file and atomic rename) and automatic path normalization for Windows. `[Docker, K8s]`

- **IsContainerRunning(podOrContainerName, namespace string) (bool, error)**  
  Checks whether a given container or pod is currently running. `[Docker, K8s]`

- **WaitContainerRunning(podOrContainerName, namespace string, timeout time.Duration) error**  
  Waits until the specified workload is running, or returns an error if the timeout is reached. `[Docker, K8s]`

- **StopContainer(podOrContainerName, namespace string) error**  
  Stops the specified running container. `[Docker, K8s]`

- **Apply(namespace, manifestFile string, force bool) error**
  Applies a manifest (e.g., `kubectl apply` or `docker compose up`) to create or update resources. `[Docker, K8s]`

- **Delete(namespace, manifestFile string, force bool) error**
  Deletes resources defined in a manifest file. `[Docker, K8s]`

- **GetContainerStatus(podOrContainerName, namespace string) (ContainerStatus, error)**
  Returns the current status of the container or pod (e.g., Running, Stopped, Pending). `[Docker, K8s]`

- **GetContainerIP(podOrContainerName, namespace string) (string, error)**
  Retrieves the IP address of the specified container or pod. `[Docker, K8s]`

- **CreateNetwork(networkName, subnet, ipRange, gateway, label string) error**
  Creates a container network with specified configuration. `[Docker only]`

- **CreateVolume(volumeName string) error**
  Creates a persistent volume. `[Docker only]`

- **IsVolumeExist(volumeName string) bool**
  Checks if a volume with the given name exists. `[Docker only]`

- **IsNetworkExist(networkName string) bool**
  Checks if a network with the given name exists. `[Docker only]`

- **WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podOrContainerName, mainContainerName, namespace string) (bool, error)**
  Waits for a specific file to appear inside the container within the given timeout.

- **GetStorageClassList() ([]TStorageClass, error)**
  Lists available storage classes. `[K8s only]`

- **GetClusterApiServerHost() string**
  Returns the API server host address. `[K8s only]`

- **IsNamespaceExist(namespace string) (bool, error)**
  Checks if a namespace exists. `[K8s only]`

- **CreateNamespace(namespace string) error**
  Creates a new namespace. `[K8s only]`

- **CopyToContainerIncremental(srcDir, podOrContainerName, mainContainerName, namespace, dstPath string, debug bool) error**
  Incrementally copies a directory to the container, optimizing by only transferring changed files. `[Docker, K8s]`
  
- **CopyToHost(src, podOrContainerName, mainContainerName, namespace, dst string) error**
  Copies a file or directory from the container to the host machine, including automatic path normalization for Windows.

- **ShowLogs(podOrContainerName, mainContainerName, namespace string) error**  
  Streams the logs from the specified container; when using Docker, `mainContainerName` should stay empty.

- **Run(cmdStr, entrypoint, chDir, image, uid, gid string, volumes []TVolume, otherOptionsList []string, namespace, podOrContainerName, mainContainerName, storageClass string) error**  
  Runs an ad-hoc command in a new container, allowing a custom entrypoint plus configurable working directory, per-volume settings (including size, read-only flag, and storage class overrides), UID/GID, namespace overrides, and additional options.

- **ExecInContainer(podOrContainerName, mainContainerName, namespace string, cmd []string) ([]byte, error)**  
  Executes a command inside an already running container and returns its output; for Docker the `mainContainerName` parameter should remain empty.

## License

MIT License
