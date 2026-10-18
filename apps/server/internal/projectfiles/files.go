// Package projectfiles owns explicit host projects and snapshot exchange with
// virtual documents. Project paths never act as virtual-document entry IDs.
package projectfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type fileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

func problem(code, message string) error { return &storage.Error{Code: code, Message: message} }

func openProject(p storage.ProjectRef) (*os.Root, error) {
	root, err := os.OpenRoot(p.RootPath)
	if err != nil {
		return nil, problem("project_unavailable", "The registered project directory is unavailable.")
	}
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	identity, err := rootIdentity(info)
	if err != nil || identity != p.RootIdentity {
		root.Close()
		return nil, problem("project_changed", "The project root no longer has its registered identity.")
	}
	return root, nil
}

func relative(value string, allowRoot bool) (string, error) {
	if value == "" && allowRoot {
		return ".", nil
	}
	if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00\r\n") || (!allowRoot && value == ".") || !fs.ValidPath(value) {
		return "", problem("invalid_path", "Use a relative path within this project.")
	}
	for _, part := range strings.Split(value, "/") {
		if strings.HasPrefix(part, ".axv-export-") {
			return "", problem("invalid_path", "Internal transfer paths are not project files.")
		}
	}
	return value, nil
}

func regular(root *os.Root, name string) (*os.File, fs.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, fileError(err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, problem("not_file", "Choose a regular project file; links and special files are not opened.")
	}
	f, err := openNonblocking(root, name)
	if err != nil {
		return nil, nil, fileError(err)
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, problem("not_file", "The selected path is not a regular project file.")
	}
	return f, info, nil
}

func read(root *os.Root, name string, max int64) ([]byte, error) {
	f, info, err := regular(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info.Size() > max {
		return nil, problem("file_too_large", "This file exceeds the operation's size limit.")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fileError(err)
	}
	if int64(len(data)) > max {
		return nil, problem("file_too_large", "This file exceeds the operation's size limit.")
	}
	return data, nil
}

func fileError(err error) error {
	if os.IsNotExist(err) {
		return problem("not_found", "The project path does not exist.")
	}
	if os.IsPermission(err) {
		return problem("file_forbidden", "The server user cannot access this project path.")
	}
	return problem("file_unavailable", "The project path is unavailable or leaves its registered root.")
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func text(data []byte) bool {
	return utf8.Valid(data) && strings.IndexFunc(string(data), func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f' }) < 0
}

func list(root *os.Root, dir, cursor string, limit int) ([]fileEntry, string, bool, error) {
	offset := 0
	var err error
	if cursor != "" {
		offset, err = strconv.Atoi(cursor)
		if err != nil || offset < 0 || offset > 10000 {
			return nil, "", false, problem("invalid_cursor", "Use the cursor returned by this directory listing.")
		}
	}
	f, err := openNonblocking(root, dir)
	if err != nil {
		return nil, "", false, fileError(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return nil, "", false, problem("invalid_directory", "Choose a project folder.")
	}
	entries, err := f.ReadDir(10001)
	if err != nil && err != io.EOF {
		return nil, "", false, fileError(err)
	}
	truncated := len(entries) > 10000
	if truncated {
		entries = entries[:10000]
	}
	items := []fileEntry{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".axv-export-") || !utf8.ValidString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if info.IsDir() {
			kind = "folder"
		} else if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		} else if !info.Mode().IsRegular() {
			continue
		}
		items = append(items, fileEntry{Name: e.Name(), Path: path.Join(dir, e.Name()), Kind: kind, Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	if offset > len(items) {
		return nil, "", truncated, problem("invalid_cursor", "The directory changed; refresh its listing.")
	}
	end := min(offset+limit, len(items))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[offset:end], next, truncated, nil
}
