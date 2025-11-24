package container

type TK8sContainerSpec struct {
	Name         string
	Image        string
	Command      []string
	Args         []string
	Env          map[string]string
	Ports        []int
	VolumeMounts map[string]string // ex: "/mnt/data": "data-volume"
}

type TK8sVolumeSpec struct {
	Name string
	Path string
	Type string // "emptyDir" | "hostPath" | "persistentVolumeClaim"
}

type TK8sRuntime struct {
	Namespace string
	PodName   string
	Container TK8sContainerSpec
	Volumes   []TK8sVolumeSpec
}
