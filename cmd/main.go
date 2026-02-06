package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	cli "github.com/urfave/cli/v3"

	"github.com/haapjari/dexec/pkg/commands"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGHUP,
	)

	root := &cli.Command{
		Name:  "dexec",
		Usage: "run commands inside a DevPod workspace",
		Flags: []cli.Flag{
			commands.RunFlag(),
			commands.NoTTYFlag(),
			commands.StartupTimeoutFlag(),
			commands.StatusIntervalFlag(),
		},
		Action: commands.RunRootAction(),
		Commands: []*cli.Command{
			commands.RunCommand(),
		},
	}

	err := root.Run(ctx, os.Args)
	stop()
	if err != nil {
		slog.Error("run dexec", "err", err)
		os.Exit(commands.ExitCodeForError(err))
	}
}
