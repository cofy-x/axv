package tailnet

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
	"tailscale.com/ipn/ipnstate"
)

const stateOwner = "axv-tailnet-v1\n"
const bindingName = "binding.json"

var validHostname = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type binding struct {
	Version int    `json:"version"`
	UserID  string `json:"ownerUserId,omitempty"`
	NodeID  string `json:"nodeId,omitempty"`
}

type stateStore struct {
	directory string
	identity  binding
}

func prepareState(options Options) (_ *stateStore, _ *os.File, result error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, nil, errors.New("AXV embedded Tailscale currently requires macOS or Linux")
	}
	if !filepath.IsAbs(options.StateDir) || filepath.Clean(options.StateDir) != options.StateDir || !validHostname.MatchString(options.Hostname) {
		return nil, nil, errors.New("tailnet requires an absolute clean state directory and a lowercase hostname of 1–63 characters")
	}
	if options.OwnerUserID != "" && !canonicalUserID(options.OwnerUserID) {
		return nil, nil, errors.New("tailnet owner user ID must be a canonical positive decimal ID")
	}
	for _, key := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TS_ID_TOKEN", "TS_AUDIENCE", "TSNET_FORCE_LOGIN"} {
		if os.Getenv(key) != "" {
			return nil, nil, errors.New("remove inherited Tailscale enrollment credentials before enabling AXV remote access")
		}
	}
	if err := os.MkdirAll(options.StateDir, 0700); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(options.StateDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !ownedFile(info) {
		return nil, nil, errors.New("tailnet state must be a private current-user-owned directory with mode 0700")
	}
	resolved, err := filepath.EvalSymlinks(options.StateDir)
	if err != nil || resolved != options.StateDir {
		return nil, nil, errors.New("tailnet state paths must not contain symlinks")
	}
	entries, err := os.ReadDir(options.StateDir)
	if err != nil {
		return nil, nil, err
	}
	initial := len(entries) == 0
	if !initial {
		if err := validateStateTree(options.StateDir); err != nil {
			return nil, nil, err
		}
		if _, err := readPrivate(options.StateDir, ".axv-owner"); err != nil {
			return nil, nil, err
		}
	}
	if err := owneddir.Ensure(options.StateDir, stateOwner); err != nil {
		return nil, nil, err
	}
	lockPath := filepath.Join(options.StateDir, ".listener.lock")
	if _, err := os.Lstat(lockPath); err == nil {
		if _, err := readPrivate(options.StateDir, ".listener.lock"); err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	lock, err := filelock.TryLock(lockPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if result != nil {
			_ = lock.Close()
		}
	}()
	store := &stateStore{directory: options.StateDir, identity: binding{Version: 1}}
	if initial {
		file, err := os.OpenFile(filepath.Join(options.StateDir, bindingName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, nil, err
		}
		writeErr := json.NewEncoder(file).Encode(store.identity)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return nil, nil, err
		}
	} else {
		data, err := readPrivate(options.StateDir, bindingName)
		if err != nil {
			return nil, nil, errors.New("the recorded tailnet enrollment is missing or invalid; it was not reset")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&store.identity); err != nil {
			return nil, nil, errors.New("the recorded tailnet enrollment is invalid; it was not reset")
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF || store.identity.Version != 1 || (store.identity.UserID == "") != (store.identity.NodeID == "") || store.identity.UserID != "" && (!canonicalUserID(store.identity.UserID) || !presentNodeID(store.identity.NodeID)) {
			return nil, nil, errors.New("the recorded tailnet enrollment is invalid; it was not reset")
		}
	}
	if options.OwnerUserID != "" && store.identity.UserID != "" && options.OwnerUserID != store.identity.UserID {
		return nil, nil, errors.New("the configured Tailscale owner differs from this profile's recorded enrollment")
	}
	return store, lock, nil
}

func (s *stateStore) bind(status *ipnstate.Status, configuredOwner string) error {
	if status == nil || status.Self == nil || status.Self.IsTagged() || status.Self.UserID <= 0 || !presentNodeID(string(status.Self.ID)) || status.BackendState != "Running" {
		return errors.New("AXV remote access requires an authenticated, untagged workstation owner")
	}
	identity := binding{Version: 1, UserID: strconv.FormatInt(int64(status.Self.UserID), 10), NodeID: string(status.Self.ID)}
	if configuredOwner != "" && configuredOwner != identity.UserID {
		return errors.New("the enrolled Tailscale account does not match the configured owner user ID")
	}
	if s.identity.UserID != "" {
		return verifyIdentity(status, s.identity)
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(s.directory, bindingName), append(data, '\n'), 0600); err != nil {
		return errors.New("the Tailscale owner binding could not be saved; remote access remains closed")
	}
	s.identity = identity
	return nil
}

func readPrivate(directory, name string) ([]byte, error) {
	info, err := os.Lstat(filepath.Join(directory, name))
	if err != nil || !privateFile(info) {
		return nil, errors.New("tailnet metadata must be a private current-user-owned regular file without links")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !privateFile(opened) || !os.SameFile(info, opened) {
		return nil, errors.New("tailnet metadata changed while opening it")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return nil, errors.New("tailnet metadata exceeds its size limit")
	}
	return data, nil
}

func canonicalUserID(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}

func presentNodeID(value string) bool {
	return value != "" && value != "0" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func privateFile(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Perm() == 0600 && ownedFile(info) && singleLink(info)
}

func validateStateTree(directory string) error {
	return filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() && info.Mode().Perm() == 0700 && ownedFile(info) {
			return nil
		}
		if privateFile(info) {
			return nil
		}
		// Tailscale's certificate store writes public certs as 0644 inside its
		// private certs directory; private keys and metadata remain 0600.
		relative, relErr := filepath.Rel(directory, path)
		if relErr == nil && filepath.Dir(relative) == "certs" && strings.HasSuffix(entry.Name(), ".ts.net.crt") &&
			info.Mode().IsRegular() && info.Mode().Perm() == 0644 && ownedFile(info) && singleLink(info) {
			return nil
		}
		return errors.New("tailnet state contains non-private, linked or foreign-owned resources; it was not adopted")
	})
}
