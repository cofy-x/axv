package compute

import (
	"testing"
)

func TestProjectInitializationValidatesRepositoryAndDirectory(t *testing.T) {
	for _, repository := range []string{"", "https://github.com/cofy-x/axv.git", "ssh://git@github.com/cofy-x/axv.git", "git@github.com:cofy-x/axv.git"} {
		if err := ValidateRepositoryURL(repository); err != nil {
			t.Errorf("valid repository %q: %v", repository, err)
		}
	}
	for _, repository := range []string{"https://user:secret@host/repo", "https://token@host/repo", "ssh://git:secret@host/repo", "https://host/repo?token=secret", "https://host/repo#secret", "file:///projects/repo", "ext::sh command", "--upload-pack=command", "https://host/repo\ncommand"} {
		if err := ValidateRepositoryURL(repository); ErrorCode(err) != "invalid_request" {
			t.Errorf("unsafe repository %q: %v", repository, err)
		}
	}
	for _, directory := range []string{"", ".", "..", "nested/project", "../project", "/project", "-option", ".axv", ".axv-private", "path\\escape", "bad\nname"} {
		_, err := ProjectInitializeSpec(InitializeProjectRequest{RequestID: "initialize-original", Directory: directory})
		if ErrorCode(err) != "invalid_request" {
			t.Errorf("unsafe destination %q: %v", directory, err)
		}
	}
}

func TestProjectInitializationUsesOwnedTypedIntent(t *testing.T) {
	req := InitializeProjectRequest{RequestID: "initialize-original", Directory: "new-repository", Repository: "https://example.com/repository.git", Name: "New project"}
	spec, err := ProjectInitializeSpec(req)
	if err != nil || spec.Initialization == nil || len(spec.Argv) != 0 || spec.Directory != "" {
		t.Fatalf("typed intent: %+v %v", spec, err)
	}
	if spec.Initialization.Directory != req.Directory || spec.Initialization.Repository != req.Repository || spec.Initialization.Name != req.Name {
		t.Fatal("initialization intent lost submitted values")
	}
	if ErrorCode(ValidateTaskSpec(spec)) != "invalid_request" {
		t.Fatal("ordinary task validation accepted internal initialization intent")
	}
	if err = ValidateInitializationTaskSpec(spec); err != nil {
		t.Fatal(err)
	}
}
