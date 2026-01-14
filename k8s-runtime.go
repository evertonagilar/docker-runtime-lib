package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type KubernetesRuntime struct {
	config TContainerRuntimeConfig
}

type tailBuffer struct {
	data  []byte
	limit int
}

func newTailBuffer(limit int) *tailBuffer {
	if limit <= 0 {
		limit = 1
	}
	return &tailBuffer{limit: limit}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	if len(p) >= t.limit {
		t.data = append(t.data[:0], p[len(p)-t.limit:]...)
		return len(p), nil
	}

	if len(t.data)+len(p) <= t.limit {
		t.data = append(t.data, p...)
		return len(p), nil
	}

	overflow := len(t.data) + len(p) - t.limit
	t.data = append(t.data[overflow:], p...)
	return len(p), nil
}

func (t *tailBuffer) String() string {
	return string(t.data)
}

// -------------------- Factory --------------------

func NewKubernetesRuntimeFactory(config TContainerRuntimeConfig) (TContainerRuntime, error) {
	kubectlBinPath := strings.TrimSpace(config.CommandBinPath)
	if kubectlBinPath == "" {
		var err error
		kubectlBinPath, err = getKubectlBinPath()
		if err != nil {
			return nil, err
		}
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
	if strings.TrimSpace(r.config.Kubeconfig) != "" {
		finalArgs = append(finalArgs, "--kubeconfig", strings.TrimSpace(r.config.Kubeconfig))
	}
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
	// Place namespace before subcommand: kubectl -n namespace cp ...
	out := make([]string, 0, len(args)+2)
	out = append(out, "-n", namespace)
	out = append(out, args...)
	return out
}

const (
	kubectlOutputTailLimit = 4 * 1024
	kubectlCopyMaxAttempts = 3
	kubectlCopyRetryDelay  = 3 * time.Second
)

var kubectlCpTransientErrorFragments = []string{
	"timeout",
	"timed out",
	"deadline exceeded",
	"i/o timeout",
	"unexpected eof",
	"connection reset",
	"broken pipe",
	"unable to upgrade connection",
}

var kubectlCpIgnoredWarnings = []string{
	"tar: Removing leading '/'",
	"tar: removing leading '/'",
	"tar: .: file changed as we read it",
}

func trimAndJoin(parts ...string) string {
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			trimmed = append(trimmed, part)
		}
	}
	return strings.Join(trimmed, "\n")
}

func filterKubectlCpWarnings(output string) string {
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		ignored := false
		for _, warn := range kubectlCpIgnoredWarnings {
			if strings.Contains(trimmed, warn) {
				ignored = true
				break
			}
		}
		if !ignored {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, "\n")
}

func shouldRetryKubectlCp(err error, stderr string) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(stderr) + " " + err.Error())
	for _, fragment := range kubectlCpTransientErrorFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func (r KubernetesRuntime) runKubectlCommand(cmd *exec.Cmd, errPrefix string) error {
	if r.config.Debug {
		fmt.Printf("🔨 Comando kubectl: %s\n", strings.Join(cmd.Args, " "))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", errPrefix, err)
		}
		return nil
	}

	stdoutTail := newTailBuffer(kubectlOutputTailLimit)
	stderrTail := newTailBuffer(kubectlOutputTailLimit)
	cmd.Stdout = stdoutTail
	cmd.Stderr = stderrTail

	if err := cmd.Run(); err != nil {
		if detail := trimAndJoin(stdoutTail.String(), stderrTail.String()); detail != "" {
			return fmt.Errorf("%s (%s): %w", errPrefix, detail, err)
		}
		return fmt.Errorf("%s: %w", errPrefix, err)
	}

	return nil
}

// -------------------- Métodos principais --------------------

// Up cria o pod/deployment a partir de um manifesto YAML
func (r KubernetesRuntime) Up(podOrContainerName, namespace, manifestFile string, waitContainerRunning bool) error {
	args := addNamespaceArg(namespace, []string{"apply", "-f", manifestFile})
	cmd := r.buildKubectlCmd(true, args...)
	if err := r.runKubectlCommand(cmd, "erro ao aplicar manifesto"); err != nil {
		return err
	}

	if waitContainerRunning {
		if err := r.WaitContainerRunning(podOrContainerName, namespace, 120*time.Second); err != nil {
			return fmt.Errorf("o pod %s não ficou pronto: %w", podOrContainerName, err)
		}
	}

	return nil
}

func (r KubernetesRuntime) Apply(namespace, manifestFile string, force bool) error {
	if force {
		deleteArgs := addNamespaceArg(namespace, []string{"delete", "-f", manifestFile, "--ignore-not-found"})
		deleteArgs = append(deleteArgs, "--grace-period=0", "--force")
		deleteCmd := r.buildKubectlCmd(true, deleteArgs...)
		if err := r.runKubectlCommand(deleteCmd, "erro ao forçar reaplicação do manifesto"); err != nil {
			return err
		}
	}

	args := addNamespaceArg(namespace, []string{"apply", "-f", manifestFile})
	cmd := r.buildKubectlCmd(true, args...)
	if err := r.runKubectlCommand(cmd, "erro ao aplicar manifesto"); err != nil {
		return err
	}
	return nil
}

func (r KubernetesRuntime) Delete(namespace, manifestFile string, force bool) error {
	args := []string{"delete", "-f", manifestFile, "--ignore-not-found"}
	if force {
		args = append(args, "--grace-period=0", "--force")
	}
	args = addNamespaceArg(namespace, args)

	cmd := r.buildKubectlCmd(true, args...)
	if err := r.runKubectlCommand(cmd, "erro ao deletar manifesto"); err != nil {
		return err
	}
	return nil
}

func (r KubernetesRuntime) Down(podOrContainerName, namespace string, force bool) error {
	deletePodArgs := []string{"delete", "pod", podOrContainerName, "--ignore-not-found"}
	if force {
		deletePodArgs = append(deletePodArgs, "--grace-period", "3")
	}
	deletePodArgs = addNamespaceArg(namespace, deletePodArgs)
	cmd := r.buildKubectlCmd(true, deletePodArgs...)
	if err := r.runKubectlCommand(cmd, fmt.Sprintf("erro ao deletar pod %s", podOrContainerName)); err != nil {
		return err
	}
	deleteSvcArgs := addNamespaceArg(namespace, []string{"delete", "svc", podOrContainerName, "--ignore-not-found"})
	cmd = r.buildKubectlCmd(true, deleteSvcArgs...)
	if err := r.runKubectlCommand(cmd, fmt.Sprintf("erro ao deletar svc %s", podOrContainerName)); err != nil {
		return err
	}
	return nil
}

func (r KubernetesRuntime) GetContainerStatus(podOrContainerName, namespace string) (ContainerStatus, error) {
	var stdout, stderr bytes.Buffer

	for attempt := 1; attempt <= 3; attempt++ {
		args := addNamespaceArg(namespace, []string{"get", "pod", podOrContainerName, "-o", "jsonpath={.status.phase}"})
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

	return ContainerStatusUnknown, fmt.Errorf("não foi possível verificar o estado do pod '%s' após 3 tentativas", podOrContainerName)
}

func (r KubernetesRuntime) IsContainerRunning(podOrContainerName, namespace string) (bool, error) {
	status, err := r.GetContainerStatus(podOrContainerName, namespace)
	if err != nil {
		return false, err
	}
	return status == ContainerStatusRunning || status == ContainerStatusSucceeded, nil
}

func (r KubernetesRuntime) WaitContainerRunning(podOrContainerName, namespace string, timeout time.Duration) error {
	const interval = 3 * time.Second
	deadline := time.Now().Add(timeout)
	var lastPhase string
	var lastDetail string

	time.Sleep(interval)

	for time.Now().Before(deadline) {
		podStatus, err := r.getPodDetailedStatus(podOrContainerName, namespace)
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
			return fmt.Errorf("pod '%s' falhou: %s", podOrContainerName, summarizePodConditions(podStatus.Status.Conditions))
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
		return fmt.Errorf("timeout aguardando pod '%s' ficar pronto. Última fase: %s. Detalhes: %s", podOrContainerName, lastPhase, lastDetail)
	}
	if lastPhase != "" {
		return fmt.Errorf("timeout aguardando pod '%s' ficar pronto. Última fase: %s", podOrContainerName, lastPhase)
	}
	if lastDetail != "" {
		return fmt.Errorf("timeout aguardando pod '%s' ficar pronto. Detalhes: %s", podOrContainerName, lastDetail)
	}
	return fmt.Errorf("timeout aguardando pod '%s' ficar pronto", podOrContainerName)
}

func (r KubernetesRuntime) StopContainer(podOrContainerName, namespace string) error {
	args := addNamespaceArg(namespace, []string{"delete", "pod", podOrContainerName})
	cmd := r.buildKubectlCmd(true, args...)
	return r.runKubectlCommand(cmd, fmt.Sprintf("erro ao deletar pod %s", podOrContainerName))
}

func (r KubernetesRuntime) ShowLogs(podOrContainerName, mainContainerName, namespace string) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	args := []string{"logs", podOrContainerName}
	if mainContainerName != "" {
		args = append(args, "-c", mainContainerName)
	}
	args = append(args, "-f")
	args = addNamespaceArg(namespace, args)
	cmd := r.buildKubectlCmd(true, args...)

	stdoutTail := newTailBuffer(4 * 1024)
	stderrTail := newTailBuffer(4 * 1024)
	cmd.Stdout = io.MultiWriter(os.Stdout, stdoutTail)
	cmd.Stderr = stderrTail

	err := cmd.Run()
	if err == nil {
		return nil
	}

	outputTail := strings.ToLower(stdoutTail.String() + stderrTail.String())
	if outputTail == "" {
		outputTail = strings.ToLower(err.Error())
	}
	if strings.Contains(outputTail, "unexpected eof") {
		return nil
	}

	if stderrContent := stderrTail.String(); stderrContent != "" {
		_, _ = os.Stderr.Write([]byte(stderrContent))
	}

	return err
}

func (r KubernetesRuntime) ExecInContainer(podOrContainerName, mainContainerName, namespace string, cmdArgs []string) ([]byte, error) {
	if podOrContainerName == "" {
		return nil, fmt.Errorf("nome do pod deve ser informado")
	}

	args := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		args = append(args, "-c", mainContainerName)
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

func (r KubernetesRuntime) getPodDetailedStatus(podOrContainerName, namespace string) (*kubectlPodStatus, error) {
	args := addNamespaceArg(namespace, []string{"get", "pod", podOrContainerName, "-o", "json"})
	cmd := r.buildKubectlCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrStr, "NotFound") {
			return nil, ErrContainerNotFound
		}
		return nil, fmt.Errorf("erro ao obter status do pod %s: %w. Stderr: %s", podOrContainerName, err, stderrStr)
	}

	var status kubectlPodStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		return nil, fmt.Errorf("erro ao decodificar status do pod %s: %w", podOrContainerName, err)
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

func (r KubernetesRuntime) GetContainerIP(podOrContainerName, namespace string) (string, error) {
	args := addNamespaceArg(namespace, []string{"get", "pod", podOrContainerName, "-o", "jsonpath={.status.podIP}"})
	cmd := r.buildKubectlCmd(true, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("falha ao obter IP do pod %s: %w. Stderr: %s", podOrContainerName, err, stderr.String())
	}

	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "", fmt.Errorf("não foi possível obter IP do pod %s", podOrContainerName)
	}

	return ip, nil
}

func (r KubernetesRuntime) CopyToContainer(src, podOrContainerName, mainContainerName, namespace, dst string, useAtomicCopy bool) error {
	if podOrContainerName == "" {
		return fmt.Errorf("nome do pod deve ser informado")
	}

	src = filepath.ToSlash(src)

	// Check if source is a directory
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("erro ao verificar origem: %w", err)
	}

	// For directories or when atomic copy is not needed, use direct copy
	if srcInfo.IsDir() || !useAtomicCopy {
		copyArgs := []string{"cp", src, fmt.Sprintf("%s:%s", podOrContainerName, dst)}
		if mainContainerName != "" {
			copyArgs = append(copyArgs, "-c", mainContainerName)
		}
		copyArgs = addNamespaceArg(namespace, copyArgs)
		copyCmd := r.buildKubectlCmd(false, copyArgs...)
		copyCmd.Stdout = os.Stdout
		copyCmd.Stderr = os.Stderr
		if err := copyCmd.Run(); err != nil {
			if srcInfo.IsDir() {
				return fmt.Errorf("erro ao copiar diretório para o pod: %w", err)
			}
			return fmt.Errorf("erro ao copiar arquivo para o pod: %w", err)
		}
		return nil
	}

	// For files with atomic copy: use temporary file + atomic move
	// This is useful for JBoss deploy to avoid deploying incomplete files
	destDir := path.Dir(dst)
	tmpName := filepath.Base(dst) + ".tmp"
	tmpDestPath := path.Join(destDir, tmpName)

	// Copia o arquivo para o container com nome temporário
	copyArgs := []string{"cp", src, fmt.Sprintf("%s:%s", podOrContainerName, tmpDestPath)}
	if mainContainerName != "" {
		copyArgs = append(copyArgs, "-c", mainContainerName)
	}
	copyArgs = addNamespaceArg(namespace, copyArgs)
	copyCmd := r.buildKubectlCmd(false, copyArgs...)
	copyCmd.Stdout = os.Stdout
	copyCmd.Stderr = os.Stderr
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("erro ao copiar arquivo temporário para o pod: %w", err)
	}

	// Move o arquivo dentro do container (rename atômico)
	mvArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		mvArgs = append(mvArgs, "-c", mainContainerName)
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

func (r KubernetesRuntime) WaitForFile(fileName string, timeout time.Duration, interval time.Duration, podOrContainerName, mainContainerName, namespace string) (bool, error) {
	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	if podOrContainerName == "" {
		return false, fmt.Errorf("nome do pod deve ser informado")
	}

	for {
		select {
		case <-timeoutChan:
			target := podOrContainerName
			if mainContainerName != "" {
				target = fmt.Sprintf("%s/%s", podOrContainerName, mainContainerName)
			}
			return false, fmt.Errorf("timeout esperando arquivo %s aparecer no pod %s", fileName, target)
		case <-ticker.C:
			running, _ := r.IsContainerRunning(podOrContainerName, namespace)
			if running {
				_, err := r.ExecInContainer(podOrContainerName, mainContainerName, namespace, []string{"test", "-f", fileName})
				if err == nil {
					return true, nil
				}
			} else {
				return false, ErrContainerNotFound
			}
		}
	}
}

func (r KubernetesRuntime) IsNamespaceExist(namespace string) (bool, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return false, fmt.Errorf("namespace deve ser informado")
	}

	cmd := r.buildKubectlCmd(true, "get", "namespace", namespace)
	cmd.Stdout = io.Discard

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrStr, "NotFound") {
			return false, nil
		}
		return false, fmt.Errorf("erro ao verificar namespace %s: %w. Stderr: %s", namespace, err, stderrStr)
	}

	return true, nil
}

func (r KubernetesRuntime) CreateNamespace(namespace string) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return fmt.Errorf("namespace deve ser informado")
	}

	exists, err := r.IsNamespaceExist(namespace)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	cmd := r.buildKubectlCmd(true, "create", "namespace", namespace)
	return r.runKubectlCommand(cmd, fmt.Sprintf("erro ao criar namespace %s", namespace))
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

func (r KubernetesRuntime) GetClusterApiServerHost() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := r.buildKubectlCmdWithContext(ctx, true, "config", "view", "--minify", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	type kubeContext struct {
		Name    string `json:"name"`
		Context struct {
			Cluster string `json:"cluster"`
		} `json:"context"`
	}

	type kubeCluster struct {
		Name    string `json:"name"`
		Cluster struct {
			Server string `json:"server"`
		} `json:"cluster"`
	}

	var kubeConfig struct {
		CurrentContext string        `json:"current-context"`
		Contexts       []kubeContext `json:"contexts"`
		Clusters       []kubeCluster `json:"clusters"`
	}

	if err := json.Unmarshal(output, &kubeConfig); err != nil {
		return ""
	}

	var targetClusterName string

	if kubeConfig.CurrentContext != "" {
		for _, ctxItem := range kubeConfig.Contexts {
			if ctxItem.Name == kubeConfig.CurrentContext {
				targetClusterName = ctxItem.Context.Cluster
				break
			}
		}
	}

	if targetClusterName == "" && len(kubeConfig.Contexts) == 1 {
		targetClusterName = kubeConfig.Contexts[0].Context.Cluster
	}

	if targetClusterName == "" && len(kubeConfig.Clusters) == 1 {
		targetClusterName = kubeConfig.Clusters[0].Name
	}

	var serverURL string
	for _, clusterItem := range kubeConfig.Clusters {
		if targetClusterName == "" || clusterItem.Name == targetClusterName {
			serverURL = strings.TrimSpace(clusterItem.Cluster.Server)
			if serverURL != "" {
				break
			}
		}
	}

	if serverURL == "" {
		return ""
	}

	if !strings.Contains(serverURL, "://") {
		serverURL = "https://" + serverURL
	}

	parsed, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}

	host := strings.TrimSpace(parsed.Hostname())
	portStr := strings.TrimSpace(parsed.Port())

	if host == "" {
		host = strings.TrimSpace(parsed.Host)
		if host == "" {
			return ""
		}
		if portStr == "" {
			if splitHost, splitPort, err := net.SplitHostPort(host); err == nil {
				host = splitHost
				portStr = splitPort
			}
		}
	}

	if portStr != "" && isLoopbackHost(host) {
		if port, err := strconv.Atoi(portStr); err == nil && port >= 1000 {
			if primaryIP := detectPrimaryIPv4(); primaryIP != "" {
				host = primaryIP
			}
		}
	}

	return host
}
