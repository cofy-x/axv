package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/spf13/cobra"
)

func newVMVolumes() *cobra.Command {
	var listJSON bool
	listVolumes := func(cmd *cobra.Command) error {
		c, err := commandClient(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
		volumes, err := c.Volumes(cmd.Context())
		if err != nil {
			return err
		}
		if listJSON {
			return jsonResult(cmd.OutOrStdout(), map[string]any{"volumes": volumes})
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tGiB\tRETAIN\tMACHINE\tSTATE")
		for _, v := range volumes {
			fmt.Fprintf(w, "%s\t%q\t%d\t%t\t%s\t%s\n", v.ID, v.Name, v.SizeGiB, v.Retain, v.MachineID, v.Status)
		}
		return w.Flush()
	}
	parent := &cobra.Command{
		Use:     "volumes",
		Aliases: []string{"volume"},
		Short:   "List or delete project volumes",
		Args:    arguments(cobra.NoArgs),
		RunE:    func(cmd *cobra.Command, _ []string) error { return listVolumes(cmd) },
	}
	parent.Flags().BoolVar(&listJSON, "json", false, "write machine-readable JSON")

	var o mutationOptions
	var yes bool
	rm := &cobra.Command{
		Use:   "rm <name|id>",
		Short: "Delete a detached project volume permanently",
		Args:  arguments(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			volumes, err := c.Volumes(ctx)
			if err != nil {
				return err
			}
			ref := strings.TrimSpace(args[0])
			var target *compute.Volume
			for _, candidate := range volumes {
				if candidate.ID == ref {
					copy := candidate
					target = &copy
					break
				}
			}
			if target == nil {
				for _, candidate := range volumes {
					if candidate.Name == ref {
						if target != nil {
							return fmt.Errorf("ambiguous volume name %q; use a full volume ID", ref)
						}
						copy := candidate
						target = &copy
					}
				}
			}
			if target == nil {
				return compute.Problem("not_found", "volume %q was not found", ref)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Permanently delete detached project volume %q (%s, %d GiB).\n", target.Name, target.ID, target.SizeGiB)
			if err = confirmDelete(cmd, yes); err != nil {
				return err
			}
			requestID := compute.NewID("cli")
			mutationReceipt(cmd, c, requestID, target.ID)
			op, err := c.DeleteVolume(ctx, target.ID, requestID, target.Revision)
			if err != nil {
				return submissionFailure(c, requestID, err)
			}
			if !o.noWait {
				op, err = waitOperation(ctx, c, op.ID, false, cmd.ErrOrStderr())
			}
			if o.json {
				if outErr := jsonResult(cmd.OutOrStdout(), op); outErr != nil {
					return outErr
				}
				return err
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", target.ID, op.ID, op.Status)
			return nil
		},
	}
	mutationFlags(rm, &o)
	rm.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without a prompt")
	parent.AddCommand(rm)
	return parent
}
