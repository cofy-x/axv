//go:build linux || darwin

// Native Guest Terminal acceptance through the private compute host socket.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

func main() {
	if err := verifyTerminal(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Real Guest Terminal creation, input, resize, detach/replay, exact-shell reattachment and explicit End passed.")
}

func verifyTerminal() (result error) {
	if len(os.Args) != 4 {
		return errors.New("usage: verify-vm-terminal <host socket> <host ID> <machine ID>")
	}
	socket, host, machine := os.Args[1], os.Args[2], os.Args[3]
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 90*time.Second)
	defer timeout()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err != nil {
			return nil, err
		}
		if err := verifyTerminalHost(ctx, conn, host); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	return verifyTerminalProbe(ctx, client, host, machine)
}

// Verify each new transport connection before it can carry a terminal request.
func verifyTerminalHost(ctx context.Context, conn net.Conn, host string) error {
	deadline := time.Now().Add(5 * time.Second)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	defer conn.SetDeadline(time.Time{})
	request, err := http.NewRequestWithContext(ctx, "GET", "http://host/v1/runtime", nil)
	if err != nil {
		return err
	}
	if err = request.Write(conn); err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return err
	}
	var runtime struct {
		HostID          string
		Ready           bool
		ProtocolVersion int
	}
	if len(data) > 64<<10 || json.Unmarshal(data, &runtime) != nil || response.StatusCode != 200 || !runtime.Ready || runtime.HostID != host || runtime.ProtocolVersion != 3 || response.Close {
		return fmt.Errorf("the original terminal host binding is unavailable: status=%d runtime=%s", response.StatusCode, data)
	}
	return nil
}

func verifyTerminalProbe(ctx context.Context, client *http.Client, host, machine string) (result error) {
	base := "http://host/v1/machines/" + url.PathEscape(machine) + "/guest"
	request := func(ctx context.Context, method, path string, body, target any) error {
		var input io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				return err
			}
			input = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, input)
		if err != nil {
			return err
		}
		req.Header.Set("X-AXV-Host-ID", host)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
			return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, data)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
		if err != nil {
			return err
		}
		if len(data) > 64<<10 {
			return errors.New("guest probe response exceeded 64 KiB")
		}
		return json.Unmarshal(data, target)
	}
	var health struct{ MachineID, BootID string }
	if err := request(ctx, "GET", "/health", nil, &health); err != nil {
		return err
	}
	if health.MachineID != machine || health.BootID == "" {
		return fmt.Errorf("guest identity mismatch: %+v", health)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	marker := hex.EncodeToString(nonce[:])
	probeFile := "/projects/.axv-terminal-" + marker
	probeFileUsed := false

	type terminal struct{ ID, BootID, Status string }
	var original terminal
	creation := map[string]any{"requestId": "native-terminal-" + marker, "cols": 80, "rows": 24}
	owned := map[string]terminal{}
	remember := func(value terminal) {
		if regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(value.ID) && value.BootID == health.BootID {
			owned[value.ID] = value
		}
	}
	end := func(ctx context.Context, value terminal) error {
		var current struct{ MachineID, BootID string }
		if err := request(ctx, "GET", "/health", nil, &current); err != nil {
			return err
		}
		if current.MachineID != machine || current.BootID != value.BootID {
			return fmt.Errorf("guest boot changed; refusing to redirect End for terminal %s", value.ID)
		}
		var stopped terminal
		if err := request(ctx, "DELETE", "/terminals/"+url.PathEscape(value.ID), nil, &stopped); err != nil {
			return err
		}
		if stopped.ID != value.ID || stopped.BootID != value.BootID || stopped.Status != "ended" {
			return fmt.Errorf("End did not stop the exact owned terminal: %+v", stopped)
		}
		delete(owned, value.ID)
		return nil
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, value := range owned {
			if err := end(cleanup, value); err != nil {
				result = errors.Join(result, fmt.Errorf("end owned terminal %s: %w", value.ID, err))
			}
		}
		if probeFileUsed {
			var output struct {
				ExitCode int
				Stderr   string
			}
			if err := request(cleanup, "POST", "/exec", map[string]any{"argv": []string{"rm", "-f", "--", probeFile}, "timeoutSeconds": 5}, &output); err != nil {
				result = errors.Join(result, fmt.Errorf("remove owned detached-output probe file: %w", err))
			} else if output.ExitCode != 0 {
				result = errors.Join(result, fmt.Errorf("remove owned detached-output probe file: %s", output.Stderr))
			}
		}
	}()
	if err := request(ctx, "POST", "/terminals", creation, &original); err != nil {
		// Replaying after a lost response could create a different shell if the
		// guest service restarted within the same Linux boot. Preserve the exact
		// request for diagnosis; the parent verifier owns cleanup of this VM.
		return fmt.Errorf("terminal creation request %s has an unconfirmed outcome; inspect the exact VM instead of creating another terminal: %w", creation["requestId"], err)
	}
	remember(original)
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(original.ID) || original.BootID != health.BootID || original.Status != "running" {
		return fmt.Errorf("terminal creation identity mismatch: %+v", original)
	}
	path := "/terminals/" + url.PathEscape(original.ID)
	var replayed terminal
	if err := request(ctx, "POST", "/terminals", creation, &replayed); err != nil {
		return err
	}
	remember(replayed)
	if replayed != original {
		return errors.New("creation retry replaced the original terminal")
	}
	attach := func() (*websocket.Conn, error) {
		address := strings.Replace(base+path+"/attach", "http:", "ws:", 1) + "?" + url.Values{"hostId": {host}, "bootId": {original.BootID}}.Encode()
		deadline := time.Now().Add(5 * time.Second)
		for {
			conn, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"X-AXV-Host-ID": {host}}})
			if err == nil {
				kind, data, readErr := conn.Read(ctx)
				var ready struct {
					Ready  bool
					BootID string
				}
				if readErr != nil || kind != websocket.MessageText || json.Unmarshal(data, &ready) != nil || !ready.Ready || ready.BootID != original.BootID {
					conn.CloseNow()
					return nil, fmt.Errorf("terminal attachment omitted matching ready identity: %s, %v", data, readErr)
				}
				return conn, nil
			}
			if response == nil || response.StatusCode != http.StatusConflict || time.Now().After(deadline) {
				return nil, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	readUntil := func(conn *websocket.Conn, markers ...string) error {
		readCtx, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		var output strings.Builder
		for output.Len() < 1<<20 {
			kind, data, err := conn.Read(readCtx)
			if err != nil {
				return fmt.Errorf("terminal output missing %q: %w; tail=%q", markers, err, output.String())
			}
			if kind == websocket.MessageBinary {
				output.Write(data)
				matched := true
				for _, marker := range markers {
					matched = matched && strings.Contains(output.String(), marker)
				}
				if matched {
					return nil
				}
			}
		}
		return errors.New("terminal probe exceeded its output bound")
	}
	conn, err := attach()
	if err != nil {
		return err
	}
	defer func() {
		if conn != nil {
			conn.CloseNow()
		}
	}()
	if err = conn.Write(ctx, websocket.MessageBinary, []byte("stty -echo; axv_probe="+marker+"; printf 'AXV_%s\\n' INITIAL; stty size\r")); err != nil {
		return err
	}
	if err = readUntil(conn, "AXV_INITIAL", "24 80"); err != nil {
		return err
	}
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"resize":[113,41]}`)); err != nil {
		return err
	}
	if err = conn.Write(ctx, websocket.MessageBinary, []byte("stty size\r")); err != nil {
		return err
	}
	if err = readUntil(conn, "41 113"); err != nil {
		return err
	}
	probeFileUsed = true
	if err = conn.Write(ctx, websocket.MessageBinary, []byte("printf 'AXV_%s\\n' PRESERVED; sleep 1; printf 'AXV_%s\\n' DETACHED; printf '%s' '"+marker+"' > '"+probeFile+"'\r")); err != nil {
		return err
	}
	if err = readUntil(conn, "AXV_PRESERVED"); err != nil {
		return err
	}
	conn.CloseNow()
	// Wait for an independent proof that the detached shell produced its output.
	// A fixed sleep could otherwise pass by receiving DETACHED as live output.
	var detached struct {
		ExitCode       int
		Stdout, Stderr string
	}
	check := "import pathlib,time\np=pathlib.Path('" + probeFile + "')\nend=time.monotonic()+5\nwhile not p.exists() and time.monotonic()<end: time.sleep(0.05)\nprint(p.read_text())"
	if err = request(ctx, "POST", "/exec", map[string]any{"argv": []string{"python3", "-c", check}, "timeoutSeconds": 8}, &detached); err != nil {
		return err
	}
	if detached.ExitCode != 0 || strings.TrimSpace(detached.Stdout) != marker {
		return fmt.Errorf("detached shell did not produce its replay before reattachment: %+v", detached)
	}
	var existing terminal
	if err = request(ctx, "GET", path, nil, &existing); err != nil {
		return err
	}
	if existing != original {
		return errors.New("disconnect ended or replaced the original shell")
	}
	conn, err = attach()
	if err != nil {
		return err
	}
	if err = readUntil(conn, "AXV_PRESERVED", "AXV_DETACHED"); err != nil {
		return err
	}
	if err = conn.Write(ctx, websocket.MessageBinary, []byte("printf 'AXV_REATTACH:%s\\n' \"$axv_probe\"\r")); err != nil {
		return err
	}
	if err = readUntil(conn, "AXV_REATTACH:"+marker); err != nil {
		return err
	}
	if err = end(ctx, original); err != nil {
		return err
	}
	endCtx, done := context.WithTimeout(ctx, 10*time.Second)
	defer done()
	for {
		kind, data, err := conn.Read(endCtx)
		if err != nil {
			return fmt.Errorf("explicit End omitted its terminal stream event: %w", err)
		}
		if kind != websocket.MessageText {
			continue
		}
		var event struct{ Ended bool }
		if json.Unmarshal(data, &event) != nil || !event.Ended {
			return fmt.Errorf("unexpected terminal End event: %s", data)
		}
		_, _, err = conn.Read(endCtx)
		if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
			return fmt.Errorf("explicit End did not close its attachment normally: %w", err)
		}
		break
	}
	return nil
}
