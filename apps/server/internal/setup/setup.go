// Package setup adapts the shared VM preparation engine for interactive and
// unattended CLI use, or follows that same engine in a running desktop.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/hostpath"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

type preparation interface {
	Check(context.Context) (prepare.Check, error)
	Plan(context.Context, prepare.Request) (prepare.Plan, error)
	Start(context.Context, prepare.StartRequest) (prepare.Job, error)
	Job(context.Context, string) (prepare.Job, error)
	Retry(context.Context, string, prepare.ActionRequest) (prepare.Job, error)
}

type Options struct {
	Request  prepare.Request
	Retry    string
	Yes      bool
	Explicit bool
	// Confirm reviews the displayed plan using the caller's interaction boundary.
	// It must return when its context is cancelled. Nil requires explicit --yes.
	Confirm func(context.Context) error
}

func RunOptions(ctx context.Context, o Options, output, diagnostic io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Normalize(&o); err != nil {
		return err
	}
	home, err := profile.Home()
	if err != nil {
		return err
	}
	if state, readErr := launch.ReadState(home); readErr == nil {
		client, verifyErr := connect(ctx, state)
		if verifyErr == nil {
			defer client.Close()

			fmt.Fprintln(output, "Using the running AXV desktop. Closing this command only detaches from its preparation task.")
			return execute(ctx, client, o, output, true)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Holding the exact desktop lock prevents an offline engine from racing a
	// starting desktop, even when its HTTP state is absent, stale or unreachable.
	lock, err := lockOffline(home)
	if err != nil {
		return err
	}
	defer lock.Close()
	logger, err := logs.Open(home, "setup", diagnostic)
	if err != nil {
		return err
	}
	if _, err = profile.Initialize(home); err != nil {
		return err
	}
	ctx = diagnostics.Context(ctx, logger.Logger)
	logger.Info("service.start")
	defer logger.Info("service.stop")
	lockedReason := ""
	if os.Getenv("AXV_COMPUTE_SOCKET") != "" {
		lockedReason = "AXV_COMPUTE_SOCKET controls this process. Remove the override to prepare profile settings."
	}
	engine, err := prepare.New(prepare.Options{Home: home, ConfigurationLockedReason: lockedReason, Logger: logger.Logger})
	if err != nil {
		return err
	}
	defer engine.Close()
	return execute(ctx, engine, o, output, false)
}

func lockOffline(home string) (*os.File, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	run := filepath.Join(home, "run")
	if err := owneddir.Ensure(run, profile.Identity(home)); err != nil {
		return nil, err
	}
	lock, err := filelock.TryLock(filepath.Join(run, "desktop.lock"))
	if err != nil {
		return nil, fmt.Errorf("the AXV desktop is starting or unavailable; wait for it and retry setup instead of preparing concurrently: %w", err)
	}
	return lock, nil
}

// Normalize validates service options independently of command-line parsing.
func Normalize(o *Options) error {
	if o.Request.Socket != "" && o.Request.HostConfig != "" {
		return fmt.Errorf("choose --socket or --host-config")
	}
	if o.Retry != "" && o.Explicit {
		return fmt.Errorf("--retry uses the original task settings; do not supply new preparation settings")
	}
	if o.Request.CPUs < 0 || o.Request.MemoryMiB < 0 || (o.Request.MemoryMiB > 0 && o.Request.MemoryMiB < 256) || o.Request.DiskGiB < 0 || o.Request.Machines < 0 {
		return fmt.Errorf("invalid preparation allocation limits")
	}
	for _, value := range []*string{&o.Request.Socket, &o.Request.HostConfig, &o.Request.ImageBundle, &o.Request.StorageRoot, &o.Request.LimaHome} {
		if *value != "" {
			if strings.TrimSpace(*value) == "" {
				return fmt.Errorf("preparation paths must not be empty")
			}
			path, err := hostpath.Resolve(*value, os.UserHomeDir)
			if err != nil {
				return err
			}
			*value = path
		}
	}
	return nil
}

func execute(ctx context.Context, service preparation, o Options, output io.Writer, live bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	check, err := service.Check(ctx)
	if err != nil {
		return err
	}
	if check.ActiveJob != nil {
		if o.Explicit || (o.Retry != "" && o.Retry != check.ActiveJob.ID) {
			return fmt.Errorf("preparation task %s is already active; run axv setup without new settings to follow it", check.ActiveJob.ID)
		}
		return follow(ctx, service, *check.ActiveJob, output, live)
	}
	if o.Retry == "" && !o.Explicit && check.LatestJob != nil && check.LatestJob.CanRetry {
		return follow(ctx, service, *check.LatestJob, output, live)
	}
	var job prepare.Job
	if o.Retry != "" {
		job, err = service.Job(ctx, o.Retry)
		if err != nil {
			return err
		}
		if !job.CanRetry {
			return fmt.Errorf("task %s cannot be retried in state %s", job.ID, job.Status)
		}
		showPlan(job.Plan, output)
		if err = confirm(ctx, o); err != nil {
			return err
		}
		job, err = service.Retry(ctx, job.ID, prepare.ActionRequest{RequestID: compute.NewID("retry"), ExpectedRevision: job.Revision})
	} else {
		plan, planErr := service.Plan(ctx, o.Request)
		if planErr != nil {
			return planErr
		}
		showPlan(plan, output)
		if !plan.CanStart {
			return fmt.Errorf("preparation checks are blocked; resolve the listed checks and review again")
		}
		if err = confirm(ctx, o); err != nil {
			return err
		}
		request := prepare.StartRequest{RequestID: compute.NewID("setup"), PlanID: plan.ID, Confirmed: true}
		job, err = service.Start(ctx, request)
		if err != nil {
			return fmt.Errorf("start preparation request %s: %w; if its outcome is unknown, run axv setup without new settings to follow the existing task", request.RequestID, err)
		}
	}
	if err != nil {
		return err
	}
	return follow(ctx, service, job, output, live)
}

func showPlan(plan prepare.Plan, output io.Writer) {
	fmt.Fprintln(output, "VM preparation plan:")
	fmt.Fprintf(output, "  Shared cache: %s\n", plan.CacheHome)
	for _, action := range plan.Actions {
		fmt.Fprintf(output, "  - %s\n", action)
	}
	if r := plan.Request; r.StorageRoot != "" {
		fmt.Fprintf(output, "  Storage: %s\n  Lima state: %s\n  Limits: %d CPUs, %d MiB memory, %d GiB disk, %d machines\n", r.StorageRoot, r.LimaHome, r.CPUs, r.MemoryMiB, r.DiskGiB, r.Machines)
	}
	for _, download := range plan.Downloads {
		status := "download"
		if download.Cached {
			status = "verified cache"
		}
		fmt.Fprintf(output, "  %s (%s, %d bytes): %s\n    SHA-256: %s\n", download.Name, status, download.Bytes, download.URL, download.SHA256)
	}
	for _, check := range plan.Checks {
		fmt.Fprintf(output, "  [%s] %s: %s\n", check.Status, check.Label, check.Detail)
	}
	fmt.Fprintf(output, "  Profile configuration: %s\n", plan.ConfigPath)
}

func confirm(ctx context.Context, o Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.Yes {
		return nil
	}
	if o.Confirm == nil {
		return fmt.Errorf("review the plan and rerun with --yes to approve its actions and downloads")
	}
	if err := o.Confirm(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

func follow(ctx context.Context, service preparation, job prepare.Job, output io.Writer, live bool) error {
	last := ""
	for {
		status := fmt.Sprintf("%s: %s · %s", job.ID, job.Status, job.Phase)
		for _, step := range job.Steps {
			if step.Status == "running" && step.BytesDone > 0 {
				status += fmt.Sprintf(" · %d/%d bytes", step.BytesDone, step.BytesTotal)
			}
		}
		if status != last {
			fmt.Fprintln(output, status)
			last = status
		}
		switch job.Status {
		case "succeeded":
			if job.Result == "ready" {
				fmt.Fprintln(output, "VM hosting is ready in the running AXV desktop.")
			} else {
				fmt.Fprintln(output, "VM configuration is prepared. Start axv serve or axv daemon start --open to activate and verify hosting.")
			}
			return nil
		case "failed", "interrupted", "cancelled":
			message := job.Status
			if job.Error != nil {
				message = job.Error.Message
			}
			if job.CanRetry {
				message += fmt.Sprintf("; retry the same task with axv setup --retry %s", job.ID)
			}
			return errors.New(message)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			if live {
				fmt.Fprintln(output, "Detached. Preparation continues in AXV; run axv setup to follow it.")
			} else {
				fmt.Fprintf(output, "Preparation interrupted; retry task %s after this process exits.\n", job.ID)
			}
			return ctx.Err()
		case <-timer.C:
		}
		var err error
		job, err = service.Job(ctx, job.ID)
		if err != nil {
			return fmt.Errorf("cannot read preparation progress (the task may still be running): %w", err)
		}
	}
}
