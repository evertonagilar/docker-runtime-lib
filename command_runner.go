package container

import (
	"fmt"
	"os/exec"
)

func runCommandWithDebug(cmd *exec.Cmd, debug bool) error {
	err := cmd.Run()
	if err != nil && debug {
		fmt.Printf("[debug] erro ao executar comando: %s (erro: %v)\n", cmd.String(), err)
	}
	return err
}
