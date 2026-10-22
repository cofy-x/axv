package cli

import (
	"fmt"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/spf13/cobra"
)

func newVMProjectInitialize() *cobra.Command {
	var host projectTaskHost
	var repository, name, requestID string
	cmd := &cobra.Command{Use: "initialize <parent-project-id> <new-directory>", Short: "Create a new project directory or clone a repository using a persistent VM task", Args: arguments(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if requestID == "" {
			return usageError(fmt.Errorf("--request-id is required; preserve it and the binding file to recover the original task"))
		}
		binding, err := host.binding(cmd, args[0], true)
		if err != nil {
			return err
		}
		req := compute.InitializeProjectRequest{RequestID: requestID, Binding: binding, Directory: args[1], Repository: repository, Name: name}
		if _, err := compute.ProjectInitializeSpec(req); err != nil {
			return usageError(err)
		}
		client, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer client.Close()
		result, err := client.InitializeProject(cmd.Context(), args[0], req)
		if err != nil {
			return fmt.Errorf("initialization request %s: %w; query the original receipt with axv vm project task receipt %s %s --host-id %s", requestID, err, args[0], requestID, binding.HostID)
		}
		return jsonResult(cmd.OutOrStdout(), result)
	}}
	host.flags(cmd)
	cmd.Flags().StringVar(&requestID, "request-id", "", "required persistent submission identity; repeat unchanged after an uncertain response")
	cmd.Flags().StringVar(&repository, "repository", "", "credential-free HTTPS or SSH Git URL (omit for an empty project)")
	cmd.Flags().StringVar(&name, "name", "", "optional project display name recorded for final registration")
	return cmd
}

func newVMProjectFinishInitialization() *cobra.Command {
	var host projectTaskHost
	cmd := &cobra.Command{Use: "finish-initialization <parent-project-id> <request-id>", Short: "Register the new directory from a successful initialization task", Args: arguments(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		binding, err := host.binding(cmd, args[0], true)
		if err != nil {
			return err
		}
		client, err := commandClientFor(cmd, computeclient.Binding{HostID: binding.HostID})
		if err != nil {
			return err
		}
		defer client.Close()
		project, err := client.FinishProjectInitialization(cmd.Context(), args[0], args[1], binding)
		if err != nil {
			return err
		}
		return jsonResult(cmd.OutOrStdout(), map[string]any{"project": project})
	}}
	host.flags(cmd)
	return cmd
}
