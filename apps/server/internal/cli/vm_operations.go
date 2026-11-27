package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
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
