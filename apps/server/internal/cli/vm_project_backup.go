package cli

import (
	"fmt"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/spf13/cobra"
)

func newVMProjectBackup() *cobra.Command {
	parent := &cobra.Command{Use: "backup", Short: "Create offline project-volume backups and restore independent projects"}
	parent.AddCommand(newVMProjectBackupCommand("list"), newVMProjectBackupCommand("create"), newVMProjectBackupCommand("restore"), newVMProjectBackupCommand("bind"))
	return parent
}

func newVMProjectBackupCommand(action string) *cobra.Command {
	var host, requestID, name, machine, sourceProject, volume string
	var revision, machineRevision, projectRevision, volumeRevision int64
	var machineJSON bool
	use := action
	args := cobra.NoArgs
	if action != "list" {
		use += " <id>"
		args = cobra.ExactArgs(1)
	}
	cmd := &cobra.Command{Use: use, Short: map[string]string{
		"list":    "List offline whole-volume project backups",
		"create":  "Back up a project after explicitly stopping its VM and reconciling tasks",
		"restore": "Restore a completed backup to a new retained volume",
		"bind":    "Verify restored Git/worktree paths and register a separate project identity",
	}[action], Args: arguments(args), RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(host) == "" {
			return usageError(fmt.Errorf("--host-id is required; retain the original host for recovery"))
		}
		if action != "list" {
			if err := compute.ValidateRequestID(requestID); err != nil {
				return usageError(err)
			}
		}
		if action == "create" && (machine == "" || machineRevision < 1 || projectRevision < 1 || volumeRevision < 1 || name == "") {
			return usageError(fmt.Errorf("supply --machine, --machine-revision, --project-revision, --volume-revision and --name from the reviewed stopped environment"))
		}
		if action == "restore" && (revision < 1 || name == "") {
			return usageError(fmt.Errorf("--revision and --name are required"))
		}
		if action == "bind" && (machine == "" || sourceProject == "" || volume == "" || volumeRevision < 1) {
			return usageError(fmt.Errorf("--machine, --source-project, --volume and --volume-revision are required"))
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: host})
		if err != nil {
			return err
		}
		defer c.Close()
		if action != "list" {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Host: %s  Request: %s; preserve this request and its payload for recovery.\n", host, requestID); err != nil {
				return err
			}
		}
		switch action {
		case "list":
			result, err := c.ProjectBackups(cmd.Context())
			if err != nil {
				return err
			}
			return jsonResult(cmd.OutOrStdout(), map[string]any{"backups": result})
		case "create":
			result, err := c.BackupProject(cmd.Context(), args[0], compute.BackupProjectRequest{RequestID: requestID, ProjectRevision: projectRevision, VolumeRevision: volumeRevision, MachineID: machine, MachineRevision: machineRevision, Name: name})
			if err != nil {
				return err
			}
			return jsonResult(cmd.OutOrStdout(), result)
		case "restore":
			result, err := c.RestoreProjectVolume(cmd.Context(), args[0], compute.RestoreProjectVolumeRequest{RequestID: requestID, Revision: revision, Name: name})
			if err != nil {
				return err
			}
			return jsonResult(cmd.OutOrStdout(), result)
		default:
			result, err := c.BindRestoredProject(cmd.Context(), args[0], compute.BindRestoredProjectRequest{RequestID: requestID, SourceProjectID: sourceProject, VolumeID: volume, VolumeRevision: volumeRevision, MachineID: machine, Name: name})
			if err != nil {
				return err
			}
			return jsonResult(cmd.OutOrStdout(), map[string]any{"project": result})
		}
	}}
	f := cmd.Flags()
	f.StringVar(&host, "host-id", "", "original host identity (required)")
	f.BoolVar(&machineJSON, "json", false, "write machine-readable JSON (also the default)")
	if action != "list" {
		f.StringVar(&requestID, "request-id", "", "unique request identity; retain original payload for recovery (required)")
		f.StringVar(&name, "name", "", "new backup, volume or project display name")
	}
	if action == "create" || action == "bind" {
		f.StringVar(&machine, "machine", "", "explicit immutable VM ID")
		f.Int64Var(&volumeRevision, "volume-revision", 0, "reviewed source/restored volume revision")
	}
	if action == "create" {
		f.Int64Var(&machineRevision, "machine-revision", 0, "reviewed stopped VM revision")
		f.Int64Var(&projectRevision, "project-revision", 0, "reviewed source project revision")
	}
	if action == "restore" {
		f.Int64Var(&revision, "revision", 0, "reviewed completed backup revision")
	}
	if action == "bind" {
		f.StringVar(&sourceProject, "source-project", "", "original project ID in the backup manifest")
		f.StringVar(&volume, "volume", "", "independent restored volume ID")
	}
	return cmd
}
