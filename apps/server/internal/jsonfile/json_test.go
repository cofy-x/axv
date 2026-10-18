package jsonfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePreservesMetadataEncodingAndRejectsInvalidValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.json")
	if err := Write(path, map[string]string{"owner": "host:first"}, 0600); err != nil {
		t.Fatal(err)
	}
	const expected = "{\n  \"owner\": \"host:first\"\n}\n"
	if err := Write(path, func() {}, 0600); err == nil {
		t.Fatal("an unsupported JSON value was accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("failed serialization replaced the saved metadata: %q %v", data, err)
	}
}

func TestReadRetainsFirstValueAndInputLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(path, []byte(`{"owner":"host:first","future":true} {"owner":"ignored"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var value struct{ Owner string }
	if err := Read(path, &value); err != nil || value.Owner != "host:first" {
		t.Fatalf("unknown fields or trailing data changed first-value decoding: %+v %v", value, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", 1<<20)+`{"owner":"outside-limit"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Read(path, &value); err == nil {
		t.Fatal("decoded JSON beyond the metadata read limit")
	}
}
