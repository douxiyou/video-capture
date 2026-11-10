package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"douxiyou.com/video-capture/internal/cli"
)

//go:embed frontend/dist
var frontend embed.FS

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dist, err := fs.Sub(frontend, "frontend/dist")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load embedded frontend:", err)
		os.Exit(1)
	}

	if err := cli.NewRootCommand(dist).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
