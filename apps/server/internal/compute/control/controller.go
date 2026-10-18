// Package control owns durable compute intent, recovery, and provider execution.
// Machine, volume, and image state share one transactional store.
package control

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
)

type Service struct {
	Logger  *diagnostics.Logger
	config  compute.Config
	store   *Store
	drivers map[string]compute.Driver
	onReady func()
	ready   atomic.Bool
}

// Ready reports completed recovery, independently of provider probing.
func (s *Service) Ready() bool { return s.ready.Load() }

func (s *Service) Run(ctx context.Context) error {
	if err := s.store.Recover(ctx); err != nil {
		return err
	}
	s.Logger.Info("compute.recovered")
	s.ready.Store(true)
	defer s.ready.Store(false)
	if s.onReady != nil {
		s.onReady()
	}
	var wg sync.WaitGroup
	for i := 0; i < s.config.Capacity.Workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.worker(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); s.monitor(ctx) }()
	<-ctx.Done()
	wg.Wait()
	return nil
}

func (s *Service) worker(ctx context.Context) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		op, found, err := s.store.Claim(ctx)
		if err != nil {
			log.Printf("compute claim: %v", err)
		}
		if found && err == nil {
			logger := s.Logger.With("operationId", op.ID, "requestId", op.RequestID, "machineId", op.MachineID, "volumeId", op.VolumeID, "resourceId", op.ResourceID, "resourceKind", op.ResourceKind, "action", op.Action)
			logger.Info("operation.started")
			work, cancel := context.WithTimeout(diagnostics.Context(ctx, logger), 10*time.Minute)
			observation, operationErr := s.perform(work, op)
			cancel()
			if operationErr != nil {
				logger.Error("operation.execution_failed", operationErr, "status", observation.Status)
			} else {
				logger.Info("operation.execution_completed", "status", observation.Status)
			}
			if ctx.Err() != nil {
				return
			} // Keep running record for recovery.
			if err := s.store.Finish(ctx, op, observation, operationErr); err != nil {
				logger.Error("operation.persistence_failed", err)
				// A failed commit is retried without replaying provider side effects.
				for {
					select {
					case <-ctx.Done():
						return
					case <-tick.C:
					}
					if s.store.Finish(ctx, op, observation, operationErr) == nil {
						break
					}
				}
			}
			if operationErr != nil {
				logger.Error("operation.failed", operationErr, "status", observation.Status)
			} else {
				logger.Info("operation.completed", "status", observation.Status)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) perform(ctx context.Context, op operationRecord) (compute.Observation, error) {
	phase := func(value string) error { return s.phase(ctx, op, value) }
	if op.ResourceKind == "image" {
		return s.performImage(ctx, op)
	}
	if op.VolumeID != "" {
		return s.performVolume(ctx, op)
	}
	m, err := s.store.Machine(ctx, op.MachineID)
	if err != nil {
		return compute.Observation{}, err
	}
	d := s.drivers[m.Spec.Provider]
	if d == nil {
		return compute.Observation{Status: "unknown"}, compute.Problem("unsupported", "Machine provider is not configured.")
	}
	var v compute.Volume
	if m.Spec.VolumeID != "" {
		v, err = s.store.Volume(ctx, m.Spec.VolumeID)
		if err != nil {
			return compute.Observation{Status: "unknown"}, err
		}
	}
	var guestInitializationError error
	switch op.Action {
	case "package-source":
		configurer, ok := d.(compute.PackageSourceConfigurer)
		if !ok {
			err = compute.Problem("unsupported", "This provider does not support package source maintenance.")
		} else if err = phase("configuring-package-source"); err == nil {
			err = configurer.ConfigurePackageSource(ctx, m, op.PackageSource)
		}
	case "create", "start", "restart":
		var image compute.Image
		image, err = s.resolveImage(ctx, m.Spec.ImageID, m.Spec.Provider)
		resumeStart := op.Phase == "preparing" || op.Phase == "starting"
		if err == nil && op.Action == "restart" && !resumeStart {
			err = phase("stopping")
			if err == nil {
				err = d.Stop(ctx, m)
			}
		}
		if err == nil {
			err = phase("preparing")
			if err == nil {
				err = d.Ensure(ctx, m, image, v)
			}
		}
		if err == nil && m.Desired == "running" {
			if m.ExpiresAt != nil {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, *m.ExpiresAt)
				defer cancel()
			}
			err = phase("starting")
			if err == nil {
				err = d.Start(ctx, m, image, v)
			}
			if err == nil && image.Guest != nil && image.Guest.Compatible() == nil {
				if initializer, ok := d.(compute.GuestPreparer); ok {
					guestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					guestErr := initializer.PrepareGuest(guestCtx, m, image, v)
					cancel()
					if guestErr != nil {
						guestInitializationError = guestErr
						diagnostics.FromContext(ctx).Error("guest.initialization_failed", guestErr, "machineId", m.ID)
					}
				}
			}
		}
	case "stop":
		err = phase("stopping")
		if err == nil {
			err = d.Stop(ctx, m)
		}
	case "force-stop":
		stopper, ok := d.(compute.ForceStopper)
		if !ok {
			err = compute.Problem("unsupported", "This provider does not expose an explicit force-stop operation.")
		} else if err = phase("forcing-stop"); err == nil {
			err = stopper.ForceStop(ctx, m)
		}
	case "configure":
		if op.Configuration == nil {
			err = compute.Problem("invalid_request", "The operation has no configuration.")
		} else if err = phase("applying-configuration"); err == nil && op.ResourcesChanged {
			configurer, ok := d.(compute.Configurer)
			if !ok {
				err = compute.Problem("unsupported", "This provider does not support configuration changes.")
			} else {
				err = configurer.Configure(ctx, m, *op.Configuration, v)
			}
		}
	case "delete":
		err = phase("stopping")
		if err == nil {
			err = d.Stop(ctx, m)
		}
		if err == nil {
			err = phase("deleting-runtime")
			if err == nil {
				err = d.Delete(ctx, m)
			}
		}
		if err == nil && v.ID != "" && !v.Retain {
			err = d.DeleteVolume(ctx, v)
		}
	}
	observeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o, inspectErr := d.Inspect(observeCtx, m)
	if inspectErr != nil {
		o = compute.Observation{Status: "unknown", Reason: inspectErr.Error()}
	}
	if op.Action == "delete" && err == nil {
		return compute.Observation{Status: "deleted"}, nil
	}
	if err != nil {
		return o, err
	}
	if inspectErr != nil {
		return o, inspectErr
	}
	if m.Desired == "running" && o.Status != "running" {
		return o, fmt.Errorf("VM did not remain running after startup")
	}
	if m.Desired == "stopped" && o.Status != "stopped" && o.Status != "absent" {
		return o, fmt.Errorf("VM shutdown is not yet confirmed")
	}
	if m.Desired == "running" {
		s.observeGuest(observeCtx, m, d, &o)
		if guestInitializationError != nil && !o.Ready {
			o.GuestReason = guestInitializationError.Error()
		}
	}

	return o, nil
}

func (s *Service) Update(ctx context.Context, id string, req compute.UpdateRequest) (compute.Result, error) {
	m, err := s.store.Machine(ctx, id)
	if err != nil {
		return compute.Result{}, err
	}
	if req.Spec.Resources != m.Spec.Resources || (m.PendingSpec != nil && m.PendingSpec.Resources != m.Spec.Resources) {
		if _, ok := s.drivers[m.Spec.Provider].(compute.Configurer); !ok {
			return compute.Result{}, compute.Problem("unsupported", "This provider does not support resource edits.")
		}
	}
	c, err := s.currentConfig(ctx)
	if err != nil {
		return compute.Result{}, err
	}
	return s.store.Update(ctx, c, id, req)
}

func (s *Service) monitor(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		machines, err := s.store.Machines(ctx)
		if err != nil {
			log.Printf("compute observation: %v", err)
		}

		var group sync.WaitGroup
		slots := make(chan struct{}, s.config.Capacity.Workers)
		for _, m := range machines {
			select {
			case <-ctx.Done():
				group.Wait()
				return
			case slots <- struct{}{}:
			}
			group.Add(1)
			go func() { defer group.Done(); defer func() { <-slots }(); s.observeMachine(ctx, m) }()
		}
		group.Wait()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) observeMachine(ctx context.Context, m compute.Machine) {

	if m.ExpiresAt != nil && !time.Now().Before(*m.ExpiresAt) {
		_, err := s.store.Action(ctx, s.config.Capacity, m.ID, compute.ActionRequest{RequestID: fmt.Sprintf("ttl-%s-%d", m.ID, m.Revision), Revision: m.Revision, Action: "delete"})
		if err != nil && compute.ErrorCode(err) != "operation_conflict" && compute.ErrorCode(err) != "revision_conflict" {
			log.Printf("task expiry %s: %v", m.ID, err)
		}
		return
	}
	d := s.drivers[m.Spec.Provider]
	if d == nil {
		return
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	o, err := d.Inspect(inspectCtx, m)
	if err != nil {
		o = compute.Observation{Status: "unknown", Reason: err.Error()}
	} else if o.Status == "running" {
		s.observeGuest(inspectCtx, m, d, &o)
	}
	cancel()
	if err := s.store.Observe(ctx, m, o); err != nil {
		s.Logger.Error("machine.observation_failed", err, "machineId", m.ID)
	} else if m.Status != o.Status {
		s.Logger.Info("machine.state_changed", "machineId", m.ID, "status", o.Status, "reason", o.Reason)
	}
}

// observeGuest keeps enhanced readiness independent from provider lifecycle.
func (s *Service) observeGuest(ctx context.Context, m compute.Machine, d compute.Driver, o *compute.Observation) {
	o.Ready = false
	o.GuestCapabilities = nil
	image, err := s.resolveImage(ctx, m.Spec.ImageID, m.Spec.Provider)
	if err != nil {
		o.GuestState = "unavailable"
		return
	}
	if image.Guest == nil {
		o.GuestState = "not-installed"
		o.GuestReason = "This image provides basic VM management without AXV guest enhancements."
		return
	}
	if err = image.Guest.Compatible(); err != nil {
		o.GuestState = "incompatible"
		o.GuestReason = err.Error()
		return
	}
	health, err := compute.Health(ctx, m, d)
	if err != nil || !health.Ready {
		o.GuestState = "unavailable"
		if compute.ErrorCode(err) == "guest_incompatible" {
			o.GuestState = "incompatible"
			o.GuestReason = err.Error()
			return
		}
		if compute.ErrorCode(err) == "guest_identity_mismatch" {
			o.GuestReason = err.Error()
			return
		}
		if err != nil {
			o.GuestReason = m.GuestReason
			if o.GuestReason == "" {
				o.GuestReason = "Guest connection unavailable; verify the preinstalled image services and dependencies."
			}
		} else {
			o.GuestReason = health.Reason
		}
		return
	}
	o.Ready, o.GuestState = true, "ready"
	for _, capability := range image.Guest.Capabilities {
		if guestimage.Has(health.Capabilities, capability) {
			o.GuestCapabilities = append(o.GuestCapabilities, capability)
		}
	}
}

func (s *Service) RequireGuest(ctx context.Context, m compute.Machine, capability string) error {
	d := s.drivers[m.Spec.Provider]
	if d == nil {
		return compute.Problem("unsupported", "The VM provider is unavailable.")
	}
	var o compute.Observation
	s.observeGuest(ctx, m, d, &o)
	if !o.Ready || (capability != "" && !guestimage.Has(o.GuestCapabilities, capability)) {
		required := capability
		if required == "" {
			required = "readiness"
		}
		return compute.Problem("unsupported", "AXV guest %s is unavailable (%s). %s Select a compatible image with the required capability; basic VM management remains available.", required, o.GuestState, o.GuestReason)
	}
	return nil
}

func (s *Service) phase(ctx context.Context, op operationRecord, value string) error {
	err := s.store.Phase(ctx, op.ID, value)
	if err == nil {
		diagnostics.FromContext(ctx).Info("operation.phase", "phase", value)
	}
	return err
}
