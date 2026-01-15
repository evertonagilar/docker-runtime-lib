package container

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// normalizeCopySrcPath normalizes local source path for Windows compatibility
func normalizeCopySrcPath(src string) string {
	if runtime.GOOS != "windows" {
		return src
	}
	if !strings.Contains(src, ":") {
		return src
	}

	cwd, err := os.Getwd()
	if err != nil {
		return src
	}
	rel, err := filepath.Rel(cwd, src)
	if err != nil {
		return src
	}
	if rel == "" {
		return "."
	}
	if strings.Contains(rel, ":") {
		return src
	}
	return rel
}

// normalizeCopyDstPath normalizes local destination path for Windows compatibility
func normalizeCopyDstPath(dst string) string {
	if runtime.GOOS != "windows" {
		return dst
	}
	if !strings.Contains(dst, ":") {
		return dst
	}

	cwd, err := os.Getwd()
	if err != nil {
		return dst
	}
	rel, err := filepath.Rel(cwd, dst)
	if err != nil {
		return dst
	}
	if rel == "" {
		return "."
	}
	if strings.Contains(rel, ":") {
		return dst
	}
	return rel
}

// getRsyncBinPath returns the rsync binary path based on the operating system
func getRsyncBinPath() string {
	if runtime.GOOS == "windows" {
		// On Windows, rsync is typically available through WSL, Git Bash, or Cygwin
		// Try to find it in PATH first
		if path, err := exec.LookPath("rsync"); err == nil {
			return path
		}
		// Common Windows locations
		commonPaths := []string{
			"C:\\msys64\\usr\\bin\\rsync.exe",
			"C:\\cygwin64\\bin\\rsync.exe",
		}
		for _, path := range commonPaths {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		// Fallback to just "rsync" and hope it's in PATH
		return "rsync"
	}
	// On Unix-like systems (Linux, macOS), try to find rsync in PATH
	if path, err := exec.LookPath("rsync"); err == nil {
		return path
	}
	// Common Unix locations
	commonPaths := []string{
		"/usr/bin/rsync",
		"/usr/local/bin/rsync",
		"/opt/homebrew/bin/rsync", // macOS with Homebrew on Apple Silicon
	}
	for _, path := range commonPaths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// Fallback to just "rsync" and hope it's in PATH
	return "rsync"
}

// normalizeRsyncPath converts Windows paths to Unix-style paths for Cygwin/MSYS2 rsync
func normalizeRsyncPath(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}

	// Convert backslashes to forward slashes
	path = strings.ReplaceAll(path, "\\", "/")

	// Convert Windows drive letters to Cygwin/MSYS2 format
	// C:/path -> /cygdrive/c/path (Cygwin) or /c/path (MSYS2)
	// We'll use MSYS2 format as it's more common with Git Bash
	if len(path) >= 2 && path[1] == ':' {
		drive := strings.ToLower(string(path[0]))
		rest := path[2:]
		// Check if we're using Cygwin rsync
		rsyncPath := getRsyncBinPath()
		if strings.Contains(strings.ToLower(rsyncPath), "cygwin") {
			return "/cygdrive/" + drive + rest
		}
		// MSYS2/Git Bash format
		return "/" + drive + rest
	}

	return path
}
