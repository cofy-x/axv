package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/spf13/cobra"
)

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
	cmd.AddCommand(inspect, newCreate(), newExec(), newIP(), newSSH(false), newSSH(true), newOperations(), newPackageSource(), newVMProject(), newVMVolumes())
	for _, action := range []string{"start", "stop", "restart", "rm"} {
		cmd.AddCommand(newAction(action))
	}
	var imagesJSON bool
	images := &cobra.Command{Use: "images", Short: "List images", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		var result struct {
			Images []compute.ImageInfo `json:"images"`
		}
		if err = c.DoJSON(cmd.Context(), "GET", "images", nil, &result); err != nil {
			return err
		}
		if imagesJSON {
			return jsonResult(cmd.OutOrStdout(), result)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tPROVIDER\tARCH\tSTATE")
		for _, i := range result.Images {
			fmt.Fprintf(w, "%s\t%q\t%s\t%s\t%s\n", i.ID, i.Name, i.Provider, i.Arch, i.Status)
		}
		return w.Flush()
	}}
	images.Flags().BoolVar(&imagesJSON, "json", false, "write machine-readable JSON")
	cmd.AddCommand(images)
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
