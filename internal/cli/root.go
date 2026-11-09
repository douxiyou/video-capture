package cli

import (
	"douxiyou.com/video-capture/internal/server"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "video-capture",
		Short:         "Video capture application",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	var addr string
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the API server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return server.Serve(cmd.Context(), addr)
		},
	}
	serve.Flags().StringVar(&addr, "addr", ":8080", "HTTP listen address")
	root.AddCommand(serve)
	return root
}
