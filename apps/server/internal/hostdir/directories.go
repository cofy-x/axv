// Package hostdir owns local host-directory selection, separately from virtual documents.
package hostdir

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Directory is a directory candidate within a host-directory listing.
type Directory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Listing describes a canonical host directory and its directory candidates.
type Listing struct {
	Path        string      `json:"path"`
	ParentPath  *string     `json:"parentPath"`
	HomePath    string      `json:"homePath"`
	Directories []Directory `json:"directories"`
	Truncated   bool        `json:"truncated"`
}

// CanonicalDirectory validates a readable absolute host directory and resolves symlinks.
func CanonicalDirectory(path string) (string, error) {
	if len(path) == 0 || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", failure(InvalidPath, "invalid_directory", "Choose an absolute host directory path.")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", failure(Unavailable, "directory_unavailable", "The host directory does not exist or cannot be accessed.")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", failure(Unavailable, "invalid_directory", "Choose an existing host directory, not a file.")
	}
	if !info.IsDir() {
		return "", failure(NotDirectory, "invalid_directory", "Choose an existing host directory, not a file.")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return "", failure(Unreadable, "directory_forbidden", "The server user cannot read this directory.")
	}
	defer f.Close()
	if _, err = f.Readdirnames(1); err != nil && err != io.EOF {
		return "", failure(Unreadable, "directory_forbidden", "The server user cannot read this directory.")
	}
	return resolved, nil
}

// List returns directory candidates at path, or the service user's home when empty.
// It resolves the selected directory without creating or modifying host files.
func List(path string) (Listing, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Listing{}, failure(HomeUnavailable, "home_unavailable", "The server user's home directory is unavailable.")
	}
	home, err = CanonicalDirectory(home)
	if err != nil {
		return Listing{}, err
	}
	if path == "" {
		path = home
	}
	path, err = CanonicalDirectory(path)
	if err != nil {
		return Listing{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Listing{}, failure(Unreadable, "directory_forbidden", "The server user cannot read this directory.")
	}
	defer f.Close()
	result := Listing{Path: path, HomePath: home, Directories: []Directory{}}
	for scanned := 0; ; {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			scanned++
			child := filepath.Join(path, entry.Name())
			isDir := entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				info, err := os.Stat(child)
				isDir = err == nil && info.IsDir()
			}
			if isDir && utf8.ValidString(entry.Name()) {
				result.Directories = append(result.Directories, Directory{Name: entry.Name(), Path: child})
			}
		}
		if len(result.Directories) >= 1000 || scanned >= 10000 {
			result.Truncated = true
			break
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Listing{}, failure(Unreadable, "directory_forbidden", "The directory could not be read. Check its permissions.")
		}
	}
	sort.Slice(result.Directories, func(i, j int) bool {
		return strings.ToLower(result.Directories[i].Name) < strings.ToLower(result.Directories[j].Name)
	})
	if len(result.Directories) > 1000 {
		result.Directories = result.Directories[:1000]
	}
	if parent := filepath.Dir(path); parent != path {
		result.ParentPath = &parent
	}
	return result, nil
}
