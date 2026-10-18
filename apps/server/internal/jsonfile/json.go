// Package jsonfile reads bounded JSON metadata and atomically publishes complete values.
package jsonfile

import (
	"encoding/json"
	"io"
	"os"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
)

// Write atomically publishes indented JSON with a trailing newline and the supplied mode.
func Write(path string, value any, mode os.FileMode) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(b, '\n'), mode)
}

// Read decodes the first JSON value from at most 1 MiB of path.
// Unknown object fields and data after that first value are left to the caller.
func Read(path string, into any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(into)
}
