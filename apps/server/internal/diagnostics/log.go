// Package diagnostics records bounded, sanitized operational events.
// It never accepts process streams, request bodies, or session output.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type outputState struct {
	mu       sync.Mutex
	output   io.Writer
	fallback io.Writer
	status   string
	secrets  []string
}

// Logger is safe for concurrent use. Derived loggers share output and status.
type Logger struct {
	state  *outputState
	fields map[string]any
}

// New records events to output and reports failed writes to fallback. Nil
// writers use stderr. It captures environment secrets for redaction at creation.
func New(component string, output io.Writer, fallback io.Writer) *Logger {
	if output == nil {
		output = os.Stderr
	}
	if fallback == nil {
		fallback = os.Stderr
	}
	return &Logger{
		state:  &outputState{output: output, fallback: fallback, secrets: environmentSecrets()},
		fields: map[string]any{"role": component},
	}
}

// Stderr leaves log-file ownership and retention to the process supervisor.
func Stderr(component string, output io.Writer) *Logger {
	logger := New(component, output, nil)
	logger.state.fallback = nil
	return logger
}

func (l *Logger) Status() string {
	if l == nil {
		return ""
	}
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	return l.state.status
}
func (l *Logger) With(fields ...any) *Logger {
	if l == nil {
		return nil
	}
	n := &Logger{state: l.state, fields: map[string]any{}}
	for k, v := range l.fields {
		n.fields[k] = v
	}
	for i := 0; i+1 < len(fields); i += 2 {
		if k, ok := fields[i].(string); ok {
			n.fields[k] = fields[i+1]
		}
	}
	return n
}
func (l *Logger) Info(event string, fields ...any) { l.record("info", event, nil, fields...) }
func (l *Logger) Error(event string, err error, fields ...any) {
	l.record("error", event, err, fields...)
}
func (l *Logger) record(level, event string, err error, fields ...any) {
	if l == nil {
		return
	}
	l = l.With(fields...)
	s := l.state
	s.mu.Lock()
	defer s.mu.Unlock()
	r := map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "event": event, "pid": os.Getpid()}
	for k, v := range l.fields {
		if sensitiveKey(k) {
			r[k] = "[REDACTED]"
		} else {
			r[k] = sanitize(v, s.secrets)
		}
	}
	if err != nil {
		r["error"] = redact(err.Error(), s.secrets)
	}
	b, marshalErr := json.Marshal(r)
	if marshalErr != nil {
		b = []byte(`{"level":"error","event":"logging.encode_failed"}`)
	}
	if len(b) > 32<<10 {
		b, _ = json.Marshal(map[string]any{"time": r["time"], "level": level, "event": event, "pid": os.Getpid(), "role": r["role"], "error": "operational event exceeded record limit"})
	}
	b = append(b, '\n')
	n, writeErr := s.output.Write(b)
	if s.fallback == nil {
		return
	}
	if writeErr == nil && n != len(b) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		s.status = redact(writeErr.Error(), s.secrets)
		_, _ = fmt.Fprintf(s.fallback, "AXV logging failed: %s\n", s.status)
		_, _ = s.fallback.Write(b)
	} else {
		s.status = ""
	}
}
