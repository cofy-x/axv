package diagnostics

import (
	"log"
	"strings"
	"sync"
)

// RedirectStandard captures standard-library diagnostics for the process's
// service. Call it only at composition, after acquiring service ownership, and
// restore it after all service goroutines have stopped. It never redirects
// stdout, stderr, or session streams.
func RedirectStandard(logger *Logger) func() {
	output, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(standardWriter{logger})
	log.SetFlags(0)
	log.SetPrefix("")
	return sync.OnceFunc(func() {
		log.SetOutput(output)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	})
}

type standardWriter struct{ logger *Logger }

func (w standardWriter) Write(p []byte) (int, error) {
	w.logger.Info("service.diagnostic", "message", strings.TrimSpace(string(p)))
	return len(p), nil
}
