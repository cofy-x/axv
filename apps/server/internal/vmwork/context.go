// Package vmwork defines one immutable VM project context for a host agent run.
// It has no dependency on Codex, desktop storage, configuration or a controller.
package vmwork

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

const (
	Version         = 1
	SkillName       = "axv-vm-project"
	maxContextBytes = 64 << 10
)

//go:embed skills/axv-vm-project/SKILL.md
var skillFiles embed.FS

// Project preserves persistent project identity separately from its environment.
type Project struct {
	Project compute.Project        `json:"project"`
	Binding compute.ProjectBinding `json:"binding"`
}

// SameProjectIdentity compares the retained project itself, independently of a
// new VM binding, display name or attachment revision explicitly selected later.
func SameProjectIdentity(a, b Project) bool {
	left, right := a.Project, b.Project
	return left.ID == right.ID && left.HostID == right.HostID && left.VolumeID == right.VolumeID && left.Root == right.Root && left.RootIdentity == right.RootIdentity
}

// Context captures connection and executable identities at launch. Helpers never
// resolve a mutable profile or activate a replacement host from these values.
type Context struct {
	Version           int     `json:"version"`
	SessionID         string  `json:"sessionId"`
	RunID             string  `json:"runId"`
	Project           Project `json:"project"`
	Socket            string  `json:"socket"`
	ConfigFingerprint string  `json:"configFingerprint"`
	Executable        string  `json:"executable"`
}

func identity(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func absolutePath(value string) bool {
	return utf8.ValidString(value) && filepath.IsAbs(value) && filepath.Clean(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func ValidateBinding(binding compute.ProjectBinding) error {
	for _, value := range []string{binding.HostID, binding.ProjectID, binding.VolumeID, binding.MachineID, binding.BootID, binding.InstanceID} {
		if !identity(value) {
			return errors.New("the VM project context requires complete original environment identities")
		}
	}
	if !compute.IsMachineID(binding.MachineID) || binding.ProjectRevision < 1 || binding.VolumeRevision < 1 {
		return errors.New("the VM project context requires the original VM ID and positive revisions")
	}
	return nil
}

func ValidateProject(project Project) error {
	if err := ValidateBinding(project.Binding); err != nil {
		return err
	}
	p, binding := project.Project, project.Binding
	if p.Kind != "vm" || p.ID != binding.ProjectID || p.HostID != binding.HostID || p.VolumeID != binding.VolumeID || p.Revision != binding.ProjectRevision || !identity(p.RootIdentity) {
		return errors.New("the VM project context must identify the same persistent project, volume and reviewed revision")
	}
	if !utf8.ValidString(p.Name) || len(p.Name) > 512 || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return errors.New("the VM project name must be valid text")
	}
	return compute.ValidateProjectRoot(p.Root)
}

func ValidateContext(value Context) error {
	if value.Version != Version || !identity(value.SessionID) || !identity(value.RunID) || !identity(value.ConfigFingerprint) || !absolutePath(value.Socket) || !absolutePath(value.Executable) {
		return errors.New("the VM project context has an invalid version, run or captured client identity")
	}
	return ValidateProject(value.Project)
}

// ReadContext rejects damaged or ambiguous metadata rather than selecting a new
// environment. Its parent directory and the file must remain private and owned.
func ReadContext(path string) (Context, error) {
	var value Context
	data, err := readPrivate(path, maxContextBytes)
	if err != nil {
		return value, err
	}
	if !jsonunicode.Valid(data) || uniqueJSON(data) != nil {
		return value, errors.New("invalid VM project context JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("invalid VM project context: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return value, errors.New("the VM project context must contain one JSON object")
	}
	return value, ValidateContext(value)
}

// ValidateSkill permits only the packaged instructions, never editable project
// content masquerading as the trusted AXV integration skill.
func ValidateSkill(path string) error {
	data, err := readPrivate(path, maxContextBytes)
	if err != nil {
		return err
	}
	original, _ := skillFiles.ReadFile("skills/axv-vm-project/SKILL.md")
	if !bytes.Equal(data, original) {
		return errors.New("the AXV VM project skill changed after publication")
	}
	return nil
}

// Publish materializes immutable launch metadata and the packaged skill below
// the private run directory. An existing differing file is never overwritten.
func Publish(directory string, value Context) (contextPath, skillPath string, err error) {
	if err = ValidateContext(value); err != nil {
		return "", "", err
	}
	root, err := privateDirectory(directory)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	skillDirectory := filepath.Join(directory, "skills", SkillName)
	for _, child := range []string{"skills", filepath.Join("skills", SkillName)} {
		if err = root.Mkdir(child, 0700); err != nil && !os.IsExist(err) {
			return "", "", err
		}
		info, statErr := root.Lstat(child)
		if statErr != nil || !info.IsDir() || !private(info) {
			return "", "", errors.New("the AXV skill directory is not a private owned directory")
		}
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", "", err
	}
	contextPath = filepath.Join(directory, "vm-project.json")
	skillPath = filepath.Join(skillDirectory, "SKILL.md")
	original, _ := skillFiles.ReadFile("skills/axv-vm-project/SKILL.md")
	if err = publish(contextPath, append(data, '\n')); err == nil {
		err = publish(skillPath, original)
	}
	return contextPath, skillPath, err
}

func privateDirectory(directory string) (*os.Root, error) {
	if !absolutePath(directory) {
		return nil, errors.New("the VM project directory requires an absolute canonical path")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !private(info) {
		return nil, errors.New("the VM project directory must be private and owned by this user")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) || !private(opened) {
		root.Close()
		return nil, errors.New("the VM project directory identity changed")
	}
	return root, nil
}

func readPrivate(path string, limit int64) ([]byte, error) {
	if !absolutePath(path) {
		return nil, errors.New("the VM project metadata requires an absolute canonical path")
	}
	root, err := privateDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !private(info) || info.Size() > limit {
		return nil, errors.New("the VM project metadata must be a bounded private regular file")
	}
	file, err := openPrivate(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || !private(opened) || opened.Size() > limit {
		return nil, errors.New("the VM project metadata identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("the VM project metadata exceeds its size limit")
	}
	return data, nil
}

func publish(path string, data []byte) error {
	root, err := privateDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	name := ".vm-project-" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	defer file.Close()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = root.Link(name, filepath.Base(path)); err != nil {
		if !os.IsExist(err) {
			return err
		}
		current, readErr := readPrivate(path, maxContextBytes)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(current, data) {
			return errors.New("the immutable VM project publication already contains different data")
		}
	}
	if err = root.Remove(name); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// A duplicate field is ambiguous even if encoding/json would accept the last
// value. Inspect every object, including nested project and binding identities.
func uniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := make(map[string]bool)
			for decoder.More() {
				token, err := decoder.Token()
				name, ok := token.(string)
				// Match encoding/json's Unicode simple-fold, including aliases
				// such as sessionId and ſessionId that lowercasing keeps distinct.
				key := foldJSONName(name)
				if err != nil || !ok || seen[key] {
					return errors.New("duplicate or invalid context field")
				}
				seen[key] = true
				if err := value(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		case json.Delim('['):
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		}
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("the VM project context must contain one JSON value")
	}
	return nil
}

func foldJSONName(name string) string {
	return strings.Map(func(r rune) rune {
		for {
			next := unicode.SimpleFold(r)
			if next <= r {
				return next
			}
			r = next
		}
	}, name)
}
