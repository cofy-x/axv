// Package limitedbuffer captures a bounded prefix while accepting all writes.
package limitedbuffer

// Buffer retains at most Limit bytes and reports whether writes were truncated.
type Buffer struct {
	Data      []byte
	Limit     int
	Truncated bool
}

// Write accepts all bytes and discards any beyond Limit.
func (b *Buffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.Limit - len(b.Data)
	if remaining < len(p) {
		b.Truncated = true
		if remaining < 0 {
			remaining = 0
		}
		p = p[:remaining]
	}
	b.Data = append(b.Data, p...)
	return n, nil
}
