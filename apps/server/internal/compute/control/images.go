package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	"github.com/cofy-x/axv/apps/server/internal/filedigest"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

type imageChange struct {
	URL    string `json:"url,omitempty"`
	Upload string `json:"upload,omitempty"`
}

func (s *Service) CreateImage(ctx context.Context, req compute.ImageRequest, upload string) (result compute.ImageResult, err error) {
	if err = compute.ValidateImageRequest(req, upload != ""); err != nil {
		return
	}
	if s.drivers["lima"] == nil {
		return result, compute.Problem("unsupported", "Image management requires the Lima provider.")
	}
	hash := fingerprint(struct {
		Request compute.ImageRequest
		Upload  bool
	}{req, upload != ""})
	err = s.store.transaction(ctx, func(tx *sql.Tx) error {
		op, exists, err := replay(ctx, tx, req.RequestID, hash)
		if err != nil {
			return err
		}
		if exists {
			record, err := catalog[compute.ImageRecord](ctx, tx, op.ResourceID, "image")
			result = compute.ImageResult{Image: record.Info(), Operation: op.Operation}
			return err
		}
		if req.Guest != nil {
			if err := req.Guest.Validate(); err != nil {
				return compute.Problem("invalid_request", "%s", err.Error())
			}
		}
		reserve := int64(9)
		if upload != "" {
			info, err := os.Stat(upload)
			if err != nil {
				return err
			}
			reserve = (info.Size()+(1<<30)-1)>>30 + 1
		}
		if err = admit(ctx, tx, s.config.Capacity, compute.Resources{DiskGiB: reserve}, false); err != nil {
			return err
		}
		id := compute.NewID("image")
		source := "Uploaded file"
		if upload == "" {
			parsed, _ := url.Parse(req.URL)
			source = "HTTPS · " + parsed.Hostname()
		}
		record := compute.ImageRecord{Image: compute.Image{ID: id, Name: req.Name, Provider: "lima", Arch: req.Arch, Rootfs: filepath.Join(s.config.DataDir, "images", id, "rootfs"), RootfsSHA256: req.SHA256, Guest: req.Guest}, Status: "queued", Managed: true, Revision: 1, ReservedGiB: reserve, Source: source, CreatedAt: time.Now().UTC(), UploadPath: upload}
		op = newOperation(req.RequestID, "", "import-image")
		op.ResourceKind, op.ResourceID, op.Payload = "image", id, json.RawMessage(encode(imageChange{URL: req.URL, Upload: upload}))
		if err = insertOperation(ctx, tx, op, hash, id, ""); err != nil {
			return err
		}
		if err = writeCatalog(ctx, tx, id, "image", record, reserve); err != nil {
			return err
		}
		result = compute.ImageResult{Image: record.Info(), Operation: op.Operation}
		return nil
	})
	return
}

func (s *Store) DeleteImage(ctx context.Context, id, requestID string, revision int64) (op operationRecord, err error) {
	if err = compute.ValidateRequestID(requestID); err != nil {
		return
	}
	hash := fingerprint([]any{"delete-image", id, revision})
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var exists bool
		op, exists, err = replay(ctx, tx, requestID, hash)
		if err != nil || exists {
			return err
		}
		record, err := catalog[compute.ImageRecord](ctx, tx, id, "image")
		if err != nil {
			return err
		}
		if record.Revision != revision {
			return compute.Problem("revision_conflict", "The image changed. Refresh before removing it.")
		}
		if record.Status == "deleted" {
			return compute.Problem("not_found", "The image was removed.")
		}
		var used int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM machines WHERE status!='deleted' AND json_extract(data,'$.spec.imageId')=?", id).Scan(&used); err != nil {
			return err
		}
		var artifacts int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM catalog WHERE kind!='image' AND json_extract(data,'$.status')!='deleted' AND json_extract(data,'$.spec.imageId')=?", id).Scan(&artifacts); err != nil {
			return err
		}
		if used+artifacts != 0 {
			return compute.Problem("image_conflict", "This image is referenced by a VM, backup or template.")
		}
		op = newOperation(requestID, "", "delete-image")
		op.ResourceID, op.ResourceKind = id, "image"
		if err = insertOperation(ctx, tx, op, hash, id); err != nil {
			return err
		}
		record.Status, record.Revision = "deleting", record.Revision+1
		return writeCatalog(ctx, tx, id, "image", record, record.ReservedGiB)
	})
	return
}

func (s *Service) performImage(ctx context.Context, op operationRecord) (compute.Observation, error) {
	record, err := s.store.Image(ctx, op.ResourceID)
	if err != nil {
		return compute.Observation{}, err
	}
	dir := filepath.Join(s.config.DataDir, "images", record.Image.ID)
	owner := s.store.HostID + ":" + record.Image.ID
	if op.Action == "delete-image" {
		if record.Managed {
			if err = owneddir.Check(dir, owner); err == nil {
				err = os.RemoveAll(dir)
			} else if os.IsNotExist(err) {
				err = nil
			}
			if err != nil {
				return compute.Observation{}, err
			}
		}
		if record.UploadPath != "" {
			if err = s.RemoveUpload(record.UploadPath); err != nil {
				return compute.Observation{}, err
			}
		}
		return compute.Observation{}, nil
	}
	if op.Action != "import-image" {
		return compute.Observation{}, compute.Problem("invalid_request", "Unknown image operation.")
	}
	var change imageChange
	if err = json.Unmarshal(op.Payload, &change); err != nil {
		return compute.Observation{}, err
	}
	if err = owneddir.Ensure(dir, owner); err != nil {
		return compute.Observation{}, err
	}
	if err = s.phase(ctx, op, "preparing-image"); err != nil {
		return compute.Observation{}, err
	}
	if _, err = os.Stat(record.Image.Rootfs); os.IsNotExist(err) {
		if err = diskspace.Require(dir, record.ReservedGiB<<30); err != nil {
			return compute.Observation{}, err
		}
		temporary := filepath.Join(dir, "download.partial")
		if change.Upload != "" {
			if err = s.validUpload(change.Upload); err != nil {
				return compute.Observation{}, err
			}
			if _, err = os.Stat(temporary); os.IsNotExist(err) {
				if err = atomicfile.Commit(change.Upload, temporary); err != nil {
					return compute.Observation{}, err
				}
			} else if err != nil {
				return compute.Observation{}, err
			}
		} else {
			if err = s.phase(ctx, op, "downloading-image"); err != nil {
				return compute.Observation{}, err
			}
			if err = downloadImage(ctx, change.URL, temporary); err != nil {
				return compute.Observation{}, err
			}
		}
		if err = atomicfile.Commit(temporary, record.Image.Rootfs); err != nil {
			return compute.Observation{}, err
		}
	} else if err != nil {
		return compute.Observation{}, err
	}
	if err = s.phase(ctx, op, "verifying-image"); err != nil {
		return compute.Observation{}, err
	}
	file, err := os.Open(record.Image.Rootfs)
	if err != nil {
		return compute.Observation{}, err
	}
	defer file.Close()
	checksum, err := filedigest.Sum(ctx, file)
	if err != nil {
		return compute.Observation{}, err
	}
	if checksum != strings.ToLower(record.Image.RootfsSHA256) {
		return compute.Observation{}, compute.Problem("invalid_request", "Image SHA-256 does not match. Remove the failed import before retrying with a verified source.")
	}
	minimum, err := compute.InspectImage(file)
	if err != nil {
		return compute.Observation{}, compute.Problem("invalid_request", "%v", err)
	}
	if minimum > s.config.Capacity.DiskGiB {
		return compute.Observation{}, compute.Problem("capacity_exceeded", "The image's virtual root disk exceeds host disk capacity.")
	}
	record.Image.MinRootGiB = minimum
	info, err := file.Stat()
	if err != nil {
		return compute.Observation{}, err
	}
	record.SizeBytes = info.Size()
	record.ReservedGiB = (record.SizeBytes + (1 << 30) - 1) >> 30
	record.UploadPath = ""
	return compute.Observation{Image: &record}, nil
}

func downloadImage(ctx context.Context, source, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return compute.Problem("invalid_request", "Invalid image URL.")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, previous []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil || len(previous) > 5 {
			return fmt.Errorf("unsupported image redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("image download could not reach its HTTPS source; check the URL, proxy and connection")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("image source returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > compute.MaxImageBytes {
		return compute.Problem("invalid_request", "Image download exceeds the 8 GiB limit.")
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(response.Body, compute.MaxImageBytes+1))
	if err != nil {
		return fmt.Errorf("image download was interrupted")
	}
	if n > compute.MaxImageBytes {
		return compute.Problem("invalid_request", "Image download exceeds the 8 GiB limit.")
	}
	return f.Sync()
}

func (s *Service) NewUpload() (*os.File, error) {
	dir := filepath.Join(s.config.DataDir, "image-uploads")
	if err := owneddir.Ensure(dir, s.store.HostID+":image-uploads"); err != nil {
		return nil, err
	}
	if err := diskspace.Require(dir, 0); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, "upload-*.blob")
}

func (s *Service) validUpload(path string) error {
	dir := filepath.Join(s.config.DataDir, "image-uploads")
	if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), "upload-") {
		return fmt.Errorf("image upload is outside its owned staging directory")
	}
	return owneddir.Check(dir, s.store.HostID+":image-uploads")
}

func (s *Service) RemoveUpload(path string) error {
	if err := s.validUpload(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) finishImage(ctx context.Context, tx *sql.Tx, op operationRecord, observed compute.Observation, operationErr error) error {
	record, err := catalog[compute.ImageRecord](ctx, tx, op.ResourceID, "image")
	if err != nil {
		return err
	}
	if operationErr != nil {
		record.Status, record.Reason = "failed", operationErr.Error()
	} else if op.Action == "delete-image" {
		record.Status, record.ReservedGiB, record.UploadPath = "deleted", 0, ""
	} else if observed.Image != nil {
		record = *observed.Image
		record.Status, record.Reason = "ready", ""
	} else {
		return compute.Problem("invalid_request", "The image operation did not return its verified state.")
	}
	record.Revision++
	return writeCatalog(ctx, tx, record.Image.ID, "image", record, record.ReservedGiB)
}
