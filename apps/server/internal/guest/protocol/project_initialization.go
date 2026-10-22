package protocol

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProjectInitialization is host-owned task intent, never a shell marker. The
// ordinary desktop task submission API cannot supply it.
type ProjectInitialization struct {
	Directory  string `json:"directory"`
	Repository string `json:"repository,omitempty"`
	Name       string `json:"name,omitempty"`
}

var scpRepository = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9_./~%-]+$`)

func ValidateRepositoryURL(repository string) error {
	if repository == "" {
		return nil
	}
	if !utf8.ValidString(repository) || len(repository) > 4096 || strings.TrimSpace(repository) != repository || strings.Contains(repository, "#") || strings.IndexFunc(repository, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 {
		return fmt.Errorf("use a credential-free HTTPS or SSH Git URL")
	}
	if scpRepository.MatchString(repository) {
		return nil
	}
	u, err := url.Parse(repository)
	if err != nil || u.Hostname() == "" || u.Path == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "ssh") {
		return fmt.Errorf("use a credential-free HTTPS or SSH Git URL without query parameters or fragments")
	}
	if u.User != nil {
		_, password := u.User.Password()
		if u.Scheme != "ssh" || password || u.User.Username() == "" {
			return fmt.Errorf("repository URLs cannot contain passwords or HTTPS credentials; configure authentication inside the VM")
		}
	}
	return nil
}

func (in ProjectInitialization) Validate() error {
	directory := in.Directory
	if directory == "" || !utf8.ValidString(directory) || len(directory) > 255 || directory == "." || directory == ".." || strings.ContainsAny(directory, "/\\") || strings.HasPrefix(directory, "-") || directory == ".axv" || strings.HasPrefix(directory, ".axv-") || strings.IndexFunc(directory, unicode.IsControl) >= 0 {
		return fmt.Errorf("choose one new directory name inside the selected volume folder")
	}
	name := in.Name
	if name == "" {
		name = directory
	}
	if !utf8.ValidString(name) || len(name) > 80 || strings.TrimSpace(name) != name || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Errorf("choose a project name of 1–80 UTF-8 bytes without surrounding spaces or control characters")
	}
	return ValidateRepositoryURL(in.Repository)
}
