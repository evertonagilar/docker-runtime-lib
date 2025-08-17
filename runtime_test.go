package container

import (
	"os"
	"path/filepath"
	"testing"
)

// Função auxiliar para criar runtime ou pular teste
func getRuntimeOrSkip(t *testing.T) TContainerRuntime {
	runtime, err := NewDockerRuntime()
	if err != nil {
		t.Skipf("Docker não disponível, pulando teste: %v", err)
	}
	t.Log("✅ Docker runtime disponível")
	return runtime
}

func TestUpAndDown(t *testing.T) {
	r := getRuntimeOrSkip(t)
	composeFile := createTempCompose(t)
	t.Logf("📁 Compose file criado em: %s", composeFile)

	t.Log("⬆️ Subindo container...")
	if err := r.Up(composeFile); err != nil {
		t.Fatalf("falha ao subir container: %v", err)
	}
	t.Log("✅ Container subiu com sucesso")

	t.Log("⬇️ Derrubando container...")
	if err := r.Down("test_container"); err != nil {
		t.Fatalf("falha ao derrubar container: %v", err)
	}
	t.Log("✅ Container derrubado com sucesso")
}

func TestIsContainerRunning(t *testing.T) {
	r := getRuntimeOrSkip(t)
	composeFile := createTempCompose(t)
	t.Logf("📁 Compose file criado em: %s", composeFile)

	t.Log("⬆️ Subindo container para teste de status...")
	if err := r.Up(composeFile); err != nil {
		t.Fatalf("falha ao subir container: %v", err)
	}
	defer func() {
		t.Log("⬇️ Derrubando container após teste de status...")
		if err := r.Down("test_container"); err != nil {
			t.Fatalf("falha ao derrubar container: %v", err)
		}
	}()

	t.Log("🔍 Verificando se container está rodando...")
	running, err := r.IsContainerRunning("test_container")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !running {
		t.Errorf("❌ Esperava container rodando, mas não está")
	} else {
		t.Log("✅ Container está rodando")
	}
}

func TestStopContainer(t *testing.T) {
	r := getRuntimeOrSkip(t)
	composeFile := createTempCompose(t)
	t.Logf("📁 Compose file criado em: %s", composeFile)

	t.Log("⬆️ Subindo container para teste de stop...")
	if err := r.Up(composeFile); err != nil {
		t.Fatalf("falha ao subir container: %v", err)
	}
	defer func() {
		t.Log("⬇️ Derrubando container após teste de stop...")
		if err := r.Down("test_container"); err != nil {
			t.Fatalf("falha ao derrubar container: %v", err)
		}
	}()

	t.Log("🛑 Parando container...")
	if err := r.StopContainer("test_container"); err != nil {
		t.Fatalf("falha ao parar container: %v", err)
	}

	t.Log("🔍 Verificando se container parou...")
	running, err := r.IsContainerRunning("test_container")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if running {
		t.Errorf("❌ Esperava container parado, mas está rodando")
	} else {
		t.Log("✅ Container parou corretamente")
	}
}

// ############ Função auxiliar ############
func createTempCompose(t *testing.T) string {
	dir := t.TempDir() // cria pasta temporária
	composeFile := filepath.Join(dir, "docker-compose.yml")
	content := `
services:
  test_container:
    image: nginx:latest
    container_name: test_container
`
	err := os.WriteFile(composeFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("falha ao criar docker-compose.yml: %v", err)
	}
	t.Logf("✅ docker-compose.yml criado em %s", composeFile)
	return composeFile
}
