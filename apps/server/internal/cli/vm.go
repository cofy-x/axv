package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/compute/ubuntusources"
	"github.com/cofy-x/axv/apps/server/internal/daemon"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type mutationOptions struct {
	json, noWait bool
	timeout      time.Duration
}

func mutationFlags(cmd *cobra.Command, o *mutationOptions) {
	cmd.Flags().BoolVar(&o.json, "json", false, "write machine-readable JSON")
	cmd.Flags().BoolVar(&o.noWait, "no-wait", false, "return after acceptance; the operation continues in the host")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 10*time.Minute, "maximum time to wait; timeout does not cancel the operation")
}

func connectVM(ctx context.Context, home string, diagnostic io.Writer, expected computeclient.Binding) (*computeclient.Client, error) {
	settings, err := app.ResolveCompute(home)
	if err != nil {
		return nil, err
	}
	if settings.Compute.Mode == "disabled" || settings.Compute.Mode == "" {
		return nil, fmt.Errorf("VM hosting is not configured; run axv setup")
	}
	if settings.Compute.Mode == "managed" {
		if settings.Host == nil {
			return nil, fmt.Errorf("managed VM host configuration is unavailable; run axv setup")
		}
		if expected.ConfigFingerprint != "" && expected.ConfigFingerprint != settings.Host.Fingerprint() {
			return nil, fmt.Errorf("the saved SSH configuration belongs to different host settings")
		}
		expected.ConfigFingerprint = settings.Host.Fingerprint()
		if _, err = daemon.EnsureHost(ctx, settings, diagnostic); err != nil {
			return nil, err
		}
	}
	c, err := computeclient.Connect(ctx, settings.Compute.Socket, expected)
	if err != nil && settings.Compute.Mode == "external" {
		return nil, fmt.Errorf("%w; the independent host must be started by its administrator", err)
	}
	return c, err
}

func commandClient(cmd *cobra.Command) (*computeclient.Client, error) {
	return commandClientFor(cmd, computeclient.Binding{})
}

func commandClientFor(cmd *cobra.Command, binding computeclient.Binding) (*computeclient.Client, error) {
	home, err := profile.Home()
	if err != nil {
		return nil, err
	}
	return connectVM(cmd.Context(), home, cmd.ErrOrStderr(), binding)
}

func jsonResult(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func newVM() *cobra.Command {
	cmd := &cobra.Command{Use: "vm", Short: "Manage VMs through the local VM host", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var listJSON bool
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List VMs", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		machines, err := c.Machines(cmd.Context())
		if err != nil {
			return err
		}
		if listJSON {
			return jsonResult(cmd.OutOrStdout(), map[string]any{"machines": machines})
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATE\tREADY\tPROVIDER")
		for _, m := range machines {
			fmt.Fprintf(w, "%s\t%q\t%s\t%t\t%s\n", m.ID, m.Spec.Name, m.Status, m.Ready, m.Spec.Provider)
		}
		return w.Flush()
	}}
	list.Flags().BoolVar(&listJSON, "json", false, "write machine-readable JSON")
	cmd.AddCommand(list)
	var inspectJSON bool
	inspect := &cobra.Command{Use: "inspect <name|id>", Short: "Inspect a VM and its pending configuration", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		m, err := c.Resolve(cmd.Context(), strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}
		return jsonResult(cmd.OutOrStdout(), m)
	}}
	inspect.Flags().BoolVar(&inspectJSON, "json", false, "write machine-readable JSON (also the default for inspect)")
	cmd.AddCommand(inspect, newCreate(), newIP(), newSSH(false), newSSH(true), newOperations(), newPackageSource())
	for _, action := range []string{"start", "stop", "restart", "rm"} {
		cmd.AddCommand(newAction(action))
	}
	for _, resource := range []string{"images", "volumes"} {
		var machineJSON bool
		child := &cobra.Command{Use: resource, Short: "List " + resource, Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := commandClient(cmd)
			if err != nil {
				return err
			}
			defer c.Close()
			if resource == "images" {
				var result struct {
					Images []compute.ImageInfo `json:"images"`
				}
				if err = c.DoJSON(cmd.Context(), "GET", "images", nil, &result); err != nil {
					return err
				}
				if machineJSON {
					return jsonResult(cmd.OutOrStdout(), result)
				}
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				fmt.Fprintln(w, "ID\tNAME\tPROVIDER\tARCH\tSTATE")
				for _, i := range result.Images {
					fmt.Fprintf(w, "%s\t%q\t%s\t%s\t%s\n", i.ID, i.Name, i.Provider, i.Arch, i.Status)
				}
				return w.Flush()
			}
			var result struct {
				Volumes []compute.Volume `json:"volumes"`
			}
			if err = c.DoJSON(cmd.Context(), "GET", "volumes", nil, &result); err != nil {
				return err
			}
			if machineJSON {
				return jsonResult(cmd.OutOrStdout(), result)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tGiB\tRETAIN\tMACHINE\tSTATE")
			for _, v := range result.Volumes {
				fmt.Fprintf(w, "%s\t%q\t%d\t%t\t%s\t%s\n", v.ID, v.Name, v.SizeGiB, v.Retain, v.MachineID, v.Status)
			}
			return w.Flush()
		}}
		child.Flags().BoolVar(&machineJSON, "json", false, "write machine-readable JSON")
		cmd.AddCommand(child)
	}
	return cmd
}

func newCreate() *cobra.Command {
	var o mutationOptions
	var values compute.Spec
	var noStart bool
	var ttl time.Duration
	cmd := &cobra.Command{Use: "create <name>", Short: "Create and start a VM", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if o.timeout <= 0 {
			return usageError(fmt.Errorf("--timeout must be positive"))
		}
		if err := nonemptyFlags(cmd, "image", "kind"); err != nil {
			return err
		}
		name := compute.NormalizeMachineName(args[0])
		if err := compute.ValidateMachineName(name); err != nil {
			return usageError(err)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
		defer cancel()
		cmd.SetContext(ctx)
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		info, err := c.Info(ctx)
		if err != nil {
			return err
		}
		image, err := selectImage(info, values.ImageID)
		if err != nil {
			return usageError(err)
		}
		spec := info.Defaults.Spec
		spec.Name = name
		spec.ImageID = image.ID
		spec.Provider = image.Provider
		spec.Resources.DiskGiB = max(spec.Resources.DiskGiB, image.MinRootGiB)
		f := cmd.Flags()
		if f.Changed("cpus") {
			spec.Resources.CPUs = values.Resources.CPUs
		}
		if f.Changed("memory") {
			spec.Resources.MemoryMiB = values.Resources.MemoryMiB
		}
		if f.Changed("disk") {
			spec.Resources.DiskGiB = values.Resources.DiskGiB
		}
		if f.Changed("project-disk") {
			spec.ProjectGiB = values.ProjectGiB
		}
		if f.Changed("kind") {
			spec.Kind = values.Kind
		}
		if f.Changed("retain-volume") {
			spec.RetainVolume = values.RetainVolume
		}
		if spec.Kind == "task" {
			if !f.Changed("retain-volume") {
				spec.RetainVolume = info.Defaults.TaskRetainVolume
			}
			spec.TTLSeconds = info.Defaults.TaskTTLSeconds
			if f.Changed("ttl") {
				spec.TTLSeconds = int64(ttl / time.Second)
			}
		} else if f.Changed("ttl") {
			return usageError(fmt.Errorf("--ttl requires --kind task"))
		}
		if f.Changed("ttl") && (ttl%time.Second != 0 || ttl < time.Minute || ttl > 30*24*time.Hour) {
			return usageError(fmt.Errorf("--ttl must be 1 minute to 30 days in whole seconds"))
		}
		request := compute.CreateRequest{RequestID: compute.NewID("cli"), Spec: spec, Start: info.Defaults.Start && !noStart}
		if err := compute.ValidateSpec(spec, info.Capacity, image.MinRootGiB); err != nil {
			return usageError(err)
		}
		mutationReceipt(cmd, c, request.RequestID, fmt.Sprintf("new VM %q", spec.Name))
		result, err := c.Create(ctx, request)
		if err != nil {
			return submissionFailure(c, request.RequestID, err)
		}
		return finishMutation(cmd, c, result, o)
	}}
	mutationFlags(cmd, &o)
	f := cmd.Flags()
	f.IntVar(&values.Resources.CPUs, "cpus", 0, "CPU count (host default: 2)")
	f.Int64Var(&values.Resources.MemoryMiB, "memory", 0, "memory in MiB (host default: 1024)")
	f.Int64Var(&values.Resources.DiskGiB, "disk", 0, "root disk in GiB (default: max of 8 and image minimum)")
	f.Int64Var(&values.ProjectGiB, "project-disk", 0, "project disk in GiB (host default: 4)")
	f.StringVar(&values.ImageID, "image", "", "compatible image ID; required if more than one is available")
	f.StringVar(&values.Kind, "kind", "", "development or task (default: development)")
	f.DurationVar(&ttl, "ttl", time.Hour, "task lease duration")
	f.BoolVar(&values.RetainVolume, "retain-volume", false, "retain the project volume (default: true for development, false for task)")
	f.BoolVar(&noStart, "no-start", false, "create the VM without starting it")
	return cmd
}

func selectImage(info compute.Info, id string) (compute.ImageInfo, error) {
	available := map[string]bool{}
	for _, p := range info.Providers {
		available[p.Provider] = p.Available
	}
	var choices []compute.ImageInfo
	for _, image := range info.Images {
		if image.Arch == info.HostArch && available[image.Provider] && (image.Status == "ready" || image.Status == "configured") {
			choices = append(choices, image)
			if id != "" && image.ID == id {
				return image, nil
			}
		}
	}
	if id != "" {
		return compute.ImageInfo{}, fmt.Errorf("image %q is not compatible and ready on this host; run axv vm images", id)
	}
	if len(choices) == 1 {
		return choices[0], nil
	}
	ids := make([]string, 0, len(choices))
	for _, image := range choices {
		ids = append(ids, image.ID)
	}
	if len(ids) == 0 {
		return compute.ImageInfo{}, fmt.Errorf("no compatible image is available; prepare VM hosting or add an image in AXV")
	}
	return compute.ImageInfo{}, fmt.Errorf("choose --image from: %s", strings.Join(ids, ", "))
}

func newAction(action string) *cobra.Command {
	var o mutationOptions
	var yes, force bool
	cmd := &cobra.Command{Use: action + " <name|id>", Short: map[string]string{"start": "Start a VM", "stop": "Stop a VM", "restart": "Restart a VM", "rm": "Delete a VM, honoring its project-volume retention"}[action], Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if o.timeout <= 0 {
			return usageError(fmt.Errorf("--timeout must be positive"))
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
		defer cancel()
		cmd.SetContext(ctx)
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		m, err := c.Resolve(ctx, strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}
		operation := action
		if action == "rm" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Delete VM %q (%s) and its root disk. Project volume %s: retain=%t.\n", m.Spec.Name, m.ID, m.Spec.VolumeID, m.Spec.RetainVolume)
			if err = confirmDelete(cmd, yes); err != nil {
				return err
			}
			operation = "delete"
		}
		if action == "stop" && force {
			operation = "force-stop"
		}
		request := compute.ActionRequest{RequestID: compute.NewID("cli"), Revision: m.Revision, Action: operation}
		mutationReceipt(cmd, c, request.RequestID, m.ID)
		result, err := c.Action(ctx, m.ID, request)
		if err != nil {
			return submissionFailure(c, request.RequestID, err)
		}
		return finishMutation(cmd, c, result, o)
	}}
	mutationFlags(cmd, &o)
	if action == "rm" {
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without a prompt")
	}
	if action == "stop" {
		cmd.Flags().BoolVar(&force, "force", false, "explicitly force-stop, when supported by this provider")
	}
	return cmd
}

func confirmDelete(cmd *cobra.Command, yes bool) error {
	if yes {
		return nil
	}
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return usageError(fmt.Errorf("review the deletion and rerun with --yes to confirm"))
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Type yes to delete: ")
	line, err := confirmationLine(cmd.Context(), f)
	if err != nil && err != io.EOF {
		return err
	}
	if strings.TrimSpace(line) != "yes" {
		return fmt.Errorf("deletion was not confirmed")
	}
	return nil
}

func confirmationLine(ctx context.Context, input io.ReadCloser) (string, error) {
	type result struct {
		line string
		err  error
	}
	read := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(input).ReadString('\n')
		read <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		_ = input.Close()
		return "", ctx.Err()
	case r := <-read:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return r.line, r.err
	}
}

func mutationReceipt(cmd *cobra.Command, c *computeclient.Client, id, target string) {
	home, _ := profile.Home()
	fmt.Fprintf(cmd.ErrOrStderr(), "Profile: %s\nHost: %s  Target: %s\nRequest: %s\n", home, c.Binding.HostID, target, id)
}

func mutationFailure(c *computeclient.Client, id string, err error) error {
	failure := fmt.Errorf("request %s: %w; use this profile with axv vm operation wait --host-id %s --request-id %s before submitting another mutation", id, err, c.Binding.HostID, id)
	if compute.ErrorCode(err) == "invalid_request" {
		return usageError(failure)
	}
	return failure
}

func submissionFailure(c *computeclient.Client, id string, err error) error {
	var response *computeclient.ResponseError
	if errors.As(err, &response) && response.StatusCode >= 400 && response.StatusCode < 500 {
		failure := fmt.Errorf("request %s was rejected: %w", id, err)
		if compute.ErrorCode(err) == "invalid_request" {
			return usageError(failure)
		}
		return failure
	}
	return mutationFailure(c, id, err)
}

func finishMutation(cmd *cobra.Command, c *computeclient.Client, result compute.Result, o mutationOptions) error {
	fmt.Fprintf(cmd.ErrOrStderr(), "Machine: %s  Operation: %s\n", result.Machine.ID, result.Operation.ID)
	var waitErr error
	if !o.noWait {
		observed, err := waitOperation(cmd.Context(), c, result.Operation.ID, false, cmd.ErrOrStderr())
		waitErr = err
		if observed.ID != "" {
			result.Operation = observed
		}
		if cmd.Context().Err() == nil {
			if m, err := c.Machine(cmd.Context(), result.Machine.ID); err == nil {
				result.Machine = m
			}
		}
	}
	if o.json {
		if err := jsonResult(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", result.Machine.ID, result.Operation.ID, result.Operation.Status)
		if result.Machine.Status == "deleted" && result.Machine.Spec.RetainVolume {
			fmt.Fprintf(cmd.OutOrStdout(), "Retained project volume: %s\n", result.Machine.Spec.VolumeID)
		}
	}
	if waitErr != nil {
		return mutationFailure(c, result.Operation.RequestID, waitErr)
	}
	return nil
}

func waitOperation(ctx context.Context, c *computeclient.Client, id string, requestID bool, diagnostic io.Writer) (compute.Operation, error) {
	var last compute.Operation
	status := ""
	for {
		op, err := c.Operation(ctx, id, requestID)
		if err != nil {
			if !requestID || compute.ErrorCode(err) != "not_found" {
				return last, fmt.Errorf("operation outcome is not confirmed: %w", err)
			}
		} else {
			last = op
			if op.Status != status {
				fmt.Fprintf(diagnostic, "%s: %s\n", op.ID, op.Status)
				status = op.Status
			}
			switch op.Status {
			case "succeeded":
				return op, nil
			case "failed":
				return op, fmt.Errorf("operation %s failed: %s", op.ID, op.Error)
			}
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, fmt.Errorf("stopped waiting; the operation was not cancelled and its outcome may still be unknown: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func newOperations() *cobra.Command {
	parent := &cobra.Command{Use: "operation", Short: "Inspect or wait for durable VM operations", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var listJSON bool
	list := &cobra.Command{Use: "list", Short: "List active and recent operations", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		var result struct {
			Operations []compute.Operation `json:"operations"`
		}
		if err = c.DoJSON(cmd.Context(), "GET", "operations", nil, &result); err != nil {
			return err
		}
		if listJSON {
			return jsonResult(cmd.OutOrStdout(), result)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tREQUEST\tMACHINE\tACTION\tSTATE")
		for _, op := range result.Operations {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", op.ID, op.RequestID, op.MachineID, op.Action, op.Status)
		}
		return w.Flush()
	}}
	list.Flags().BoolVar(&listJSON, "json", false, "write machine-readable JSON")
	parent.AddCommand(list)
	for _, action := range []string{"get", "wait"} {
		var requestID string
		var hostID string
		var machineJSON bool
		var timeout time.Duration
		child := &cobra.Command{Use: action + " [operation-id]", Short: action + " for an operation ID or --request-id", Args: arguments(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == (requestID != "") {
				return usageError(fmt.Errorf("supply one operation ID or --request-id"))
			}
			if timeout <= 0 {
				return usageError(fmt.Errorf("--timeout must be positive"))
			}
			id := requestID
			if len(args) == 1 {
				id = args[0]
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			cmd.SetContext(ctx)
			c, err := commandClientFor(cmd, computeclient.Binding{HostID: hostID})
			if err != nil {
				return err
			}
			defer c.Close()
			var op compute.Operation
			if action == "wait" {
				op, err = waitOperation(ctx, c, id, requestID != "", cmd.ErrOrStderr())
			} else {
				op, err = c.Operation(ctx, id, requestID != "")
			}
			if op.ID != "" {
				if outputErr := jsonResult(cmd.OutOrStdout(), op); outputErr != nil {
					return outputErr
				}
			}
			return err
		}}
		child.Flags().StringVar(&requestID, "request-id", "", "original request ID, including after a lost response")
		child.Flags().StringVar(&hostID, "host-id", "", "require the host identity printed in the original request receipt")
		child.Flags().BoolVar(&machineJSON, "json", false, "write machine-readable JSON (also the default)")
		child.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "maximum time to wait; does not cancel an operation")
		parent.AddCommand(child)
	}
	return parent
}

func newIP() *cobra.Command {
	var machineJSON bool
	cmd := &cobra.Command{Use: "ip <name|id>", Short: "Show current guest IP addresses; host reachability is separate", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		m, err := c.Resolve(cmd.Context(), strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}
		var result guestproto.NetworkInfo
		if err = c.DoJSON(cmd.Context(), "GET", "machines/"+m.ID+"/network", nil, &result); err != nil {
			return err
		}
		if machineJSON {
			return jsonResult(cmd.OutOrStdout(), result)
		}
		if len(result.Addresses) == 0 {
			return fmt.Errorf("VM has no current guest IP addresses")
		}
		for _, address := range result.Addresses {
			fmt.Fprintln(cmd.OutOrStdout(), address.Address)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Guest addresses are not necessarily reachable from this computer. Use axv vm ssh for guest access.")
		return nil
	}}
	cmd.Flags().BoolVar(&machineJSON, "json", false, "include interface, boot identity, observation time and reachability")
	return cmd
}

func newPackageSource() *cobra.Command {
	var o mutationOptions
	var source string
	cmd := &cobra.Command{Use: "package-source <name|id>", Short: "Explicitly configure Ubuntu 24.04 package sources in one running Lima VM", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := ubuntusources.Validate(source); err != nil {
			return usageError(err)
		}
		if o.timeout <= 0 {
			return usageError(fmt.Errorf("--timeout must be positive"))
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
		defer cancel()
		cmd.SetContext(ctx)
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		m, err := c.Resolve(ctx, strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}
		request := compute.ActionRequest{RequestID: compute.NewID("cli"), Revision: m.Revision, Action: "package-source", PackageSource: source}
		fmt.Fprintf(cmd.ErrOrStderr(), "Configure %s package sources in VM %q only; retain backups, do not update packages.\n", source, m.Spec.Name)
		mutationReceipt(cmd, c, request.RequestID, m.ID)
		result, err := c.Action(ctx, m.ID, request)
		if err != nil {
			return submissionFailure(c, request.RequestID, err)
		}
		return finishMutation(cmd, c, result, o)
	}}
	mutationFlags(cmd, &o)
	cmd.Flags().StringVar(&source, "source", "", "explicit selection: official or aliyun (Ubuntu 24.04 arm64/amd64)")
	return cmd
}
