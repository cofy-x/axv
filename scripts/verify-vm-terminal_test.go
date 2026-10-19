//go:build linux || darwin

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type terminalRoundTrip func(*http.Request) (*http.Response, error)

func (f terminalRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTerminalProbeCleansKnownIdentitiesBeforeReturning(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid-status", true: "replaced-retry"}[replaced], func(t *testing.T) {
			created, deleted := 0, map[string]bool{}
			client := &http.Client{Transport: terminalRoundTrip(func(r *http.Request) (*http.Response, error) {
				body := `{"machineId":"vm","bootId":"original-boot"}`
				if r.Method == "POST" {
					created++
					id, status := strings.Repeat("a", 32), "invalid"
					if replaced {
						status = "running"
						if created > 1 {
							id = strings.Repeat("b", 32)
						}
					}
					body = `{"id":"` + id + `","bootId":"original-boot","status":"` + status + `"}`
				}
				if r.Method == "DELETE" {
					id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					deleted[id] = true
					body = `{"id":"` + id + `","bootId":"original-boot","status":"ended"}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			err := verifyTerminalProbe(context.Background(), client, "host", "vm")
			if err == nil {
				t.Fatal("invalid terminal identity passed acceptance")
			}
			want := 1
			if replaced {
				want = 2
			}
			if len(deleted) != want || !deleted[strings.Repeat("a", 32)] || (replaced && !deleted[strings.Repeat("b", 32)]) {
				t.Fatalf("known owned terminal was leaked: %v (%v)", deleted, err)
			}
		})
	}
}

func TestTerminalProbeDoesNotReplaceAnUnknownCreation(t *testing.T) {
	created := 0
	client := &http.Client{Transport: terminalRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			created++
			return nil, errors.New("creation response lost after acceptance")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"machineId":"vm","bootId":"original-boot"}`))}, nil
	})}
	err := verifyTerminalProbe(context.Background(), client, "host", "vm")
	if err == nil || !strings.Contains(err.Error(), "unconfirmed outcome") || !strings.Contains(err.Error(), "native-terminal-") || created != 1 {
		t.Fatalf("unknown terminal was replaced or lost its recovery identity: requests=%d error=%v", created, err)
	}
}

func TestTerminalProbeRejectsHostReplacementBeforeApplicationTraffic(t *testing.T) {
	for _, host := range []string{"original", "replacement"} {
		t.Run(host, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			done := make(chan error, 1)
			go func() {
				request, err := http.ReadRequest(bufio.NewReader(server))
				if err != nil {
					done <- err
					return
				}
				if request.URL.Path != "/v1/runtime" {
					done <- errors.New("application traffic preceded runtime verification")
					return
				}
				body, _ := json.Marshal(map[string]any{"ready": true, "hostId": host, "protocolVersion": 3})
				response := &http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), ContentLength: int64(len(body))}
				done <- response.Write(server)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := verifyTerminalHost(ctx, client, "original")
			if (err != nil) != (host != "original") {
				t.Fatalf("wrong binding result: %v", err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
