package githubreview

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

const (
	gitHubDeviceURL = "https://github.com/login/device"
	codexDeviceURL  = "https://auth.openai.com/codex/device"
)

var (
	errCodexLoginRequired = errors.New("Connect a ChatGPT Codex account to continue.")
	colorSequence         = regexp.MustCompile("\\x1b\\[[0-?]*[ -/]*[@-~]")
	githubDeviceCode      = regexp.MustCompile(`(?im)^.*one-time code:\s*([A-Z0-9]{4}-[A-Z0-9]{4})[ \t]*\r?$\n`)
	codexDeviceCode       = regexp.MustCompile(`(?m)^[ \t]*([A-Z0-9]{4}-[A-Z0-9]{4,5})[ \t]*\r?$\n`)
)

type authAttempt struct {
	value  LoginAttempt
	cancel context.CancelFunc
}

type authProbe struct {
	at         time.Time
	connection Connection
}

// Auth coordinates app-initiated login with provider-owned CLI authentication.
// A login lives with the desktop service, independently of the initiating HTTP request.
type Auth struct {
	Context         context.Context
	GitHub          GitHubCLI
	CodexExecutable string
	ProxyURL        string
	WorkDir         string

	mu       sync.Mutex
	attempts map[string]*authAttempt
	probes   map[string]authProbe
	epochs   map[string]uint64
}

func (a *Auth) executable(provider string) (string, error) {
	switch provider {
	case "github":
		return a.GitHub.executable()
	case "codex":
		name := a.CodexExecutable
		if name == "" {
			name = "codex"
		}
		path, err := exec.LookPath(name)
		if err != nil {
			return "", errors.New("Codex CLI is unavailable on the AXV machine.")
		}
		return path, nil
	default:
		return "", errors.New("Unknown review account provider.")
	}
}

func (a *Auth) codexCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = a.WorkDir
	cmd.Env = cliEnvironment("codex", a.ProxyURL)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func (a *Auth) probe(ctx context.Context, provider string) (Connection, error) {
	path, err := a.executable(provider)
	if err != nil {
		return Connection{Error: err.Error()}, err
	}
	account, err := a.account(ctx, provider, path)
	if err != nil {
		return Connection{Available: true, Error: err.Error()}, err
	}
	return Connection{Available: true, Connected: true, Account: account}, nil
}

func (a *Auth) account(ctx context.Context, provider, path string) (string, error) {
	if provider == "github" {
		return a.GitHub.Account(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := a.codexCommand(ctx, path, "-c", codexCredentialStore, "login", "status")
	output := limitedbuffer.Buffer{Limit: 8 << 10}
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	switch {
	case err == nil && containsLine(string(output.Data), "Logged in using ChatGPT"):
		return "ChatGPT", nil
	case ctx.Err() != nil:
		return "", ctx.Err()
	case err != nil && containsLine(string(output.Data), "Not logged in"):
		return "", errCodexLoginRequired
	case err == nil:
		return "", errors.New("Codex is using a different sign-in method. Sign in with ChatGPT on the AXV machine to use subscription usage.")
	default:
		return "", errors.New("Could not check Codex login status on the AXV machine.")
	}
}

func containsLine(output, expected string) bool {
	for _, line := range strings.Split(colorSequence.ReplaceAllString(output, ""), "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}

func (a *Auth) Inspect(ctx context.Context, provider string) Connection {
	a.mu.Lock()
	attempt := a.attempts[provider]
	epoch := a.epochs[provider]
	if attempt != nil && attempt.value.Status == "pending" {
		connection := a.withAttemptLocked(provider, Connection{Available: true})
		a.mu.Unlock()
		return connection
	}
	if probe, ok := a.probes[provider]; ok && time.Since(probe.at) < 10*time.Second {
		connection := a.withAttemptLocked(provider, probe.connection)
		a.mu.Unlock()
		return connection
	}
	a.mu.Unlock()

	connection, _ := a.probe(ctx, provider)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.epochs[provider] == epoch && ctx.Err() == nil {
		a.setProbeLocked(provider, connection)
	} else if latest := a.attempts[provider]; latest != nil && latest.value.Status == "pending" {
		connection = Connection{Available: true}
	}
	return a.withAttemptLocked(provider, connection)
}

func (a *Auth) withAttemptLocked(provider string, connection Connection) Connection {
	if attempt := a.attempts[provider]; attempt != nil {
		copy := attempt.value
		connection.Attempt = &copy
	}
	return connection
}

func (a *Auth) setProbeLocked(provider string, connection Connection) {
	if a.probes == nil {
		a.probes = make(map[string]authProbe)
	}
	a.probes[provider] = authProbe{at: time.Now(), connection: connection}
}

func (a *Auth) Connect(ctx context.Context, provider string) error {
	a.mu.Lock()
	if attempt := a.attempts[provider]; attempt != nil && attempt.value.Status == "pending" {
		a.mu.Unlock()
		return nil
	}
	epoch := a.epochs[provider]
	a.mu.Unlock()
	connection, err := a.probe(ctx, provider)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.epochs[provider] != epoch {
		return nil
	}
	a.advanceLocked(provider)
	if a.attempts == nil {
		a.attempts = make(map[string]*authAttempt)
	}
	if err == nil {
		a.attempts[provider] = &authAttempt{value: LoginAttempt{Status: "complete"}}
		a.setProbeLocked(provider, connection)
		return nil
	}
	if !errors.Is(err, ErrGitHubLoginRequired) && !errors.Is(err, errCodexLoginRequired) {
		a.setProbeLocked(provider, connection)
		return err
	}
	serviceContext := a.Context
	if serviceContext == nil {
		serviceContext = context.WithoutCancel(ctx)
	}
	loginCtx, cancel := context.WithTimeout(serviceContext, 16*time.Minute)
	attempt := &authAttempt{value: LoginAttempt{Status: "pending"}, cancel: cancel}
	a.attempts[provider] = attempt
	go a.deviceLogin(loginCtx, provider, attempt)
	return nil
}

func (a *Auth) advanceLocked(provider string) {
	if a.epochs == nil {
		a.epochs = make(map[string]uint64)
	}
	a.epochs[provider]++
}

func (a *Auth) Stop(provider string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.advanceLocked(provider)
	if attempt := a.attempts[provider]; attempt != nil && attempt.cancel != nil {
		attempt.cancel()
	}
	delete(a.attempts, provider)
	delete(a.probes, provider)
}

func (a *Auth) deviceCommand(ctx context.Context, provider string) (*exec.Cmd, error) {
	if provider == "github" {
		return a.GitHub.command(ctx, "", "auth", "login", "--hostname", githubHost, "--web", "--skip-ssh-key", "--clipboard=false")
	}
	path, err := a.executable(provider)
	if err != nil {
		return nil, err
	}
	return a.codexCommand(ctx, path, "-c", codexCredentialStore, "login", "--device-auth"), nil
}

func (a *Auth) deviceLogin(ctx context.Context, provider string, attempt *authAttempt) {
	defer attempt.cancel()
	cmd, err := a.deviceCommand(ctx, provider)
	if err != nil {
		a.finishLogin(provider, attempt, Connection{}, loginFailure(provider))
		return
	}
	deadline := time.AfterFunc(40*time.Second, attempt.cancel)
	defer deadline.Stop()
	transcript := &deviceTranscript{provider: provider, onPrompt: func(url, code string) {
		deadline.Stop()
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.attempts[provider] == attempt {
			attempt.value.URL, attempt.value.Code = url, code
		}
	}}
	cmd.Stdout, cmd.Stderr = transcript, transcript
	if err := cmd.Run(); err != nil {
		a.finishLogin(provider, attempt, Connection{Available: true}, loginFailure(provider))
		return
	}
	deadline.Stop()
	connection, err := a.probe(ctx, provider)
	a.finishLogin(provider, attempt, connection, err)
}

func loginFailure(provider string) error {
	if provider == "github" {
		return errors.New("GitHub device login failed or expired. Try connecting again.")
	}
	return errors.New("Codex device login failed or expired. Check that device-code login is enabled in ChatGPT, then try again.")
}

func (a *Auth) finishLogin(provider string, attempt *authAttempt, connection Connection, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.attempts[provider] != attempt {
		return
	}
	a.advanceLocked(provider)
	attempt.value = LoginAttempt{Status: "complete"}
	if err != nil {
		attempt.value = LoginAttempt{Status: "failed", Error: err.Error()}
	}
	a.setProbeLocked(provider, connection)
}

type deviceTranscript struct {
	provider string
	output   limitedbuffer.Buffer
	onPrompt func(url, code string)
}

func (p *deviceTranscript) Write(chunk []byte) (int, error) {
	if p.onPrompt == nil {
		return len(chunk), nil
	}
	p.output.Limit = 16 << 10
	n, _ := p.output.Write(chunk)
	if url, code := parseDevicePrompt(p.provider, string(p.output.Data)); url != "" {
		p.onPrompt(url, code)
		p.onPrompt = nil
		p.output = limitedbuffer.Buffer{}
	}
	return n, nil
}

func parseDevicePrompt(provider, output string) (string, string) {
	clean := colorSequence.ReplaceAllString(output, "")
	expectedURL, codePattern := gitHubDeviceURL, githubDeviceCode
	if provider == "codex" {
		expectedURL, codePattern = codexDeviceURL, codexDeviceCode
	}
	for _, line := range strings.Split(clean, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != expectedURL && trimmed != "Open this URL to continue in your web browser: "+expectedURL {
			continue
		}
		if matches := codePattern.FindStringSubmatch(clean); len(matches) == 2 {
			return expectedURL, matches[1]
		}
	}
	return "", ""
}
