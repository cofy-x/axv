package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
	"github.com/spf13/cobra"
)

func cacheForCommand() (*artifactcache.Cache, error) {
	home, err := artifactcache.Home()
	if err != nil {
		return nil, err
	}
	return artifactcache.New(home)
}

func cacheOperationContext(cmd *cobra.Command) (context.Context, *diagnostics.Logger, error) {
	home, err := profile.Home()
	if err != nil {
		return nil, nil, err
	}
	profileLog, err := logs.Open(home, "cache", cmd.ErrOrStderr())
	if err != nil {
		return nil, nil, err
	}
	logger := profileLog.With("command", cmd.Name())
	return diagnostics.Context(cmd.Context(), logger), logger, nil
}
func newCache() *cobra.Command {
	root := &cobra.Command{Use: "cache", Short: "Inspect and maintain shared verified VM downloads", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var jsonOutput bool
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "print JSON")
	for _, name := range []string{"list", "verify"} {
		root.AddCommand(&cobra.Command{Use: name, Short: "Verify and list cached artifacts", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
			cache, err := cacheForCommand()
			if err != nil {
				return err
			}
			items, err := cache.List(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Home      string                 `json:"cacheHome"`
					Artifacts []artifactcache.Status `json:"artifacts"`
				}{cache.Home, items})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Cache:", cache.Home)
			for _, item := range items {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", item.SHA256, item.State, item.Name)
			}
			return nil
		}})
	}
	root.AddCommand(&cobra.Command{Use: "remove <sha256>", Short: "Explicitly remove one inactive cache entry", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		cache, err := cacheForCommand()
		if err != nil {
			return err
		}
		ctx, logger, err := cacheOperationContext(cmd)
		if err != nil {
			return err
		}
		logger.Info("artifact.cache.remove", "sha256", args[0])
		err = cache.Remove(ctx, args[0])
		if err != nil {
			logger.Error("artifact.cache.remove_failed", err, "sha256", args[0])
		} else {
			logger.Info("artifact.cache.removed", "sha256", args[0])
		}
		return err
	}})
	for _, importing := range []bool{false, true} {
		name, usage, argc := "fetch", " <artifact-id>", 1
		if importing {
			name, usage, argc = "import", " <artifact-id> <existing-file>", 2
		}
		var output string
		command := &cobra.Command{Use: name + usage, Short: "Cache a pinned public artifact (lima-arm64, lima-amd64, ubuntu-arm64, ubuntu-amd64)", Args: arguments(cobra.ExactArgs(argc)), RunE: func(cmd *cobra.Command, args []string) error {
			a, ok := artifacts.Lookup(args[0])
			if !ok {
				return usageError(fmt.Errorf("select a reviewed official artifact ID"))
			}
			a, err := artifacts.MirrorArtifact(a, os.Getenv("AXV_ARTIFACT_MIRRORS"))
			if err != nil {
				return err
			}
			cache, err := cacheForCommand()
			if err != nil {
				return err
			}
			source := ""
			if importing {
				source = args[1]
				if !filepath.IsAbs(source) {
					return usageError(fmt.Errorf("import source must be an absolute existing file"))
				}
			}
			partial := ""
			if output != "" {
				if !filepath.IsAbs(output) {
					return usageError(fmt.Errorf("--output must be absolute"))
				}
				rel, e := filepath.Rel(cache.Home, output)
				if e != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
					return usageError(fmt.Errorf("profile output must be outside the shared cache"))
				}
				partial = filepath.Join(filepath.Dir(output), ".axv-"+a.SHA256+".part")
			}
			if a.OriginURL != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "Artifact source: %s (original: %s); no automatic fallback; SHA-256 %s\n", a.URL, a.OriginURL, a.SHA256)
			}
			client := artifactcache.DownloadClient()
			defer client.CloseIdleConnections()
			ctx, _, err := cacheOperationContext(cmd)
			if err != nil {
				return err
			}
			status, err := cache.CopyTo(ctx, client, a, output, partial, source, nil)
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", status.State, status.Path)
			return nil
		}}
		command.Flags().StringVar(&output, "output", "", "copy independent bytes to this profile-owned file")
		root.AddCommand(command)
	}
	return root
}
