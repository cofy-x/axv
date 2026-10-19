package hosttailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHostCommandHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HOST_TAILSCALE_HELPER") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || index+1 == len(os.Args) {
		os.Exit(2)
	}
	switch os.Args[index+1] {
	case "status":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"TS_AUTHKEY": os.Getenv("TS_AUTHKEY"), "tailscale_debug": os.Getenv("tailscale_debug"), "HOME": os.Getenv("HOME")})
	case "oversized":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", maxStatusBytes+1))
	case "timeout":
		for {
			time.Sleep(time.Hour)
		}
	case "failure":
		_, _ = fmt.Fprintln(os.Stderr, "private@example.com https://login.tailscale.com/a/private /private/state")
		os.Exit(1)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestCommandBoundsAndEnvironment(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"status", "oversized", "timeout", "failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if mode == "timeout" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			// Race checks stay enabled; the helper needs no post-exit sleep.
			environment := append(os.Environ(), "GO_WANT_HOST_TAILSCALE_HELPER=1", "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0", "TS_AUTHKEY=private", "tailscale_debug=private")
			data, err := runCommand(ctx, executable, []string{"-test.run=^TestHostCommandHelper$", "--", mode}, environment)
			if mode != "status" {
				if err == nil || len(data) != 0 || strings.Contains(err.Error(), "private") {
					t.Fatalf("unbounded or private command failure: %d bytes, %v", len(data), err)
				}
				return
			}
			var values map[string]string
			if err != nil || json.Unmarshal(data, &values) != nil || values["TS_AUTHKEY"] != "" || values["tailscale_debug"] != "" || values["HOME"] != os.Getenv("HOME") {
				t.Fatalf("host environment was redirected: %s, %v", data, err)
			}
		})
	}
}
