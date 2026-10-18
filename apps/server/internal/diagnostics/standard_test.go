package diagnostics

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"
)

func TestStandardDiagnosticsRedactAndRestore(t *testing.T) {
	previousOutput, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	var original, events bytes.Buffer
	log.SetOutput(&original)
	log.SetFlags(log.Ldate | log.Lshortfile)
	log.SetPrefix("original prefix: ")

	restore := RedirectStandard(Stderr("host", &events))
	log.Print("TLS certificate verify failed for https://user:private-password@example.test/file?token=private-query#private-fragment\nAuthorization: Bearer private-header")
	restore()
	restore()
	if log.Writer() != &original || log.Flags() != log.Ldate|log.Lshortfile || log.Prefix() != "original prefix: " {
		t.Fatal("standard logger configuration was not restored")
	}
	if original.Len() != 0 {
		t.Fatal("diagnostic was also written outside the structured sink")
	}
	if bytes.Count(events.Bytes(), []byte{'\n'}) != 1 {
		t.Fatal("multiline diagnostic escaped its JSON record")
	}
	var event map[string]any
	if err := json.Unmarshal(events.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["event"] != "service.diagnostic" || event["role"] != "host" || !strings.Contains(event["message"].(string), "TLS certificate verify failed") {
		t.Fatalf("diagnostic lost its context: %v", event)
	}
	for _, secret := range []string{"private-password", "private-query", "private-fragment", "private-header", "original prefix"} {
		if strings.Contains(events.String(), secret) {
			t.Fatalf("diagnostic retained %q", secret)
		}
	}
	log.Print("after restoration")
	if !strings.Contains(original.String(), "original prefix: ") || !strings.Contains(original.String(), "after restoration") {
		t.Fatal("standard diagnostic did not return to its original output")
	}
}
