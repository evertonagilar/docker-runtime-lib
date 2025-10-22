package container

import (
	"fmt"
	"os/exec"
	"strings"
)

func QuoteList(list []string) []string {
	quoted := make([]string, len(list))
	for i, s := range list {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return quoted
}

func CheckKubectlAvailable() error {
	cmd := exec.Command("kubectl", "version", "--client")
	if output, err := cmd.Output(); err != nil {
		return fmt.Errorf("kubectl não está instalado")
	} else {
		_ = strings.TrimSpace(string(output))
	}
	return nil
}

func CheckK8sContextAvailable() error {
	cmd := exec.Command("kubectl", "config", "current-context")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("kubectl sem contexto configurado. Use 'kubectl config use-context <nome>'")
	}
	ctx := strings.TrimSpace(string(output))
	if ctx == "" || strings.EqualFold(ctx, "none") {
		return fmt.Errorf("nenhum contexto Kubernetes selecionado no kubectl")
	}
	return nil
}

func CheckKubernetesAvailable() error {
	if err := CheckKubectlAvailable(); err != nil {
		return err
	}
	if err := CheckK8sContextAvailable(); err != nil {
		return err
	}

	return nil
}
