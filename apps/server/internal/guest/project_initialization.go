package guest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

// createInitializationRoot runs only for durable, typed guest task intent. It
// publishes the filesystem identity before a clone process can be launched.
func (t *projectTasks) createInitializationRoot(record *guestTaskRecord, parent *os.Root, parentInfo os.FileInfo) (*protocol.ProjectFileRoot, error) {
	intent := record.Spec.Initialization
	if err := t.service.verifyProjectFiles(record.ProjectFileScope, parentInfo); err != nil {
		return nil, err
	}
	if err := parent.Mkdir(intent.Directory, 0700); err != nil {
		return nil, fmt.Errorf("the destination already exists or cannot be created; existing files were preserved: %w", err)
	}
	child, info, err := projectDirectory(parent, intent.Directory)
	if err != nil {
		return nil, err
	}
	defer child.Close()
	identity, err := projectFileRootIdentity(child, info)
	if err != nil {
		return nil, err
	}
	for _, root := range []*os.Root{child, parent} {
		f, err := root.Open(".")
		if err != nil {
			return nil, err
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return nil, err
		}
	}
	return &protocol.ProjectFileRoot{Root: path.Join(record.Root, intent.Directory), RootIdentity: identity}, nil
}

func (s *service) verifyInitializedRoot(record *guestTaskRecord) error {
	created := record.CreatedRoot
	if created == nil {
		return errors.New("the initialized directory has no durable identity receipt")
	}
	root, _, _, err := s.openProjectFiles(protocol.ProjectFileScope{Root: created.Root, RootIdentity: created.RootIdentity, BootID: record.BootID, InstanceID: record.InstanceID}, false)
	if root != nil {
		root.Close()
	}
	return err
}

func initializationCommand(intent *protocol.ProjectInitialization) ([]string, []string) {
	if intent.Repository == "" {
		return []string{"/bin/true"}, nil
	}
	argv := []string{"git", "-c", "core.hooksPath=/dev/null", "-c", "core.worktree=.", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always", "clone", "--", intent.Repository, "."}
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GIT_DIR" || key == "GIT_WORK_TREE" || key == "GIT_COMMON_DIR" || key == "GIT_INDEX_FILE" || key == "GIT_OBJECT_DIRECTORY" || key == "GIT_ALTERNATE_OBJECT_DIRECTORIES" || strings.HasPrefix(key, "GIT_CONFIG_") || key == "GIT_TERMINAL_PROMPT" || key == "GCM_INTERACTIVE" || key == "GIT_SSH_COMMAND" {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes")
	return argv, environment
}

func configureInitializationCommand(cmd *exec.Cmd, intent *protocol.ProjectInitialization) {
	_, environment := initializationCommand(intent)
	if environment != nil {
		cmd.Env = environment
	}
	// Git diagnostics can contain credentials returned by a remote server.
	// The durable typed outcome and a bounded safe reason replace raw output.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
}

func validateInitializationResult(record *guestTaskRecord) error {
	created := record.CreatedRoot
	if created == nil {
		if record.Spec.Initialization != nil && record.Status == "succeeded" {
			return errors.New("successful initialization has no created-root receipt")
		}
		return nil
	}
	if record.Spec.Initialization == nil || created.Root != path.Join(record.Root, record.Spec.Initialization.Directory) || created.RootIdentity == "" || len(created.RootIdentity) > 128 || !utf8.ValidString(created.RootIdentity) || strings.IndexFunc(created.RootIdentity, unicode.IsControl) >= 0 {
		return errors.New("guest initialization root receipt is invalid; refusing to adopt it")
	}
	return nil
}
