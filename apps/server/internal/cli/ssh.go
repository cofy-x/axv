package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/guest/sshkey"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/sshstream"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

type sshFiles struct{ Identity, KnownHosts, ClientID, PublicKey string }

func newSSH(configOnly bool) *cobra.Command {
	var start, noCommand, noTTY bool
	var tty int
	var local, remote, dynamic []string
	var timeout time.Duration
	name, short, usage := "ssh", "Connect with standard SSH", " <name|id> [-- command...]"
	if configOnly {
		name, short = "ssh-config", "Print independent OpenSSH configuration for ssh, scp and sftp"
		usage = " <name|id>"
	}
	cmd := &cobra.Command{Use: name + usage, Short: short, Args: arguments(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if configOnly && len(args) != 1 {
			return usageError(fmt.Errorf("ssh-config accepts one VM reference"))
		}
		if timeout <= 0 {
			return usageError(fmt.Errorf("--timeout must be positive"))
		}
		if tty > 0 && noTTY {
			return usageError(fmt.Errorf("choose -t or -T"))
		}
		if noCommand && len(args) > 1 {
			return usageError(fmt.Errorf("-N does not accept a remote command"))
		}
		if !configOnly {
			if _, err := exec.LookPath("ssh"); err != nil {
				return fmt.Errorf("install the system OpenSSH client: %w", err)
			}
		}
		home, err := profile.Home()
		if err != nil {
			return err
		}
		prepareCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		c, err := connectVM(prepareCtx, home, cmd.ErrOrStderr(), computeclient.Binding{})
		if err != nil {
			return err
		}
		defer c.Close()
		m, err := c.Resolve(prepareCtx, strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}
		if m.Status != "running" || m.Desired != "running" {
			if !start {
				return fmt.Errorf("VM %q is not running; run axv vm start or use --start", m.Spec.Name)
			}
			request := compute.ActionRequest{RequestID: compute.NewID("cli"), Revision: m.Revision, Action: "start"}
			mutationReceipt(cmd, c, request.RequestID, m.ID)
			result, err := c.Action(prepareCtx, m.ID, request)
			if err != nil {
				return submissionFailure(c, request.RequestID, err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Machine: %s  Operation: %s\n", m.ID, result.Operation.ID)
			if _, err = waitOperation(prepareCtx, c, result.Operation.ID, false, cmd.ErrOrStderr()); err != nil {
				return mutationFailure(c, request.RequestID, err)
			}
		}
		info, files, err := prepareSSH(prepareCtx, c, home, m.ID, "")
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		executable, err = profile.PinExecutable(home, executable)
		if err != nil {
			return err
		}
		boot := info.BootID
		if configOnly {
			boot = ""
		}
		config, alias, err := sshConfiguration(home, executable, c.Binding, m.ID, boot, files)
		if err != nil {
			return err
		}
		if configOnly {
			_, err = io.WriteString(cmd.OutOrStdout(), config)
			return err
		}
		file, err := os.CreateTemp(filepath.Join(home, "ssh"), "connection-*.conf")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		if _, err = file.WriteString(config); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		sshArgs := []string{"-F", file.Name()}
		if noCommand {
			sshArgs = append(sshArgs, "-N")
		}
		if noTTY {
			sshArgs = append(sshArgs, "-T")
		}
		for i := 0; i < tty; i++ {
			sshArgs = append(sshArgs, "-t")
		}
		for _, entry := range local {
			sshArgs = append(sshArgs, "-L", entry)
		}
		for _, entry := range remote {
			sshArgs = append(sshArgs, "-R", entry)
		}
		for _, entry := range dynamic {
			sshArgs = append(sshArgs, "-D", entry)
		}
		sshArgs = append(sshArgs, alias)
		sshArgs = append(sshArgs, args[1:]...)
		process := exec.CommandContext(cmd.Context(), "ssh", sshArgs...)
		process.Stdin, process.Stdout, process.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		return sshResult(cmd.Context(), process.Run())
	}}
	f := cmd.Flags()
	f.BoolVar(&start, "start", false, "explicitly start a stopped VM before connecting")
	f.DurationVar(&timeout, "timeout", 10*time.Minute, "maximum preparation/start wait; established SSH has no duration limit")
	if !configOnly {
		f.BoolVarP(&noCommand, "no-command", "N", false, "forward ports without a remote command")
		f.BoolVarP(&noTTY, "no-tty", "T", false, "disable pseudo-terminal allocation")
		f.CountVarP(&tty, "tty", "t", "request a pseudo-terminal; repeat to force it")
		f.StringArrayVarP(&local, "local-forward", "L", nil, "local TCP forwarding, as in ssh -L")
		f.StringArrayVarP(&remote, "remote-forward", "R", nil, "guest-loopback TCP forwarding, as in ssh -R")
		f.StringArrayVarP(&dynamic, "dynamic-forward", "D", nil, "local SOCKS forwarding, as in ssh -D")
	}
	return cmd
}

func sshResult(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return &exitError{code: 130}
	}
	var status *exec.ExitError
	if errors.As(err, &status) {
		if status.ExitCode() < 0 {
			return fmt.Errorf("system SSH process terminated: %w", err)
		}
		return &exitError{code: status.ExitCode()}
	}
	return err
}

func prepareSSH(ctx context.Context, c *computeclient.Client, home, id, expectedBoot string) (guestproto.SSHInfo, sshFiles, error) {
	var info guestproto.SSHInfo
	var files sshFiles
	if err := c.DoJSON(ctx, "GET", "machines/"+id+"/ssh", nil, &info); err != nil {
		return info, files, err
	}
	if info.Version != guestproto.Version || info.HostID != c.Binding.HostID || info.MachineID != id || info.BootID == "" || info.User != "axv" {
		return info, files, fmt.Errorf("guest SSH identity or protocol does not match the selected VM")
	}
	if !info.Available {
		return info, files, fmt.Errorf("guest SSH is unavailable: %s", info.Reason)
	}
	if expectedBoot != "" && expectedBoot != info.BootID {
		return info, files, fmt.Errorf("the VM boot changed before SSH attached; reconnect explicitly")
	}
	var err error
	files, err = profileSSH(ctx, home, sshAlias(c.Binding.HostID, id), info.HostKey)
	if err != nil {
		return info, files, err
	}
	request := guestproto.SSHClientRequest{BootID: info.BootID, ClientID: files.ClientID, PublicKey: files.PublicKey}
	var client guestproto.SSHClient
	if err = c.DoJSON(ctx, "POST", "machines/"+id+"/ssh/clients", request, &client); err != nil {
		return info, files, err
	}
	if client.BootID != request.BootID || client.ClientID != request.ClientID || client.PublicKey != request.PublicKey {
		return info, files, fmt.Errorf("guest did not confirm the exact SSH client registration")
	}
	return info, files, nil
}

func profileSSH(ctx context.Context, home, alias, hostKey string) (sshFiles, error) {
	dir := filepath.Join(home, "ssh")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return sshFiles{}, err
	}
	if err := privatePath(dir, true); err != nil {
		return sshFiles{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var lock *os.File
	for {
		var err error
		lock, err = filelock.TryLock(filepath.Join(dir, "credentials.lock"))
		if err == nil {
			break
		}
		if os.IsPermission(err) || os.IsNotExist(err) {
			return sshFiles{}, err
		}
		select {
		case <-ctx.Done():
			return sshFiles{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer lock.Close()
	files := sshFiles{Identity: filepath.Join(dir, "client_ed25519"), KnownHosts: filepath.Join(dir, "known_hosts"), ClientID: profile.Identity(home)}
	if err := privatePath(files.Identity, false); os.IsNotExist(err) {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return files, err
		}
		block, err := ssh.MarshalPrivateKey(key, "AXV profile client")
		if err != nil {
			return files, err
		}
		if err = atomicfile.Write(files.Identity, pem.EncodeToMemory(block), 0600); err != nil {
			return files, err
		}
	} else if err != nil {
		return files, err
	}
	data, err := os.ReadFile(files.Identity)
	if err != nil {
		return files, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return files, fmt.Errorf("read profile SSH key: %w", err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return files, fmt.Errorf("profile SSH key must be Ed25519")
	}
	files.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	canonical, err := sshkey.Normalize(hostKey)
	if err != nil {
		return files, fmt.Errorf("invalid guest SSH host key: %w", err)
	}
	if err = privatePath(files.KnownHosts, false); err != nil && !os.IsNotExist(err) {
		return files, err
	}
	known, err := os.ReadFile(files.KnownHosts)
	if err != nil && !os.IsNotExist(err) {
		return files, err
	}
	for _, line := range strings.Split(string(known), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) != 3 {
			return files, fmt.Errorf("invalid AXV known-hosts file; preserve it and repair it explicitly")
		}
		if fields[0] == alias {
			if fields[1]+" "+fields[2] != canonical {
				return files, fmt.Errorf("SSH host key changed for %s; verify the VM and explicitly repair %s before connecting", alias, files.KnownHosts)
			}
			return files, nil
		}
	}
	if len(known) > 0 && known[len(known)-1] != '\n' {
		known = append(known, '\n')
	}
	known = append(known, []byte(alias+" "+canonical+"\n")...)
	if err = atomicfile.Write(files.KnownHosts, known, 0600); err != nil {
		return files, err
	}
	return files, nil
}

func privatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("SSH path must be a regular owned %s: %s", map[bool]string{true: "directory", false: "file"}[directory], path)
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("SSH path has unsafe permissions; restrict access to its owner: %s", path)
	}
	return nil
}

func sshAlias(host, id string) string {
	digest := sha256.Sum256([]byte(host + "\x00" + id))
	return "axv-" + hex.EncodeToString(digest[:])
}

func sshConfiguration(home, executable string, binding computeclient.Binding, id, boot string, files sshFiles) (string, string, error) {
	args := []string{executable, "_ssh-proxy", "--profile", home, "--host-id", binding.HostID, "--fingerprint", binding.ConfigFingerprint, "--machine-id", id}
	if boot != "" {
		args = append(args, "--boot-id", boot)
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return "", "", fmt.Errorf("SSH configuration paths and identities cannot contain line breaks or NUL")
		}
		quoted[i] = "'" + strings.ReplaceAll(strings.ReplaceAll(arg, "'", "'\"'\"'"), "%", "%%") + "'"
	}
	quotePath := func(path string) (string, error) {
		if strings.ContainsAny(path, "\x00\r\n") {
			return "", fmt.Errorf("SSH file path cannot contain line breaks or NUL")
		}
		if strings.Contains(path, "${") {
			return "", fmt.Errorf("SSH file path cannot contain OpenSSH environment expansion syntax '${': %s", path)
		}
		path = strings.ReplaceAll(path, "\\", "\\\\")
		path = strings.ReplaceAll(path, "\"", "\\\"")
		path = strings.ReplaceAll(path, "%", "%%")
		return "\"" + path + "\"", nil
	}
	identity, err := quotePath(files.Identity)
	if err != nil {
		return "", "", err
	}
	known, err := quotePath(files.KnownHosts)
	if err != nil {
		return "", "", err
	}
	alias := sshAlias(binding.HostID, id)
	config := fmt.Sprintf("Host %s\n  HostName %s\n  User axv\n  ProxyCommand %s\n  IdentityFile %s\n  IdentityAgent none\n  IdentitiesOnly yes\n  HostKeyAlias %s\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile /dev/null\n  StrictHostKeyChecking yes\n  CheckHostIP no\n  HostKeyAlgorithms ssh-ed25519\n  PubkeyAcceptedAlgorithms ssh-ed25519\n  PreferredAuthentications publickey\n  PasswordAuthentication no\n  KbdInteractiveAuthentication no\n  ForwardAgent no\n  ForwardX11 no\n  ControlMaster no\n  ControlPath none\n  ExitOnForwardFailure yes\n  ConnectTimeout 30\n", alias, alias, strings.Join(quoted, " "), identity, alias, known)
	return config, alias, nil
}

func newSSHProxy() *cobra.Command {
	var home, id, boot string
	var binding computeclient.Binding
	cmd := &cobra.Command{Use: "_ssh-proxy", Hidden: true, Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if !filepath.IsAbs(home) || !compute.IsMachineID(id) || binding.HostID == "" || binding.ConfigFingerprint == "" {
			return usageError(fmt.Errorf("invalid private SSH proxy identity"))
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		c, err := connectVM(ctx, home, cmd.ErrOrStderr(), binding)
		if err != nil {
			return err
		}
		defer c.Close()
		info, _, err := prepareSSH(ctx, c, home, id, boot)
		if err != nil {
			return err
		}
		conn, err := c.Dial(ctx)
		if err != nil {
			return err
		}
		request, _ := http.NewRequest("GET", "http://host/v1/machines/"+id+"/ssh/attach?bootId="+url.QueryEscape(info.BootID), nil)
		request.Header.Set("X-AXV-Host-ID", binding.HostID)
		stream, err := sshstream.Upgrade(ctx, conn, request)
		if err != nil {
			conn.Close()
			return err
		}
		defer stream.Close()
		return sshstream.Pump(cmd.Context(), stream, cmd.InOrStdin(), cmd.OutOrStdout())
	}}
	f := cmd.Flags()
	f.StringVar(&home, "profile", "", "fixed profile directory")
	f.StringVar(&id, "machine-id", "", "fixed machine ID")
	f.StringVar(&boot, "boot-id", "", "expected VM boot")
	f.StringVar(&binding.HostID, "host-id", "", "verified host ID")
	f.StringVar(&binding.ConfigFingerprint, "fingerprint", "", "verified host configuration")
	return cmd
}
