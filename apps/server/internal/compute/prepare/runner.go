package prepare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func (e *Engine) run(ctx context.Context, id string) {
	r, err := e.store.mutate(context.Background(), id, true, func(r *record) error {
		if r.Job.Status == "cancelled" {
			return context.Canceled
		}
		r.Job.Status = "running"
		r.Job.Error = nil
		return nil
	})
	logger := e.jobLogger(r.Job).With("jobId", id)
	ctx = diagnostics.Context(ctx, logger)
	if err == nil {
		logger.Info("preparation.started")
		err = e.execute(ctx, id, r)
	}
	if err != nil {
		finished, saveErr := e.store.mutate(context.Background(), id, true, func(r *record) error {
			if r.Job.Status == "cancelled" {
				return errNoChange
			}
			r.Job.CanCancel = false
			if e.ctx.Err() != nil || errors.Is(err, context.Canceled) {
				r.Job.Status = "interrupted"
				r.Job.Error = &Failure{Code: "interrupted", Message: "Preparation was interrupted. Retry checks and continues this task.", Retryable: true}
			} else {
				r.Job.Status = "failed"
				r.Job.Error = failure(err)
			}
			r.Job.CanRetry = r.Job.Error.Retryable
			for i := range r.Job.Steps {
				if r.Job.Steps[i].Status == "running" {
					r.Job.Steps[i].Status = "failed"
					r.Job.Steps[i].Detail = r.Job.Error.Message
				}
			}
			return nil
		})
		if saveErr != nil {
			logger.Error("preparation.persistence_failed", saveErr, "cause", err)
			if logger == nil {
				log.Printf("persist preparation failure: %v (original error: %v)", saveErr, err)
			}
		} else if finished.Job.Status == "failed" {
			logger.Error("preparation."+finished.Job.Status, err)
		} else {
			logger.Info("preparation."+finished.Job.Status, "reason", err.Error())
		}
	} else {
		logger.Info("preparation.succeeded")
	}
}

func (e *Engine) execute(ctx context.Context, id string, r record) error {
	spec := r.Spec
	if spec.Plan.CacheHome != e.cache.Home {
		return problem(KindConflict, "task_cache_changed", "This task was reviewed with a different AXV_CACHE_HOME. Restore that cache location or review a new plan; existing artifacts were preserved.")
	}
	if spec.Plan.Platform != e.platform || spec.Plan.Arch != e.arch {
		return problem(KindConflict, "task_platform_changed", "This task was reviewed for a different platform or architecture. Existing data was preserved.")
	}
	if spec.Plan.Provider == "lima" && spec.LimaVersion != compute.SupportedLimaVersion {
		return problem(KindConflict, "task_version_changed", "This task was reviewed for a different Lima release. Review a new preparation plan with this AXV version.")
	}
	var intent commitIntent
	if spec.Existing {
		_, current, err := readCompute(e.options.Home)
		if err != nil {
			return err
		}
		if current != spec.ComputeFingerprint {
			return problem(KindConflict, "configuration_changed", "Existing VM configuration changed after review.")
		}
		intent = commitIntent{ComputeJSON: spec.ComputeJSON, HostJSON: spec.HostJSON, HostPath: spec.Plan.HostConfigPath}
	} else if r.Intent != nil {
		intent = *r.Intent
	} else {
		if err := e.step(ctx, id, "check", func(ctx context.Context) error {
			_, current, err := readCompute(e.options.Home)
			if err != nil {
				return err
			}
			if current != spec.ComputeFingerprint {
				return problem(KindConflict, "configuration_changed", "VM configuration changed after review. Existing settings were preserved.")
			}
			if e.options.ConfigurationLockedReason != "" {
				return problem(KindConflict, "configuration_locked", "%s", e.options.ConfigurationLockedReason)
			}
			if spec.Plan.Provider == "lima" && !spec.Imported {
				_, err = e.validateStorage(spec.Plan.Request)
				if err != nil {
					return err
				}
				if err = validateCapacity(spec.Plan.Request); err != nil {
					return err
				}
				return macPrerequisites(ctx, e.arch)
			}
			return nil
		}); err != nil {
			return err
		}
		if spec.Imported {
			if err := e.step(ctx, id, "connect", func(ctx context.Context) error { return e.verifyImported(ctx, spec) }); err != nil {
				return err
			}
			intent = commitIntent{ComputeJSON: spec.ComputeJSON, HostJSON: spec.HostJSON}
		} else if spec.Plan.Provider == "lima" {
			binary := spec.LimaBinary
			if err := e.step(ctx, id, "lima", func(ctx context.Context) error {
				var err error
				binary, err = e.installLima(ctx, id, spec.LimaArtifact, filepath.Dir(filepath.Dir(spec.LimaBinary)), spec.Plan.Arch, e.progress(ctx, id, "lima"))
				return err
			}); err != nil {
				return err
			}
			images := filepath.Join(spec.Plan.Request.StorageRoot, "images")
			if err := owneddir.Ensure(images, e.owner+":images"); err != nil {
				return err
			}
			image := spec.ImageArtifact
			rootfs := imageCache(spec.Plan.Request.StorageRoot, image)
			if err := e.step(ctx, id, "image", func(ctx context.Context) error {
				if spec.ImageShared {
					_, err := e.cache.CopyTo(ctx, e.client, image, rootfs, filepath.Join(images, ".prepare-"+id+"-image.part"), "", e.progress(ctx, id, "image"))
					return err
				}
				return copyPinned(ctx, spec.ImageSource, rootfs, filepath.Join(images, ".prepare-"+id+"-image.part"), image, e.progress(ctx, id, "image"))
			}); err != nil {
				return err
			}
			request := spec.Plan.Request
			host := compute.Config{DataDir: filepath.Join(request.StorageRoot, "compute"), Socket: filepath.Join(e.options.Home, "host", "control.sock"), Capacity: compute.Capacity{Resources: compute.Resources{CPUs: request.CPUs, MemoryMiB: request.MemoryMiB, DiskGiB: request.DiskGiB}, Machines: request.Machines, Workers: 2}, Lima: &compute.LimaConfig{Binary: binary, Version: compute.SupportedLimaVersion, Home: request.LimaHome}, Images: []compute.Image{{ID: spec.ImageBundle.ID, Name: spec.ImageBundle.Name, Provider: "lima", Arch: spec.Plan.Arch, Rootfs: rootfs, RootfsSHA256: image.SHA256, Guest: spec.ImageBundle.Guest}}}
			intent.HostJSON, _ = json.Marshal(host)
			intent.HostPath = spec.Plan.HostConfigPath
			intent.ComputeJSON, _ = json.Marshal(profile.Compute{Mode: "managed", HostConfig: intent.HostPath})
		} else {
			if err := e.step(ctx, id, "connect", func(ctx context.Context) error {
				binding, err := probeLocalService(ctx, spec.Plan.Request.Socket, true)
				if err != nil {
					return err
				}
				if binding.HostID != spec.ExpectedHostID {
					return problem(KindConflict, "host_changed", "The local service identity changed after review.")
				}
				return nil
			}); err != nil {
				return err
			}
			intent.ComputeJSON, _ = json.Marshal(profile.Compute{Mode: "external", Socket: spec.Plan.Request.Socket})
		}
		_, err := e.store.mutate(context.Background(), id, true, func(r *record) error {
			if r.Job.Status == "cancelled" || ctx.Err() != nil {
				return context.Canceled
			}
			r.Intent = &intent
			r.Job.CanCancel = false
			return nil
		})
		if err != nil {
			return err
		}
	}
	if !spec.Existing {
		if err := e.step(ctx, id, "commit", func(ctx context.Context) error {
			if e.beforeCommit != nil {
				if err := e.beforeCommit(); err != nil {
					return err
				}
			}
			return e.commit(ctx, spec, intent)
		}); err != nil {
			return err
		}
		if _, err := e.store.mutate(context.Background(), id, true, func(r *record) error { r.Job.Committed = true; r.Job.CanCancel = false; return nil }); err != nil {
			return err
		}
	}
	var activation Activation
	if err := json.Unmarshal(intent.ComputeJSON, &activation.Compute); err != nil {
		return err
	}
	if len(intent.HostJSON) != 0 {
		activation.Host = &compute.Config{}
		if err := json.Unmarshal(intent.HostJSON, activation.Host); err != nil {
			return err
		}
	}
	activation.ExpectedHostID = spec.ExpectedHostID
	var binding *Binding
	result := "configured"
	if err := e.step(ctx, id, "activate", func(ctx context.Context) error {
		if e.options.Activator == nil {
			return nil
		}
		actual, err := e.options.Activator(ctx, activation)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return problem(KindUnavailable, "activation_failed", "Configuration was saved, but the local VM service is not ready: %v", err)
		}
		if actual.HostID == "" || actual.ProtocolVersion != compute.ProtocolVersion || actual.ConfigFingerprint == "" {
			return problem(KindUnavailable, "activation_failed", "The VM service did not provide a verified runtime identity.")
		}
		if activation.ExpectedHostID != "" && actual.HostID != activation.ExpectedHostID {
			return problem(KindConflict, "host_changed", "The connected VM service has a different identity.")
		}
		if activation.Host != nil && actual.ConfigFingerprint != activation.Host.Fingerprint() {
			return problem(KindConflict, "host_changed", "The connected VM service has a different configuration.")
		}
		binding = &actual
		result = "ready"
		return nil
	}); err != nil {
		return err
	}
	_, err := e.store.mutate(context.Background(), id, true, func(r *record) error {
		r.Job.Status = "succeeded"
		r.Job.Phase = "complete"
		r.Job.Result = result
		r.Job.Binding = binding
		r.Job.CanCancel = false
		r.Job.CanRetry = false
		r.Job.Error = nil
		return nil
	})
	return err
}

func (e *Engine) step(ctx context.Context, id, phase string, work func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := e.store.mutate(context.Background(), id, true, func(r *record) error {
		if r.Job.Status == "cancelled" {
			return context.Canceled
		}
		r.Job.Phase = phase
		for i := range r.Job.Steps {
			if r.Job.Steps[i].ID == phase {
				r.Job.Steps[i].Status = "running"
				r.Job.Steps[i].Detail = ""
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	logger := diagnostics.FromContext(ctx).With("phase", phase)
	logger.Info("preparation.phase_started")
	if err = work(diagnostics.Context(ctx, logger)); err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.Error("preparation.phase_failed", err)
		}
		return err
	}
	_, err = e.store.mutate(context.Background(), id, true, func(r *record) error {
		if r.Job.Status == "cancelled" {
			return context.Canceled
		}
		for i := range r.Job.Steps {
			if r.Job.Steps[i].ID == phase {
				r.Job.Steps[i].Status = "succeeded"
			}
		}
		return nil
	})
	if err == nil {
		logger.Info("preparation.phase_completed")
	}
	return err
}

func (e *Engine) progress(ctx context.Context, id, phase string) progressFunc {
	logger := diagnostics.FromContext(ctx).With("phase", phase)
	lastLog := time.Time{}
	last := time.Time{}
	return func(done, total int64) {
		if time.Since(last) < time.Second && done != total {
			return
		}
		last = time.Now()
		_, err := e.store.mutate(context.Background(), id, false, func(r *record) error {
			if r.Job.Status != "running" {
				return errNoChange
			}
			for i := range r.Job.Steps {
				if r.Job.Steps[i].ID == phase {
					r.Job.Steps[i].BytesDone = done
					r.Job.Steps[i].BytesTotal = total
				}
			}
			return nil
		})
		if err != nil {
			logger.Error("preparation.persistence_failed", err)
			if logger == nil {
				log.Printf("persist preparation progress: %v", err)
			}
		} else if time.Since(lastLog) >= 5*time.Second || done == total {
			lastLog = time.Now()
			logger.Info("preparation.progress", "bytesDone", done, "bytesTotal", total)
		}
	}
}

func (e *Engine) commit(ctx context.Context, spec specification, intent commitIntent) error {
	var lock *os.File
	var err error
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		lock, err = profile.Lock(e.options.Home)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("profile is busy; retry configuration commit: %w", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer lock.Close()
	if _, err = profile.Read(e.options.Home); err != nil {
		return err
	}
	_, current, err := readCompute(e.options.Home)
	if err != nil {
		return err
	}
	var desired profile.Compute
	if err = json.Unmarshal(intent.ComputeJSON, &desired); err != nil {
		return err
	}
	var generic any
	if err = json.Unmarshal(intent.ComputeJSON, &generic); err != nil {
		return err
	}
	target := digestJSON(generic)
	if current != spec.ComputeFingerprint && current != target {
		return problem(KindConflict, "configuration_changed", "Another action changed VM configuration. The existing profile was preserved.")
	}
	if intent.HostPath != "" && len(intent.HostJSON) > 0 {
		if saved, readErr := os.ReadFile(intent.HostPath); readErr == nil {
			var actual any
			if json.Unmarshal(saved, &actual) != nil {
				return problem(KindConflict, "host_configuration_changed", "The host configuration at the reviewed path is invalid; it was preserved.")
			}
			var expected any
			_ = json.Unmarshal(intent.HostJSON, &expected)
			if digestJSON(actual) != digestJSON(expected) {
				return problem(KindConflict, "host_configuration_changed", "A different host configuration occupies the reviewed path; it was preserved.")
			}
		} else if os.IsNotExist(readErr) {
			if err = atomicCreate(intent.HostPath, intent.HostJSON); err != nil {
				return err
			}
		} else {
			return readErr
		}
	}
	if current == target {
		return nil
	}
	return profile.SaveCompute(e.options.Home, desired)
}

func atomicCreate(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".prepare-config-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(name, path); err != nil {
		return fmt.Errorf("publish new configuration without replacing existing files: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (e *Engine) verifyImported(ctx context.Context, spec specification) error {
	var c profile.Compute
	if err := json.Unmarshal(spec.ComputeJSON, &c); err != nil {
		return err
	}
	if c.Mode == "external" {
		binding, err := probeLocalService(ctx, c.Socket, e.platform == "linux")
		if err != nil {
			return err
		}
		if spec.ExpectedHostID != "" && binding.HostID != spec.ExpectedHostID {
			return problem(KindConflict, "host_changed", "The local service changed identity after review.")
		}
		return nil
	}
	host, err := compute.LoadConfig(c.HostConfig)
	if err != nil {
		return err
	}
	var expected compute.Config
	if err = json.Unmarshal(spec.HostJSON, &expected); err != nil {
		return err
	}
	if host.Fingerprint() != expected.Fingerprint() {
		return problem(KindConflict, "host_configuration_changed", "The imported host configuration changed after review.")
	}
	if host.Lima == nil {
		return fmt.Errorf("managed macOS hosting requires Lima")
	}
	return validateLima(ctx, host.Lima.Binary, e.arch)
}
