package control

import (
	"context"
	"database/sql"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func catalog[T any](ctx context.Context, q querier, id, kind string) (T, error) {
	return decodeRow[T](q.QueryRowContext(ctx, "SELECT data FROM catalog WHERE id=? AND kind=?", id, kind))
}

func writeCatalog(ctx context.Context, tx *sql.Tx, id, kind string, value any, diskGiB int64) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog(id,kind,data,disk_gib) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET data=excluded.data,disk_gib=excluded.disk_gib WHERE catalog.kind=excluded.kind`, id, kind, encode(value), diskGiB)
	if err != nil {
		return err
	}
	if count, checkErr := result.RowsAffected(); checkErr != nil {
		return checkErr
	} else if count != 1 {
		return compute.Problem("configuration_conflict", "The catalog identity belongs to another resource kind.")
	}
	return err
}

func (s *Store) UploadInUse(ctx context.Context, path string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM catalog WHERE kind='image' AND json_extract(data,'$.uploadPath')=? AND json_extract(data,'$.status')!='deleted'", path).Scan(&count)
	return count != 0, err
}

func (s *Store) Images(ctx context.Context) ([]compute.ImageRecord, error) {
	return rowsJSON[compute.ImageRecord](s.db.QueryContext(ctx, "SELECT data FROM catalog WHERE kind='image' ORDER BY id"))
}

func (s *Store) Image(ctx context.Context, id string) (compute.ImageRecord, error) {
	return catalog[compute.ImageRecord](ctx, s.db, id, "image")
}

func (s *Store) SeedImages(ctx context.Context, images []compute.Image) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		for _, image := range images {
			existing, err := catalog[compute.ImageRecord](ctx, tx, image.ID, "image")
			if err == nil {
				if fingerprint(existing.Image) != fingerprint(image) || existing.Managed {
					return compute.Problem("configuration_conflict", "Configured image %s changed; register a new immutable image ID instead.", image.ID)
				}
				continue
			}
			if compute.ErrorCode(err) != "not_found" {
				return err
			}
			record := compute.ImageRecord{Image: image, Status: "configured", Revision: 1, Source: "Host configuration", CreatedAt: time.Now().UTC()}
			if err = writeCatalog(ctx, tx, image.ID, "image", record, 0); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) currentConfig(ctx context.Context) (compute.Config, error) {
	c := s.config
	records, err := s.store.Images(ctx)
	if err != nil {
		return c, err
	}
	c.Images = nil
	for _, record := range records {
		if record.Status == "configured" || record.Status == "ready" {
			c.Images = append(c.Images, record.Image)
		}
	}
	return c, nil
}

func (s *Service) Create(ctx context.Context, req compute.CreateRequest) (compute.Result, error) {
	c, err := s.currentConfig(ctx)
	if err != nil {
		return compute.Result{}, err
	}
	return s.store.Create(ctx, c, req)
}

func (s *Service) resolveImage(ctx context.Context, id, provider string) (compute.Image, error) {
	c, err := s.currentConfig(ctx)
	if err != nil {
		return compute.Image{}, err
	}
	return c.Image(id, provider)
}
