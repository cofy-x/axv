package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *service) directory(relative string) (string, error) {
	if err := s.projectReady(); err != nil {
		return "", err
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("directory must be relative to the project volume")
	}
	root, err := filepath.EvalSymlinks(s.config.ProjectRoot)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("directory is outside the project volume")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("working directory does not exist")
	}
	return path, nil
}
