//go:build !linux && !darwin

package guest

import (
	"fmt"
	"os"
	"os/exec"
)

func startTerminal(string, uint16, uint16) (*os.File, *exec.Cmd, error) {
	return nil, nil, fmt.Errorf("the AXV guest service requires Linux")
}

func (s *terminal) resize(uint16, uint16) error {
	return fmt.Errorf("the AXV guest service requires Linux")
}

func (s *terminal) terminate() {
	s.terminateOnce.Do(func() {
		_ = s.cmd.Process.Kill()
		s.file.Close()
		<-s.done
	})
}
