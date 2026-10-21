package cli

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/spf13/cobra"
)

func newVMProject() *cobra.Command {
	parent := &cobra.Command{Use: "project", Short: "Register and access projects on retained VM volumes", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var listJSON bool
	list := &cobra.Command{Use: "list", Short: "List persistent VM projects", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		projects, err := c.Projects(cmd.Context())
		if err != nil {
			return err
		}
		if listJSON {
			return jsonResult(cmd.OutOrStdout(), map[string]any{"projects": projects})
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tVOLUME\tROOT")
		for _, project := range projects {
			fmt.Fprintf(w, "%s\t%q\t%s\t%q\n", project.ID, project.Name, project.VolumeID, project.Root)
		}
		return w.Flush()
	}}
	list.Flags().BoolVar(&listJSON, "json", false, "write machine-readable JSON")
	var bindingJSON bool
	binding := &cobra.Command{Use: "binding <project-id>", Short: "Capture the current VM binding for explicit later file operations", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		result, err := c.ProjectBinding(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	binding.Flags().BoolVar(&bindingJSON, "json", false, "write machine-readable JSON (also the default)")
	parent.AddCommand(list, newRegisterVMProject(), binding, newVMProjectTask())
	for _, action := range []string{"files", "read", "write", "mkdir"} {
		parent.AddCommand(newVMProjectFile(action))
	}
	return parent
}

func newRegisterVMProject() *cobra.Command {
	var machine, volumeID, root, name, requestID, hostID string
	var volumeRevision int64
	var machineJSON bool
	cmd := &cobra.Command{Use: "register", Short: "Register an existing directory on a retained attached project volume", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		for _, value := range []string{machine, volumeID, root, name, requestID, hostID} {
			if !utf8.ValidString(value) {
				return usageError(fmt.Errorf("VM project arguments must be valid UTF-8"))
			}
		}
		if err := compute.ValidateProjectRoot(root); err != nil {
			return usageError(err)
		}
		if strings.TrimSpace(machine) == "" || strings.TrimSpace(volumeID) == "" {
			return usageError(fmt.Errorf("--machine and --volume are required"))
		}
		if err := nonemptyFlags(cmd, "machine", "volume", "name", "request-id", "host-id"); err != nil {
			return err
		}
		if cmd.Flags().Changed("volume-revision") && volumeRevision < 1 {
			return usageError(fmt.Errorf("--volume-revision must be positive"))
		}
		replay := cmd.Flags().Changed("host-id") || cmd.Flags().Changed("volume-revision")
		if replay && (hostID == "" || !cmd.Flags().Changed("volume-revision") || requestID == "" || !compute.IsMachineID(machine)) {
			return usageError(fmt.Errorf("registration recovery requires --host-id, --volume-revision, --request-id and the original immutable VM ID in --machine"))
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: hostID})
		if err != nil {
			return err
		}
		defer c.Close()
		machineID := machine
		// A committed receipt remains recoverable after its VM is gone. Replay
		// submits only the original payload to its original host.
		if !replay {
			m, err := c.Resolve(cmd.Context(), machine)
			if err != nil {
				return err
			}
			machineID = m.ID
			var inventory struct {
				Volumes []compute.Volume `json:"volumes"`
			}
			if err = c.DoJSON(cmd.Context(), "GET", "volumes", nil, &inventory); err != nil {
				return err
			}
			for _, candidate := range inventory.Volumes {
				if candidate.ID == volumeID {
					volumeRevision = candidate.Revision
					break
				}
			}
			if volumeRevision < 1 {
				return fmt.Errorf("project volume %q was not found", volumeID)
			}
		}
		if requestID == "" {
			requestID = compute.NewID("cli")
		}
		request := compute.RegisterProjectRequest{RequestID: requestID, MachineID: machineID, VolumeID: volumeID, VolumeRevision: volumeRevision, Root: root, Name: name}
		fmt.Fprintf(cmd.ErrOrStderr(), "Host: %s  Machine: %s  Volume: %s  Volume revision: %d  Request: %s\n", c.Binding.HostID, machineID, volumeID, volumeRevision, requestID)
		project, err := c.RegisterProject(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("project registration request %s: %w; inspect axv vm project list --json before replaying the original registration", requestID, err)
		}
		return jsonResult(cmd.OutOrStdout(), map[string]any{"project": project})
	}}
	f := cmd.Flags()
	f.StringVar(&machine, "machine", "", "running VM name or ID")
	f.StringVar(&volumeID, "volume", "", "exact retained volume ID attached to that VM")
	f.StringVar(&hostID, "host-id", "", "original host identity for recovery; requires original VM ID, volume revision and request ID")
	f.Int64Var(&volumeRevision, "volume-revision", 0, "original volume revision for recovery with --host-id and --request-id (default: current inventory revision)")
	f.StringVar(&root, "root", "", "existing directory relative to the mounted volume (default: the volume root)")
	f.StringVar(&name, "name", "", "optional project display name")
	f.StringVar(&requestID, "request-id", "", "registration identity; preserve it when recovering a lost response")
	f.BoolVar(&machineJSON, "json", false, "write machine-readable JSON (also the default)")
	return cmd
}

func readProjectBinding(filename, projectID string) (compute.ProjectBinding, error) {
	if strings.TrimSpace(filename) == "" {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("--binding-file is required; capture it explicitly with axv vm project binding %s", projectID))
	}
	f, err := os.Open(filename)
	if err != nil {
		return compute.ProjectBinding{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil {
		return compute.ProjectBinding{}, err
	}
	if len(data) > 64*1024 {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("binding file exceeds 64 KiB"))
	}
	if !jsonunicode.Valid(data) {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("binding file requires valid UTF-8 and complete Unicode surrogate pairs"))
	}
	var result computeclient.ProjectBindingResult
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("invalid binding file: %w", err))
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("binding file must contain one JSON object"))
	}
	b := result.Binding
	if result.Project.ID != projectID || b.ProjectID != projectID || b.HostID == "" || b.VolumeID == "" || b.MachineID == "" || b.BootID == "" || b.InstanceID == "" || b.ProjectRevision < 1 || b.VolumeRevision < 1 {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("binding file must contain the complete original binding for project %s", projectID))
	}
	return b, nil
}

func newVMProjectFile(action string) *cobra.Command {
	var bindingFile, contentFile, expected, cursor string
	var create, machineJSON bool
	var limit int
	cmd := &cobra.Command{Use: action + " <project-id> <path>", Short: map[string]string{"files": "List a VM project directory", "read": "Read a VM project UTF-8 file and its version", "write": "Write a VM project file with an explicit version check", "mkdir": "Create one VM project directory"}[action], Args: arguments(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if !utf8.ValidString(args[0]) || !utf8.ValidString(cursor) {
			return usageError(fmt.Errorf("VM project arguments must be valid UTF-8"))
		}
		if err := compute.ValidateProjectRoot(args[1]); err != nil {
			return usageError(err)
		}
		if action != "files" && args[1] == "" {
			return usageError(fmt.Errorf("a nonempty project-relative file path is required"))
		}
		binding, err := projectBindingForCommand(cmd, bindingFile, args[0])
		if err != nil {
			return err
		}
		request := compute.ProjectFileRequest{Binding: binding, Path: args[1]}
		if action == "files" {
			if limit < 0 || limit > 200 {
				return usageError(fmt.Errorf("--limit must be 1–200, or zero for the service default"))
			}
			request.Limit, request.Cursor = limit, cursor
		}
		if action == "write" {
			if create == (expected != "") {
				return usageError(fmt.Errorf("choose exactly one of --create or --expected-sha256"))
			}
			if !create {
				if digest, err := hex.DecodeString(expected); err != nil || len(digest) != 32 || strings.ToLower(expected) != expected {
					return usageError(fmt.Errorf("--expected-sha256 must be a lowercase SHA-256 digest"))
				}
			}
			input := cmd.InOrStdin()
			if contentFile != "-" {
				file, err := os.Open(contentFile)
				if err != nil {
					return err
				}
				defer file.Close()
				input = file
			}
			content, err := io.ReadAll(io.LimitReader(input, protocol.MaxProjectFileSize+1))
			if err != nil {
				return err
			}
			if len(content) > protocol.MaxProjectFileSize || !utf8.Valid(content) {
				return usageError(fmt.Errorf("content must be UTF-8 and at most 1 MiB"))
			}
			request.Content, request.ExpectedSHA256 = string(content), &expected
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer c.Close()
		var result any
		switch action {
		case "files":
			result, err = c.ProjectFiles(cmd.Context(), args[0], request)
		case "read":
			result, err = c.ReadProjectFile(cmd.Context(), args[0], request)
		case "write":
			result, err = c.WriteProjectFile(cmd.Context(), args[0], request)
		case "mkdir":
			result, err = c.MkdirProject(cmd.Context(), args[0], request)
		}
		if err != nil {
			return err
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	f := cmd.Flags()
	f.StringVar(&bindingFile, "binding-file", "", "original JSON from axv vm project binding; never refreshed automatically")
	f.BoolVar(&machineJSON, "json", false, "write machine-readable JSON (also the default)")
	if action == "files" {
		f.IntVar(&limit, "limit", 0, "directory page size (zero uses the service default)")
		f.StringVar(&cursor, "cursor", "", "next cursor returned by the original listing")
	}
	if action == "write" {
		f.StringVar(&contentFile, "file", "-", "UTF-8 content file, or '-' to read stdin")
		f.StringVar(&expected, "expected-sha256", "", "exact SHA-256 returned by the original file read")
		f.BoolVar(&create, "create", false, "create a new file only; fail if it already exists")
	}
	return cmd
}
