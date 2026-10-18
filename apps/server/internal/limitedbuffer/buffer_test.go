package limitedbuffer

import (
	"io"
	"strings"
	"testing"
)

func TestCaptureRetainsPrefixAndAcceptsDiscardedBytes(t *testing.T) {
	buffer := &Buffer{Limit: 5}
	for _, value := range []string{"abc", "defg", "more"} {
		n, err := buffer.Write([]byte(value))
		if err != nil || n != len(value) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", value, n, err, len(value))
		}
	}
	if string(buffer.Data) != "abcde" || !buffer.Truncated {
		t.Fatalf("capture = %q, truncated = %v", buffer.Data, buffer.Truncated)
	}
}

func TestExactLimitDoesNotReportTruncation(t *testing.T) {
	buffer := &Buffer{Limit: 5}
	n, err := io.Copy(buffer, strings.NewReader("abcde"))
	if err != nil || n != 5 || string(buffer.Data) != "abcde" || buffer.Truncated {
		t.Fatalf("copy = %d, %v; capture = %q, truncated = %v", n, err, buffer.Data, buffer.Truncated)
	}
	if n, err := buffer.Write(nil); err != nil || n != 0 || buffer.Truncated {
		t.Fatalf("empty write = %d, %v; truncated = %v", n, err, buffer.Truncated)
	}
}

func TestZeroValueDiscardsWithoutShortWrite(t *testing.T) {
	var buffer Buffer
	n, err := io.Copy(&buffer, strings.NewReader("discarded"))
	if err != nil || n != 9 || len(buffer.Data) != 0 || !buffer.Truncated {
		t.Fatalf("copy = %d, %v; capture = %q, truncated = %v", n, err, buffer.Data, buffer.Truncated)
	}
}
