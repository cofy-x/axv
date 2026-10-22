package providerutil

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/filedigest"
)

type CopyCompletion struct {
	Version  int    `json:"version"`
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
	SizeGiB  int64  `json:"sizeGiB"`
	SHA256   string `json:"sha256"`
}

func ReadCopyCompletion(name, sourceID, targetID string, sizeGiB int64) (CopyCompletion, bool, error) {
	var result CopyCompletion
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return result, true, fmt.Errorf("the copy completion record is not a bounded regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return result, true, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return result, true, fmt.Errorf("the copy completion record changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return result, true, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 4096 || decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return result, true, fmt.Errorf("the copy completion record is invalid; preserve it for inspection")
	}
	digest, err := hex.DecodeString(result.SHA256)
	if result.Version != 1 || result.SourceID != sourceID || result.TargetID != targetID || result.SizeGiB != sizeGiB || err != nil || len(digest) != 32 || result.SHA256 != strings.ToLower(result.SHA256) {
		return result, true, fmt.Errorf("the copy completion identity or digest is invalid; no replacement was created")
	}
	return result, true, nil
}

func VerifyCopyCompletion(ctx context.Context, name, sourceID, targetID string, sizeGiB int64, file *os.File) error {
	record, exists, err := ReadCopyCompletion(name, sourceID, targetID, sizeGiB)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("the immutable copy completion record is missing; no replacement was created")
	}
	actual, err := filedigest.Sum(ctx, file)
	if err != nil {
		return err
	}
	if actual != record.SHA256 {
		return fmt.Errorf("the completed volume copy failed its recorded checksum verification; preserve the changed storage for inspection")
	}
	return nil
}

func WriteCopyCompletion(name, sourceID, targetID string, sizeGiB int64, digest string) error {
	data, err := json.Marshal(CopyCompletion{Version: 1, SourceID: sourceID, TargetID: targetID, SizeGiB: sizeGiB, SHA256: digest})
	if err != nil {
		return err
	}
	return atomicfile.Write(name, data, 0600)
}
