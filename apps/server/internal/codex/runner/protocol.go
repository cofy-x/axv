package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"time"
)

const maxMessageBytes = 32 << 20

type wireMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type wireRead struct {
	raw []byte
	err error
}

func readMessages(ctx context.Context, reader io.Reader) <-chan wireRead {
	out := make(chan wireRead, 1)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)
		for scanner.Scan() {
			data := append([]byte(nil), scanner.Bytes()...)
			select {
			case out <- wireRead{raw: data}:
			case <-ctx.Done():
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case out <- wireRead{err: err}:
		case <-ctx.Done():
		}
	}()
	return out
}

func writeMessage(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return ErrProtocol
	}
	return writeRawMessage(writer, data)
}

func writeRawMessage(writer io.Writer, data []byte) error {
	if _, err := writer.Write(append(append([]byte(nil), data...), '\n')); err != nil {
		return ErrUnavailable
	}
	return nil
}

type ownedProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	done   chan struct{}
	err    error
	group  bool
}

func startProcess(command *exec.Cmd, stdin io.WriteCloser, stdout io.ReadCloser, group bool) (*ownedProcess, error) {
	if err := command.Start(); err != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		if stdout != nil {
			_ = stdout.Close()
		}
		return nil, ErrUnavailable
	}
	process := &ownedProcess{cmd: command, stdin: stdin, stdout: stdout, done: make(chan struct{}), group: group}
	go func() {
		process.err = command.Wait()
		close(process.done)
	}()
	return process, nil
}

func (process *ownedProcess) stop() {
	if process == nil {
		return
	}
	if process.stdin != nil {
		_ = process.stdin.Close()
	}
	select {
	case <-process.done:
	case <-time.After(300 * time.Millisecond):
		terminateProcess(process)
		select {
		case <-process.done:
		case <-time.After(time.Second):
			killProcess(process)
			<-process.done
		}
	}
	// A child may exit before its helper processes. Only the app-server has a
	// private process group; never signal the TUI's inherited foreground group.
	if process.group {
		killProcess(process)
	}
	if process.stdout != nil {
		_ = process.stdout.Close()
	}
}

func historyEnvironment(environment []string, home string) []string {
	out := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "CODEX_HOME=") {
			out = append(out, entry)
		}
	}
	return append(out, "CODEX_HOME="+home)
}

func startServer(cliPath, project, home string, environment []string) (*ownedProcess, error) {
	command := exec.Command(cliPath, "app-server", "--listen", "stdio://")
	command.Dir = project
	command.Env = historyEnvironment(environment, home)
	command.Stderr = io.Discard
	configureServerProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, ErrUnavailable
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, ErrUnavailable
	}
	return startProcess(command, stdin, stdout, true)
}

func responseThreadID(result json.RawMessage) string {
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &response) != nil {
		return ""
	}
	return response.Thread.ID
}

// VerifyHistory reads only the exact stored conversation's metadata through
// the official protocol. It does not list sessions or read transcript files.
func VerifyHistory(ctx context.Context, cliPath, project, home string, environment []string, threadID string) error {
	if !threadIDPattern.MatchString(threadID) {
		return ErrHistoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	process, err := startServer(cliPath, project, home, environment)
	if err != nil {
		return err
	}
	defer process.stop()
	reads := readMessages(ctx, process.stdout)
	call := func(id, method string, params any) (wireMessage, error) {
		var response wireMessage
		if writeMessage(process.stdin, map[string]any{"id": id, "method": method, "params": params}) != nil {
			return response, ErrUnavailable
		}
		for {
			select {
			case <-ctx.Done():
				return response, ErrUnavailable
			case read, ok := <-reads:
				if !ok || read.err != nil {
					return response, ErrUnavailable
				}
				if json.Unmarshal(read.raw, &response) != nil {
					return response, ErrProtocol
				}
				var responseID string
				if json.Unmarshal(response.ID, &responseID) == nil && responseID == id {
					return response, nil
				}
			}
		}
	}
	response, err := call("axv-initialize", "initialize", map[string]any{"clientInfo": map[string]string{"name": "axv", "version": "0.1.0"}})
	if err != nil {
		return err
	}
	if len(response.Error) != 0 || len(response.Result) == 0 {
		return ErrProtocol
	}
	if writeMessage(process.stdin, map[string]any{"method": "initialized", "params": map[string]any{}}) != nil {
		return ErrUnavailable
	}
	response, err = call("axv-read", "thread/read", map[string]any{"threadId": threadID, "includeTurns": false})
	if err != nil {
		return err
	}
	if len(response.Error) != 0 {
		return ErrHistoryUnavailable
	}
	if responseThreadID(response.Result) != threadID {
		return ErrProtocol
	}
	return nil
}
