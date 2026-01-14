package container

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TChecksumMap stores checksums for subdirectories
type TChecksumMap map[string]string

// CopyToContainerIncremental copies a directory to a container incrementally.
// It calculates checksums for each subdirectory and only copies those that have changed.
func (r KubernetesRuntime) CopyToContainerIncremental(srcDir, podOrContainerName, mainContainerName, namespace, dstPath string, debug bool) error {
	if debug {
		fmt.Printf("📊 Calculando checksums do código fonte em %s\n", srcDir)
	}

	// Calculate checksums for all subdirectories in srcDir
	localChecksums, err := calculateDirectoryChecksums(srcDir, debug)
	if err != nil {
		return fmt.Errorf("erro ao calcular checksums locais: %w", err)
	}

	// Retrieve existing checksums from container (if any)
	checksumFile := filepath.Join(dstPath, ".checksums.json")
	remoteChecksums, err := r.getRemoteChecksums(checksumFile, podOrContainerName, mainContainerName, namespace, debug)

	// First run: destination doesn't exist, do full copy
	if err != nil {
		if debug {
			fmt.Printf("⚠️  Não foi possível recuperar checksums remotos (primeira execução?): %v\n", err)
			fmt.Println("📤 Primeira execução: enviando todo o código fonte")
		}

		// Do full copy of entire directory
		if err := r.CopyToContainer(srcDir, podOrContainerName, mainContainerName, namespace, dstPath); err != nil {
			return fmt.Errorf("erro ao copiar código fonte completo: %w", err)
		}

		// Save checksums for next time
		if err := r.saveRemoteChecksums(checksumFile, localChecksums, podOrContainerName, mainContainerName, namespace, debug); err != nil {
			return fmt.Errorf("erro ao salvar checksums remotos: %w", err)
		}

		return nil
	}

	// Compare and identify what needs to be copied
	dirsToSync := identifyChangedDirectories(localChecksums, remoteChecksums, debug)

	if len(dirsToSync) == 0 {
		if debug {
			fmt.Println("✅ Nenhuma alteração detectada nas subpastas")
		}
	} else {
		if debug {
			fmt.Printf("📤 Enviando %d subpasta(s) modificada(s)\n", len(dirsToSync))
		}

		// Copy only changed directories
		for _, subdir := range dirsToSync {
			srcPath := filepath.Join(srcDir, subdir)
			dstSubPath := filepath.Join(dstPath, subdir)

			if debug {
				fmt.Printf("  📁 %s\n", subdir)
			}

			// Remove existing directory in container to avoid conflicts
			execArgs := []string{"exec", podOrContainerName}
			if mainContainerName != "" {
				execArgs = append(execArgs, "-c", mainContainerName)
			}
			execArgs = append(execArgs, "--", "rm", "-rf", dstSubPath)
			execArgs = addNamespaceArg(namespace, execArgs)

			cmd := r.buildKubectlCmd(true, execArgs...)
			_ = cmd.Run() // Ignora erro se pasta não existir

			// Use existing CopyToContainer for each subdirectory
			if err := r.CopyToContainer(srcPath, podOrContainerName, mainContainerName, namespace, dstSubPath); err != nil {
				return fmt.Errorf("erro ao copiar %s: %w", subdir, err)
			}
		}
	}

	// Always copy root-level files (they don't have checksums)
	rootFiles, err := getRootLevelFiles(srcDir)
	if err != nil {
		return fmt.Errorf("erro ao listar arquivos da raiz: %w", err)
	}

	if len(rootFiles) > 0 {
		if debug {
			fmt.Printf("📄 Enviando %d arquivo(s) da raiz\n", len(rootFiles))
		}

		for _, file := range rootFiles {
			srcPath := filepath.Join(srcDir, file)
			dstFilePath := filepath.Join(dstPath, file)

			if debug {
				fmt.Printf("  📄 %s\n", file)
			}

			if err := r.CopyToContainer(srcPath, podOrContainerName, mainContainerName, namespace, dstFilePath); err != nil {
				return fmt.Errorf("erro ao copiar arquivo raiz %s: %w", file, err)
			}
		}
	}

	// Update checksums in container
	if err := r.saveRemoteChecksums(checksumFile, localChecksums, podOrContainerName, mainContainerName, namespace, debug); err != nil {
		return fmt.Errorf("erro ao salvar checksums remotos: %w", err)
	}

	return nil
}

// getRootLevelFiles returns list of files (not directories) in the root of srcDir
func getRootLevelFiles(srcDir string) ([]string, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			// Skip hidden files
			name := entry.Name()
			if name[0] != '.' {
				files = append(files, name)
			}
		}
	}

	return files, nil
}

// calculateDirectoryChecksums calculates SHA256 checksums for each subdirectory
func calculateDirectoryChecksums(rootDir string, debug bool) (TChecksumMap, error) {
	checksums := make(TChecksumMap)

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip hidden directories and common build artifacts
		name := entry.Name()
		if name[0] == '.' || name == "build" || name == "target" || name == "node_modules" {
			continue
		}

		subPath := filepath.Join(rootDir, name)
		checksum, err := calculateDirChecksum(subPath)
		if err != nil {
			return nil, fmt.Errorf("erro ao calcular checksum de %s: %w", name, err)
		}

		checksums[name] = checksum

		if debug {
			fmt.Printf("  %s: %s\n", name, checksum[:12])
		}
	}

	return checksums, nil
}

// calculateDirChecksum calculates a checksum for files in the first level of a directory
// Only includes direct children (non-recursive) for safety and performance
func calculateDirChecksum(dirPath string) (string, error) {
	hash := sha256.New()

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "", err
	}

	// Sort entries for consistent ordering
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	// Process in sorted order for consistency
	for _, name := range names {
		entryPath := filepath.Join(dirPath, name)
		info, err := os.Stat(entryPath)
		if err != nil {
			continue // Skip if can't stat
		}

		// Include name in hash
		hash.Write([]byte(name))

		// Mark if it's a directory
		if info.IsDir() {
			hash.Write([]byte("/"))
		} else {
			// For files, include content
			file, err := os.Open(entryPath)
			if err != nil {
				continue // Skip if can't open
			}
			io.Copy(hash, file)
			file.Close()
		}
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// identifyChangedDirectories compares local and remote checksums
func identifyChangedDirectories(local, remote TChecksumMap, debug bool) []string {
	var changed []string

	for dir, localSum := range local {
		remoteSum, exists := remote[dir]
		if !exists || localSum != remoteSum {
			changed = append(changed, dir)
			if debug && exists {
				fmt.Printf("  🔄 %s modificado\n", dir)
			} else if debug {
				fmt.Printf("  ➕ %s novo\n", dir)
			}
		}
	}

	return changed
}

// getRemoteChecksums retrieves checksums from the container
func (r KubernetesRuntime) getRemoteChecksums(checksumFile, podOrContainerName, mainContainerName, namespace string, debug bool) (TChecksumMap, error) {
	// Try to read checksum file from container
	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "cat", checksumFile)
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var checksums TChecksumMap
	if err := json.Unmarshal(output, &checksums); err != nil {
		return nil, err
	}

	return checksums, nil
}

// saveRemoteChecksums saves checksums to the container
func (r KubernetesRuntime) saveRemoteChecksums(checksumFile string, checksums TChecksumMap, podOrContainerName, mainContainerName, namespace string, debug bool) error {
	data, err := json.Marshal(checksums)
	if err != nil {
		return err
	}

	// Escape single quotes in JSON for shell
	jsonStr := strings.ReplaceAll(string(data), "'", "'\\''")

	// Write checksums to container using echo and redirection
	execArgs := []string{"exec", podOrContainerName}
	if mainContainerName != "" {
		execArgs = append(execArgs, "-c", mainContainerName)
	}
	execArgs = append(execArgs, "--", "sh", "-c", fmt.Sprintf("echo '%s' > %s", jsonStr, checksumFile))
	execArgs = addNamespaceArg(namespace, execArgs)

	cmd := r.buildKubectlCmd(true, execArgs...)
	if err := cmd.Run(); err != nil {
		return err
	}

	if debug {
		fmt.Printf("💾 Checksums salvos em %s\n", checksumFile)
	}

	return nil
}
