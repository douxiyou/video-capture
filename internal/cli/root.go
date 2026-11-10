package cli

import (
	"errors"
	"fmt"
	"io/fs"

	"douxiyou.com/video-capture/internal/config"
	"douxiyou.com/video-capture/internal/server"
	"douxiyou.com/video-capture/internal/storage"
	"github.com/spf13/cobra"
)

func NewRootCommand(frontend fs.FS) *cobra.Command {
	root := &cobra.Command{
		Use:           "video-capture",
		Short:         "Video capture application",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	var configPath string
	root.PersistentFlags().StringVar(&configPath, "config", "config.yaml", "YAML configuration file")
	loadConfig := func(cmd *cobra.Command) (config.Config, error) {
		return config.Load(configPath, cmd.Flags().Changed("config"))
	}
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the API server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if cfg.Face.Enabled {
				return serveExperimental(cmd.Context(), cfg, frontend)
			}
			return server.Serve(cmd.Context(), cfg.Server.Addr, frontend)
		},
	}

	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending SQLite database migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Open(cmd.Context(), cfg.Database.Path)
			if err != nil {
				return err
			}
			defer func() {
				if err := db.Close(); err != nil {
					runErr = errors.Join(runErr, fmt.Errorf("close database: %w", err))
				}
			}()
			count, err := storage.Migrate(cmd.Context(), db)
			if err != nil {
				return err
			}
			if count == 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Database is already up to date.")
			} else {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Applied %d migration(s).\n", count)
			}
			return err
		},
	}
	root.AddCommand(serve, migrate)
	return root
}
