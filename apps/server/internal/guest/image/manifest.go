package image

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const maxManifestBytes = 64 << 10

func decode(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return fmt.Errorf("image manifest must be a regular file of at most %d bytes", maxManifestBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, maxManifestBytes+1))
	d.DisallowUnknownFields()
	if err = d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("image manifest must contain one JSON object")
	}
	return nil
}
