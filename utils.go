package container

import (
	"fmt"
	"net"
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

func detectPrimaryIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var fallback string

	for _, iface := range interfaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipv4 := extractIPv4(addr)
			if ipv4 == nil {
				continue
			}

			if isPrivateIPv4(ipv4) {
				return ipv4.String()
			}

			if fallback == "" {
				fallback = ipv4.String()
			}
		}
	}

	return fallback
}

func extractIPv4(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP.To4()
	case *net.IPAddr:
		return v.IP.To4()
	default:
		return nil
	}
}

func isPrivateIPv4(ip net.IP) bool {
	if ip == nil {
		return false
	}
	ip = ip.To4()
	if ip == nil {
		return false
	}

	switch {
	case ip[0] == 10:
		return true
	case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
		return true
	case ip[0] == 192 && ip[1] == 168:
		return true
	default:
		return false
	}
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || host == "127.0.0.1" {
		return true
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}

	return false
}
