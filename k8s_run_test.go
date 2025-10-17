package container

import (
	"strings"
	"testing"
)

func TestGenerateManifestNoVolumes(t *testing.T) {
	runCfg := runSettings{PodName: "demo", Namespace: "tools"}
	envs := map[string]string{
		"UID":   "1000",
		"GID":   "1000",
		"DEBUG": "true",
	}

	command := []string{"/bin/bash", "-c", "echo hello"}

	manifest, err := generateManifest(runCfg, "alpine:latest", "/workspace", command, envs, nil)
	if err != nil {
		t.Fatalf("esperava manifesto sem erro, recebi: %v", err)
	}

	output := string(manifest)

	expectedSnippets := []string{
		"command: [\"/bin/bash\", \"-c\", \"echo hello\"]",
		"env:\n        - name: DEBUG\n          value: \"true\"",
		"volumeMounts: []",
		"volumes: []",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Fatalf("manifesto não contém trecho esperado: %q\nManifesto:\n%s", snippet, output)
		}
	}
}

func TestGenerateManifestWithVolumes(t *testing.T) {
	runCfg := runSettings{PodName: "demo", Namespace: "tools"}
	envs := map[string]string{"UID": "1000", "GID": "1000"}
	command := []string{"/bin/bash", "-c", "echo hello"}

	volumes := []string{"data:/mnt/data:ro"}
	manifest, err := generateManifest(runCfg, "alpine:latest", "/workspace", command, envs, volumes)
	if err != nil {
		t.Fatalf("esperava manifesto sem erro, recebi: %v", err)
	}

	output := string(manifest)

	expectedSnippets := []string{
		"- name: vol-0",
		"mountPath: /mnt/data",
		"readOnly: true",
		"path: /sigctl/data",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Fatalf("manifesto não contém trecho esperado: %q\nManifesto:\n%s", snippet, output)
		}
	}
}
