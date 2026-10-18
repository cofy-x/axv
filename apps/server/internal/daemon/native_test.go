package daemon

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testJob() Job {
	return Job{Role: "host", Label: "com.axv.test.host", Args: []string{"/tmp/AXV & spaces/axv", "host", "--config", "/tmp/owned-unique.json"}, Marker: "/tmp/owned-unique.json", Definition: "/tmp/host.plist", Log: "/tmp/host.log", Environment: map[string]string{"PATH": "/tmp/a & b:/bin", "AXV_HOME": "/tmp/profile"}}
}

func TestLaunchdDefinitionPreservesArgumentsAndVMs(t *testing.T) {
	j := testJob()
	s := plist(j)
	d := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(s, "AXV &amp; spaces") || !strings.Contains(s, "<key>AbandonProcessGroup</key><true/>") {
		t.Fatal(s)
	}
	j.Role = "desktop"
	if strings.Contains(plist(j), "AbandonProcessGroup") {
		t.Fatal("desktop child shells would survive shutdown")
	}
}

func TestNativeRefusesForeignService(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			calls := 0
			n := &native{platform: platform, run: func(_ context.Context, name string, args ...string) (string, error) {
				calls++
				return "LoadState=loaded\nActiveState=active\nforeign executable", nil
			}}
			if err := n.Stop(context.Background(), testJob()); err == nil {
				t.Fatal("stopped foreign service")
			}
			if calls != 1 {
				t.Fatalf("mutating command invoked, calls=%d", calls)
			}
		})
	}
}

func TestMissingServiceAndSupervisorFailureDiffer(t *testing.T) {
	n := &native{platform: "darwin", run: func(context.Context, string, ...string) (string, error) {
		return "Could not find service", errors.New("exit 113")
	}}
	loaded, err := n.Loaded(context.Background(), testJob())
	if err != nil || loaded {
		t.Fatalf("%t %v", loaded, err)
	}
	n.run = func(context.Context, string, ...string) (string, error) {
		return "Could not find domain", errors.New("exit 113")
	}
	if _, err = n.Loaded(context.Background(), testJob()); err == nil {
		t.Fatal("masked missing login session")
	}
}

func TestSystemdUsesIndependentTransientService(t *testing.T) {
	var args []string
	n := &native{platform: "linux", run: func(_ context.Context, name string, a ...string) (string, error) {
		if name == "systemctl" {
			return "LoadState=not-found", nil
		}
		if name != "systemd-run" {
			t.Fatal(name)
		}
		args = a
		return "", nil
	}}
	j := testJob()
	j.Role = "desktop"
	if err := n.Start(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(args, "\n")
	if !strings.Contains(all, "--property=KillMode=process") || !strings.Contains(all, "--user") || strings.Contains(all, "enable") {
		t.Fatal(all)
	}
	if args[len(args)-1] != j.Marker || args[len(args)-4] != j.Args[0] {
		t.Fatal("lost argument boundaries")
	}
}

func TestNativeCancellationIsNotReportedAsAMissingService(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		for _, operation := range []string{"inspect", "start", "stop"} {
			for _, cause := range []string{"caller cancellation", "native command deadline"} {
				t.Run(platform+"/"+operation+"/"+cause, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					var commandError error = context.DeadlineExceeded
					calls := 0
					n := &native{platform: platform, run: func(actual context.Context, _ string, _ ...string) (string, error) {
						calls++
						if actual != ctx {
							t.Error("native command lost the caller's cancellation context")
						}
						if cause == "caller cancellation" {
							cancel()
							commandError = ctx.Err()
						}
						if platform == "darwin" {
							return "Could not find service", commandError
						}
						return "LoadState=not-found", commandError
					}}
					j := testJob()
					j.Definition = filepath.Join(t.TempDir(), "owned.plist")
					var err error
					switch operation {
					case "inspect":
						_, err = n.Loaded(ctx, j)
					case "start":
						err = n.Start(ctx, j)
					case "stop":
						err = n.Stop(ctx, j)
					}
					if !errors.Is(err, commandError) || calls != 1 {
						t.Fatalf("cancellation was masked or another native command ran: %v calls=%d", err, calls)
					}
					if _, err := os.Stat(j.Definition); !os.IsNotExist(err) {
						t.Fatalf("canceled inspection published a service definition: %v", err)
					}
				})
			}
		}
	}
}
