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

	manifest, err := generateManifest(runCfg, "alpine:latest", "/workspace", command, envs, nil, "", "")
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

	volumes := []TVolume{
		{
			HostPath:  "data",
			MountPath: "/mnt/data",
			ReadOnly:  true,
		},
	}
	manifest, err := generateManifest(runCfg, "alpine:latest", "/workspace", command, envs, volumes, "", "demo")
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

func TestGenerateManifestWithStorageClass(t *testing.T) {
	runCfg := runSettings{PodName: "demo", Namespace: "tools"}
	envs := map[string]string{"UID": "1000", "GID": "1000"}
	command := []string{"/bin/bash", "-c", "echo hello"}

	volumes := []TVolume{
		{
			HostPath:  "dados",
			MountPath: "/mnt/storage",
		},
	}
	manifest, err := generateManifest(runCfg, "alpine:latest", "/workspace", command, envs, volumes, "fast-storage", "demo-run")
	if err != nil {
		t.Fatalf("esperava manifesto sem erro, recebi: %v", err)
	}

	output := string(manifest)

	expectedSnippets := []string{
		"kind: PersistentVolume",
		"name: demo-run-pv-0",
		"storageClassName: fast-storage",
		"kind: PersistentVolumeClaim",
		"claimName: demo-run-pvc-0",
		"persistentVolumeClaim:",
		"volumeName: demo-run-pv-0",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Fatalf("manifesto não contém trecho esperado: %q\nManifesto:\n%s", snippet, output)
		}
	}
}

func TestGenerateManifestWithDynamicProvisioning(t *testing.T) {
	runCfg := runSettings{PodName: "demo", Namespace: "tools"}
	envs := map[string]string{"UID": "1000", "GID": "1000"}
	command := []string{"/bin/bash", "-c", "echo hello"}

	volumes := []TVolume{
		{
			MountPath:    "/mnt/dynamic",
			StorageClass: "fast-storage",
		},
	}
	manifest, err := generateManifest(runCfg, "alpine:latest", "/workspace", command, envs, volumes, "", "dynamic-run")
	if err != nil {
		t.Fatalf("esperava manifesto sem erro, recebi: %v", err)
	}

	output := string(manifest)

	if strings.Contains(output, "\nkind: PersistentVolume\n") {
		t.Fatalf("manifesto não deveria conter PersistentVolume para provisionamento dinâmico:\n%s", output)
	}
	if strings.Contains(output, "hostPath:") {
		t.Fatalf("manifesto não deveria conter hostPath para provisionamento dinâmico:\n%s", output)
	}
	if strings.Contains(output, "volumeName:") {
		t.Fatalf("manifesto não deveria definir volumeName na PVC para provisionamento dinâmico:\n%s", output)
	}

	expectedSnippets := []string{
		"kind: PersistentVolumeClaim",
		"storageClassName: fast-storage",
		"persistentVolumeClaim:",
		"claimName: dynamic-run-pvc-0",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(output, snippet) {
			t.Fatalf("manifesto não contém trecho esperado: %q\nManifesto:\n%s", snippet, output)
		}
	}
}
