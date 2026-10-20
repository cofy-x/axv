package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/spf13/cobra"
)

type projectTaskHost struct {
	hostID      string
	bindingFile string
}

func (o *projectTaskHost) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.hostID, "host-id", "", "original host identity; never replaced automatically")
	cmd.Flags().StringVar(&o.bindingFile, "binding-file", "", "original project binding JSON; queries use its host identity")
	cmd.Flags().Bool("json", false, "write machine-readable JSON (also the default)")
}

func (o projectTaskHost) binding(projectID string, required bool) (compute.ProjectBinding, error) {
	if !utf8.ValidString(projectID) || !utf8.ValidString(o.hostID) {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("VM project task identities must be valid UTF-8"))
	}
	if o.bindingFile != "" {
		binding, err := readProjectBinding(o.bindingFile, projectID)
		if err != nil {
			return binding, err
		}
		if o.hostID != "" && o.hostID != binding.HostID {
			return binding, usageError(fmt.Errorf("--host-id must match the original binding file"))
		}
		return binding, nil
	}
	if required {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("--binding-file is required; capture the intended environment explicitly with axv vm project binding %s", projectID))
	}
	if strings.TrimSpace(o.hostID) == "" {
		return compute.ProjectBinding{}, usageError(fmt.Errorf("--host-id or --binding-file is required to select the original host"))
	}
	return compute.ProjectBinding{HostID: o.hostID}, nil
}

func newVMProjectTask() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Run and recover persistent tasks inside a VM project", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	cmd.AddCommand(newSubmitVMProjectTask(), newRetryVMProjectTask(), newCancelVMProjectTask(), newVMProjectTaskLogs())
	for _, action := range []string{"list", "inspect", "receipt"} {
		cmd.AddCommand(newVMProjectTaskQuery(action))
	}
	return cmd
}

func taskIdentities(args ...string) error {
	for _, value := range args {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return usageError(fmt.Errorf("supply the exact nonempty task identities as valid UTF-8 without control characters"))
		}
	}
	return nil
}

func taskRequestID(cmd *cobra.Command, requestID string) (string, error) {
	if err := nonemptyFlags(cmd, "request-id"); err != nil {
		return "", err
	}
	if requestID == "" {
		return compute.NewID("cli"), nil
	}
	if !utf8.ValidString(requestID) {
		return "", usageError(fmt.Errorf("request ID must be valid UTF-8"))
	}
	if err := compute.ValidateRequestID(requestID); err != nil {
		return "", usageError(err)
	}
	return requestID, nil
}

func taskReceipt(cmd *cobra.Command, projectID, requestID string, binding compute.ProjectBinding) error {
	_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Host: %s  Project: %s  VM: %s  Boot: %s  Instance: %s  Request: %s\n", binding.HostID, projectID, binding.MachineID, binding.BootID, binding.InstanceID, requestID)
	if err != nil {
		return fmt.Errorf("write task recovery identity before submission: %w", err)
	}
	return nil
}

func taskSubmissionError(projectID, requestID, hostID string, err error) error {
	return fmt.Errorf("project task request %s: %w; inspect axv vm project task receipt %s %s --host-id %s before submitting more work", requestID, err, projectID, requestID, hostID)
}

func newSubmitVMProjectTask() *cobra.Command {
	var host projectTaskHost
	var requestID string
	spec := compute.NormalizeTaskSpec(compute.TaskSpec{})
	timeout := time.Duration(spec.Limits.TimeoutSeconds) * time.Second
	cmd := &cobra.Command{Use: "submit <project-id> [flags] -- <command> [args...]", Short: "Submit one persistent task to an explicitly captured VM binding", Args: arguments(cobra.MinimumNArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() != 1 {
			return usageError(fmt.Errorf("supply the project ID before -- and the complete command argv after --"))
		}
		if err := taskIdentities(args[0]); err != nil {
			return err
		}
		if timeout%time.Second != 0 || timeout < time.Second || timeout > 24*time.Hour {
			return usageError(fmt.Errorf("--timeout must be a whole number of seconds from 1s to 24h"))
		}
		spec.Argv, spec.Limits.TimeoutSeconds = args[1:], int(timeout/time.Second)
		if err := compute.ValidateTaskSpec(spec); err != nil {
			return usageError(err)
		}
		binding, err := host.binding(args[0], true)
		if err != nil {
			return err
		}
		requestID, err = taskRequestID(cmd, requestID)
		if err != nil {
			return err
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer c.Close()
		if err := taskReceipt(cmd, args[0], requestID, binding); err != nil {
			return err
		}
		result, err := c.SubmitProjectTask(cmd.Context(), args[0], compute.SubmitProjectTaskRequest{RequestID: requestID, Binding: binding, Spec: spec})
		if err != nil {
			return taskSubmissionError(args[0], requestID, binding.HostID, err)
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	host.flags(cmd)
	f := cmd.Flags()
	f.StringVar(&requestID, "request-id", "", "durable submission identity; preserve it after an uncertain response")
	f.StringVar(&spec.Directory, "directory", "", "existing directory relative to the registered project root")
	f.DurationVar(&timeout, "timeout", timeout, "task wall time limit, 1s–24h; independent of CLI connection lifetime")
	f.IntVar(&spec.Limits.CPUs, "cpus", spec.Limits.CPUs, "per-attempt CPU limit")
	f.Int64Var(&spec.Limits.MemoryMiB, "memory", spec.Limits.MemoryMiB, "per-attempt memory limit in MiB")
	f.IntVar(&spec.Limits.MaxProcesses, "max-processes", spec.Limits.MaxProcesses, "maximum processes in the attempt")
	f.Int64Var(&spec.Limits.MaxLogBytes, "max-log-bytes", spec.Limits.MaxLogBytes, "retained combined stdout/stderr capacity in bytes")
	return cmd
}

func newVMProjectTaskQuery(action string) *cobra.Command {
	var host projectTaskHost
	count, use := 2, action+" <project-id> <task-id>"
	if action == "list" {
		count, use = 1, "list <project-id>"
	} else if action == "receipt" {
		use = "receipt <project-id> <request-id>"
	}
	cmd := &cobra.Command{Use: use, Short: map[string]string{"list": "List persistent project tasks", "inspect": "Inspect a task and its attempt history", "receipt": "Recover the durable result of an original submit or retry request"}[action], Args: arguments(cobra.ExactArgs(count)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := taskIdentities(args...); err != nil {
			return err
		}
		binding, err := host.binding(args[0], false)
		if err != nil {
			return err
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer c.Close()
		var result any
		switch action {
		case "list":
			tasks, listErr := c.ProjectTasks(cmd.Context(), args[0])
			result, err = map[string]any{"tasks": tasks}, listErr
		case "inspect":
			result, err = c.ProjectTask(cmd.Context(), args[0], args[1])
		case "receipt":
			result, err = c.ProjectTaskByRequest(cmd.Context(), args[0], args[1])
		}
		if err != nil {
			return err
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	host.flags(cmd)
	return cmd
}

func newRetryVMProjectTask() *cobra.Command {
	var host projectTaskHost
	var requestID string
	cmd := &cobra.Command{Use: "retry <project-id> <task-id>", Short: "Create another attempt of the original task spec using an explicit VM binding", Args: arguments(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := taskIdentities(args...); err != nil {
			return err
		}
		binding, err := host.binding(args[0], true)
		if err != nil {
			return err
		}
		requestID, err = taskRequestID(cmd, requestID)
		if err != nil {
			return err
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer c.Close()
		if err := taskReceipt(cmd, args[0], requestID, binding); err != nil {
			return err
		}
		result, err := c.RetryProjectTask(cmd.Context(), args[0], args[1], compute.RetryProjectTaskRequest{RequestID: requestID, Binding: binding})
		if err != nil {
			return taskSubmissionError(args[0], requestID, binding.HostID, err)
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	host.flags(cmd)
	cmd.Flags().StringVar(&requestID, "request-id", "", "durable retry identity; preserve it after an uncertain response")
	return cmd
}

func newCancelVMProjectTask() *cobra.Command {
	var host projectTaskHost
	cmd := &cobra.Command{Use: "cancel <project-id> <task-id> <attempt-id>", Short: "Cancel exactly one recorded attempt without selecting a replacement", Args: arguments(cobra.ExactArgs(3)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := taskIdentities(args...); err != nil {
			return err
		}
		binding, err := host.binding(args[0], false)
		if err != nil {
			return err
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer c.Close()
		if host.bindingFile == "" {
			result, err := c.ProjectTask(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			found := false
			for _, attempt := range result.Attempts {
				if attempt.ID == args[2] && attempt.TaskID == args[1] && attempt.Binding.HostID == binding.HostID && attempt.Binding.ProjectID == args[0] {
					binding, found = attempt.Binding, true
					break
				}
			}
			if !found {
				return compute.Problem("not_found", "The exact original attempt was not found in this task's history; no cancellation was sent.")
			}
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Host: %s  Project: %s  Task: %s  Attempt: %s  VM: %s  Boot: %s  Instance: %s\n", binding.HostID, args[0], args[1], args[2], binding.MachineID, binding.BootID, binding.InstanceID)
		result, err := c.CancelProjectTaskAttempt(cmd.Context(), args[0], args[1], args[2], compute.CancelProjectTaskRequest{Binding: binding})
		if err != nil {
			return fmt.Errorf("cancel task %s attempt %s: %w; inspect that exact attempt before deciding whether to repeat cancellation", args[1], args[2], err)
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	host.flags(cmd)
	return cmd
}

func newVMProjectTaskLogs() *cobra.Command {
	var host projectTaskHost
	var offset int64
	var limit int
	var raw bool
	cmd := &cobra.Command{Use: "logs <project-id> <task-id> <attempt-id>", Short: "Read one byte-offset log chunk from an exact attempt", Args: arguments(cobra.ExactArgs(3)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := taskIdentities(args...); err != nil {
			return err
		}
		if offset < 0 || limit < 1 || limit > 65536 {
			return usageError(fmt.Errorf("--offset must be nonnegative and --limit must be 1–65536 bytes"))
		}
		if raw && cmd.Flags().Changed("json") {
			return usageError(fmt.Errorf("choose either --raw or --json log output"))
		}
		binding, err := host.binding(args[0], false)
		if err != nil {
			return err
		}
		c, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer c.Close()
		result, err := c.ProjectTaskLogs(cmd.Context(), args[0], args[1], args[2], offset, limit)
		if err != nil {
			return err
		}
		if raw {
			_, err = io.Copy(cmd.OutOrStdout(), bytes.NewReader(result.Data))
			return err
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	host.flags(cmd)
	cmd.Flags().Int64Var(&offset, "offset", 0, "zero-based byte offset in this attempt's combined log")
	cmd.Flags().IntVar(&limit, "limit", 65536, "maximum bytes to return, 1–65536")
	cmd.Flags().BoolVar(&raw, "raw", false, "write only the exact log bytes to stdout without decoding text")
	return cmd
}
