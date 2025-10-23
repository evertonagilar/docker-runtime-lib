package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type KubernetesRuntime struct {
	config TContainerRuntimeConfig
}

// -------------------- Factory --------------------

func NewKubernetesRuntimeFactory(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	kubectlBinPath, err := getKubectlBinPath()
	if err != nil {
		return nil, err
	}
	config.CommandBinPath = kubectlBinPath

	return KubernetesRuntime{config: config}, nil
}

func getKubectlBinPath() (string, error) {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return "", fmt.Errorf("não encontrei o binário do kubectl no PATH")
	}
	return path, nil
}

// -------------------- Comandos base --------------------

func (r KubernetesRuntime) buildKubectlArgs(args ...string) []string {
	finalArgs := []string{}
	// Suporte futuro a context, namespace, etc.
	finalArgs = append(finalArgs, args...)
	return finalArgs
}

func (r KubernetesRuntime) buildKubectlCmd(captureOutput bool, args ...string) *exec.Cmd {
	return r.buildKubectlCmdWithContext(context.Background(), captureOutput, args...)
}

func (r KubernetesRuntime) buildKubectlCmdWithContext(ctx context.Context, captureOutput bool, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.config.CommandBinPath, r.buildKubectlArgs(args...)...)
	if !captureOutput {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}

func addNamespaceArg(namespace string, args []string) []string {
	if namespace == "" || len(args) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0])
	out = append(out, "-n", namespace)
	out = append(out, args[1:]...)
	return out
}

// -------------------- Métodos principais --------------------

// Up cria o pod/deployment a partir de um manifesto YAML
func (r KubernetesRuntime) Up(podName, namespace, manifestFile string, waitContainerRunning bool) error {
	args := addNamespaceArg(namespace, []string{"apply", "-f", manifestFile})
	cmd := r.buildKubectlCmd(false, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao aplicar manifesto: %w", err)
	}

	if waitContainerRunning {
		if err := r.WaitContainerRunning(podName, namespace, 120*time.Second); err != nil {
			return fmt.Errorf("o pod %s não ficou pronto: %w", podName, err)
		}
	}

	return nil
}

func (r KubernetesRuntime) Down(podName, namespace string, force bool) error {
	deletePodArgs := []string{"delete", "pod", podName, "--ignore-not-found"}
	if force {
		deletePodArgs = append(deletePodArgs, "--grace-period", "3")
	}
	deletePodArgs = addNamespaceArg(namespace, deletePodArgs)
	cmd := r.buildKubectlCmd(false, deletePodArgs...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao deletar pod %s: %w", podName, err)
	}
	deleteSvcArgs := addNamespaceArg(namespace, []string{"delete", "svc", podName, "--ignore-not-found"})
	cmd = r.buildKubectlCmd(false, deleteSvcArgs...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao deletar svc %s: %w", podName, err)
	}
	return nil
}

func (r KubernetesRuntime) GetContainerStatus(podName, namespace string) (ContainerStatus, error) {
	var stdout, stderr bytes.Buffer

	for attempt := 1; attempt <= 3; attempt++ {
		args := addNamespaceArg(namespace, []string{"get", "pod", podName, "-o", "jsonpath={.status.phase}"})
		cmd := r.buildKubectlCmd(true, args...)
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err == nil {
			switch strings.TrimSpace(stdout.String()) {
			case "Running":
				return ContainerStatusRunning, nil
			case "Succeeded":
				return ContainerStatusSucceeded, nil
			case "Pending":
				return ContainerStatusPending, nil
			case "Failed":
				return ContainerStatusFailed, nil
			case "Unknown":
				return ContainerStatusUnknown, nil
			default:
				return ContainerStatusUnknown, nil
			}
		}

		stderrStr := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrStr, "NotFound") {
			return ContainerStatusNotFound, nil
		}

		time.Sleep(1 * time.Second)
	}

	return ContainerStatusUnknown, fmt.Errorf("não foi possível verificar o estado do pod '%s' após 3 tentativas", podName)
}

func (r KubernetesRuntime) IsContainerRunning(podName, namespace string) (bool, error) {
	status, err := r.GetContainerStatus(podName, namespace)
	if err != nil {
		return false, err
	}
	return status == ContainerStatusRunning || status == ContainerStatusSucceeded, nil
}

func (r KubernetesRuntime) WaitContainerRunning(podName, namespace string, timeout time.Duration) error {
	const interval = 3 * time.Second
	deadline := time.Now().Add(timeout)
	var lastPhase string
	var lastDetail string

	time.Sleep(interval)

	for time.Now().Before(deadline) {
		podStatus, err := r.getPodDetailedStatus(podName, namespace)
		if errors.Is(err, ErrContainerNotFound) {
			time.Sleep(interval)
			continue
		}
		if err != nil {
			lastDetail = err.Error()
			time.Sleep(interval)
			continue
		}

		lastPhase = podStatus.Status.Phase

		switch podStatus.Status.Phase {
		case "Failed":
			return fmt.Errorf("pod '%s' falhou: %s", podName, summarizePodConditions(podStatus.Status.Conditions))
		case "Succeeded":
			return nil
		}

		ready, detail := isPodReady(podStatus)
		if ready {
			return nil
		}
		if detail != "" {
			lastDetail = detail
		}

		time.Sleep(interval)
	}

	if lastDetail != "" && lastPhase != "" {
		return fmt.Errorf("timeout aguardando pod '%s' ficar pronto. Última fase: %s. Detalhes: %s", podName, lastPhase, lastDetail)
	}
	if lastPhase != "" {
		return fmt.Errorf("timeout aguardando pod '%s' ficar pronto. Última fase: %s", podName, lastPhase)
	}
	if lastDetail != "" {
		return fmt.Errorf("timeout aguardando pod '%s' ficar pronto. Detalhes: %s", podName, lastDetail)
	}
	return fmt.Errorf("timeout aguardando pod '%s' ficar pronto", podName)
}

func (r KubernetesRuntime) StopContainer(podName, namespace string) error {
	args := addNamespaceArg(namespace, []string{"delete", "pod", podName})
	cmd := r.buildKubectlCmd(false, args...)
	return cmd.Run()
}

func (r KubernetesRuntime) ShowLogs(podName, containerName, namespace string) error {
	if podName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	args := []string{"logs", podName}
	if containerName != "" {
		args = append(args, "-c", containerName)
	}
	args = append(args, "-f")
	args = addNamespaceArg(namespace, args)
	cmd := r.buildKubectlCmd(false, args...)
	return cmd.Run()
}

func (r KubernetesRuntime) ExecInContainer(podName, containerName, namespace string, cmdArgs []string) ([]byte, error) {
	if podName == "" {
		return nil, fmt.Errorf("nome do pod deve ser informado")
	}

	args := []string{"exec", podName}
	if containerName != "" {
		args = append(args, "-c", containerName)
	}
	args = append(args, "--")
	args = append(args, cmdArgs...)
	args = addNamespaceArg(namespace, args)
	cmd := r.buildKubectlCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("erro ao executar comando no pod: %w. Stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// -------------------- Utilidades --------------------

type kubectlPodStatus struct {
	Status struct {
		Phase              string          `json:"phase"`
		Conditions         []podCondition  `json:"conditions"`
		ContainerStatuses  []podContainer  `json:"containerStatuses"`
		InitContainerState []podInitStatus `json:"initContainerStatuses"`
	} `json:"status"`
}

type podCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type podContainer struct {
	Name  string            `json:"name"`
	Ready bool              `json:"ready"`
	State podContainerState `json:"state"`
}

type podInitStatus struct {
	Name  string            `json:"name"`
	State podContainerState `json:"state"`
}

type podContainerState struct {
	Waiting    *podStateDetail `json:"waiting"`
	Terminated *podTerminated  `json:"terminated"`
}

type podStateDetail struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type podTerminated struct {
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
}

func (r KubernetesRuntime) getPodDetailedStatus(podName, namespace string) (*kubectlPodStatus, error) {
	args := addNamespaceArg(namespace, []string{"get", "pod", podName, "-o", "json"})
	cmd := r.buildKubectlCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrStr, "NotFound") {
			return nil, ErrContainerNotFound
		}
		return nil, fmt.Errorf("erro ao obter status do pod %s: %w. Stderr: %s", podName, err, stderrStr)
	}

	var status kubectlPodStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		return nil, fmt.Errorf("erro ao decodificar status do pod %s: %w", podName, err)
	}

	return &status, nil
}

func isPodReady(podStatus *kubectlPodStatus) (bool, string) {
	if podStatus == nil {
		return false, "status do pod indisponível"
	}

	if status, msg := conditionStatus(podStatus.Status.Conditions, "Initialized"); status == "False" {
		return false, msg
	}

	if status, msg := conditionStatus(podStatus.Status.Conditions, "Ready"); status != "True" {
		if msg != "" {
			return false, msg
		}
		return false, "condição Ready ainda não satisfeita"
	}

	if status, msg := conditionStatus(podStatus.Status.Conditions, "ContainersReady"); status == "False" {
		return false, msg
	}

	if len(podStatus.Status.ContainerStatuses) == 0 {
		return false, "status dos containers ainda não disponível"
	}

	for _, cs := range podStatus.Status.ContainerStatuses {
		if cs.Ready {
			continue
		}

		if cs.State.Waiting != nil {
			return false, fmt.Sprintf("container %s aguardando: %s - %s", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
		}

		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "Completed" {
			return false, fmt.Sprintf("container %s finalizado (%d): %s - %s", cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message)
		}

		return false, fmt.Sprintf("container %s não está pronto", cs.Name)
	}

	for _, init := range podStatus.Status.InitContainerState {
		if init.State.Terminated != nil && init.State.Terminated.Reason == "Completed" {
			continue
		}
		if init.State.Waiting != nil {
			return false, fmt.Sprintf("init container %s aguardando: %s - %s", init.Name, init.State.Waiting.Reason, init.State.Waiting.Message)
		}
		if init.State.Terminated != nil {
			return false, fmt.Sprintf("init container %s finalizado (%d): %s - %s", init.Name, init.State.Terminated.ExitCode, init.State.Terminated.Reason, init.State.Terminated.Message)
		}
	}

	return true, ""
}

func conditionStatus(conditions []podCondition, conditionType string) (string, string) {
	for _, cond := range conditions {
		if cond.Type != conditionType {
			continue
		}
		message := cond.Message
		if message == "" {
			message = cond.Reason
		}
		return cond.Status, message
	}
	return "", ""
}

func summarizePodConditions(conditions []podCondition) string {
	if len(conditions) == 0 {
		return "condições indisponíveis"
	}

	var summaries []string
	for _, cond := range conditions {
		status := cond.Status
		if status == "" {
			status = "desconhecido"
		}
		message := cond.Message
		if message == "" {
			message = cond.Reason
		}
		if message != "" {
			summaries = append(summaries, fmt.Sprintf("%s=%s (%s)", cond.Type, status, message))
		} else {
			summaries = append(summaries, fmt.Sprintf("%s=%s", cond.Type, status))
		}
	}
	return strings.Join(summaries, "; ")
}

func (r KubernetesRuntime) GetContainerIP(podName, namespace string) (string, error) {
	args := addNamespaceArg(namespace, []string{"get", "pod", podName, "-o", "jsonpath={.status.podIP}"})
	cmd := r.buildKubectlCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("falha ao obter IP do pod %s: %w. Stderr: %s", podName, err, stderr.String())
	}

	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "", fmt.Errorf("não foi possível obter IP do pod %s", podName)
	}

	return ip, nil
}

func (r KubernetesRuntime) CopyToContainer(src, podName, containerName, namespace, dst string) error {
	if podName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	src = filepath.ToSlash(src)
	destDir := path.Dir(dst)
	tmpName := filepath.Base(dst) + ".tmp"
	tmpDestPath := path.Join(destDir, tmpName)

	// Copia o arquivo para o container com nome temporário
	copyArgs := []string{"cp", src, fmt.Sprintf("%s:%s", podName, tmpDestPath)}
	if containerName != "" {
		copyArgs = append(copyArgs, "-c", containerName)
	}
	copyArgs = addNamespaceArg(namespace, copyArgs)
	copyCmd := r.buildKubectlCmd(false, copyArgs...)
	copyCmd.Stdout = os.Stdout
	copyCmd.Stderr = os.Stderr
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar arquivo temporário para o pod: %w", err)
	}

	// Move o arquivo dentro do container (rename atômico)
	mvArgs := []string{"exec", podName}
	if containerName != "" {
		mvArgs = append(mvArgs, "-c", containerName)
	}
	mvArgs = append(mvArgs, "--", "mv", "-f", tmpDestPath, dst)
	mvArgs = addNamespaceArg(namespace, mvArgs)
	mvCmd := r.buildKubectlCmd(false, mvArgs...)
	mvCmd.Stdout = os.Stdout
	mvCmd.Stderr = os.Stderr
	if err := mvCmd.Run(); err != nil {
		return fmt.Errorf("erro ao mover arquivo dentro do pod: %w", err)
	}

	return nil
}

func (r KubernetesRuntime) CopyToHost(src, podName, containerName, namespace, dst string) error {
	if podName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	args := []string{"cp", fmt.Sprintf("%s:%s", podName, src), dst}
	if containerName != "" {
		args = append(args, "-c", containerName)
	}
	args = addNamespaceArg(namespace, args)
	cmd := r.buildKubectlCmd(false, args...)

	// Cria buffers separados para stdout e stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// Redireciona stderr, mas filtra apenas o warning do tar
	cmd.Stderr = io.Discard // descarta tudo de stderr, incluindo o tar warning

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar arquivo do pod: %w", err)
	}

	return nil
}

func (r KubernetesRuntime) WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podName, containerName, namespace string) (bool, error) {
	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	if podName == "" {
		return false, fmt.Errorf("nome do pod deve ser informado")
	}

	for {
		select {
		case <-timeoutChan:
			target := podName
			if containerName != "" {
				target = fmt.Sprintf("%s/%s", podName, containerName)
			}
			return false, fmt.Errorf("timeout esperando arquivo %s aparecer no pod %s", fileName, target)
		case <-ticker.C:
			running, _ := r.IsContainerRunning(podName, namespace)
			if running {
				_, err := r.ExecInContainer(podName, containerName, namespace, []string{"test", "-f", fileName})
				if err == nil {
					return true, nil
				}
			} else {
				return false, ErrContainerNotFound
			}
		}
	}
}

func (r KubernetesRuntime) GetStorageClassList() ([]TStorageClass, error) {
	cmd := r.buildKubectlCmd(true, "get", "storageclass", "-o", "json")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errOutput := strings.TrimSpace(stderr.String())
		return nil, fmt.Errorf("erro ao listar storageclasses: %w. Stderr: %s", err, errOutput)
	}

	type storageClassMetadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	}

	type storageClassSpec struct {
		Provisioner string `json:"provisioner"`
	}

	type storageClassItem struct {
		Metadata storageClassMetadata `json:"metadata"`
		Spec     storageClassSpec     `json:"spec"`
	}

	var scList struct {
		Items []storageClassItem `json:"items"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &scList); err != nil {
		return nil, fmt.Errorf("erro ao interpretar storageclasses: %w", err)
	}

	result := make([]TStorageClass, 0, len(scList.Items))
	for _, item := range scList.Items {
		isDefault := false
		if annotations := item.Metadata.Annotations; annotations != nil {
			for _, key := range []string{
				"storageclass.kubernetes.io/is-default-class",
				"storageclass.beta.kubernetes.io/is-default-class",
			} {
				if value, ok := annotations[key]; ok && strings.EqualFold(value, "true") {
					isDefault = true
					break
				}
			}
		}

		provisioner := strings.TrimSpace(item.Spec.Provisioner)
		isDinamic := provisioner != "" && !strings.EqualFold(provisioner, "kubernetes.io/no-provisioner")

		result = append(result, TStorageClass{
			Name:      item.Metadata.Name,
			IsDefault: isDefault,
			IsDinamic: isDinamic,
		})
	}

	return result, nil
}

// -------------------- Métodos não usados no Kubernetes --------------------

func (r KubernetesRuntime) CreateNetwork(networkName, subnet, ipRange, gateway, label string) error {
	// Kubernetes não usa redes customizadas como Docker; pode ser ignorado
	return nil
}

func (r KubernetesRuntime) IsNetworkExist(networkName string) bool {
	// Não aplicável
	return true
}

func (r KubernetesRuntime) CreateVolume(volumeName string) error {
	// Kubernetes usa PersistentVolumeClaim, mas podemos ignorar por ora
	return nil
}

func (r KubernetesRuntime) IsVolumeExist(volumeName string) bool {
	// Não aplicável
	return true
}
