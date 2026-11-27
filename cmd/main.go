//go:build (windows || linux || darwin) && (amd64 || arm64 || 386)

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/xmapst/xslog"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/chanrpc"
	"github.com/xmapst/taskflow/internal/config"
	"github.com/xmapst/taskflow/internal/server"
	"github.com/xmapst/taskflow/internal/worker"
	"github.com/xmapst/taskflow/pkg/utility"
)

const longText = `TaskFlow runs cross-platform workflows. A directed acyclic graph controls when dependent steps run and lets independent steps run in parallel.`

func init() {
	time.Local = time.UTC
}

func main() {
	var cmd = &cobra.Command{
		Use:           filepath.Base(os.Args[0]),
		Short:         "TaskFlow: cross-platform task orchestration and execution",
		Long:          longText,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       utility.Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.Init(); err != nil {
				return err
			}
			// Also clean up if the service manager fails before Start or Stop.
			defer closeDatabase()

			name, err := filepath.Abs(os.Args[0])
			if err != nil {
				slog.Error(
					"operation failed",
					slog.Any("error", err),
				)
				return err
			}
			svc, err := service.New(&standaloneService{
				ctx:    cmd.Context(),
				server: server.New(cmd.Context()),
				worker: worker.New(cmd.Context()),
			}, &service.Config{
				Name:        api.ServiceName,
				DisplayName: "TaskFlow",
				Description: "Cross-platform task orchestration and execution service",
				Executable:  name,
				Arguments:   os.Args[1:],
			})
			if err != nil {
				return err
			}
			err = svc.Run()
			if err != nil {
				return err
			}
			return nil
		},
	}

	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.SetHelpTemplate(`{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`)
	cmd.PersistentFlags().Bool("help", false, "Print usage")
	cmd.PersistentFlags().BoolP("version", "v", false, "Print version information and quit")
	config.App.BindFlags(cmd.PersistentFlags(), cmd.Flags())

	cmd.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "print version information and quit",
			RunE: func(cmd *cobra.Command, args []string) error {
				utility.PrintHeadInfo()
				return nil
			},
		},
	)

	if err := cmd.Execute(); err != nil {
		slog.Error(
			"command failed",
			slog.Any("error", err),
		)
		_ = xslog.Close()
		os.Exit(1)
	}
	_ = xslog.Close()
}

type standaloneService struct {
	ctx     context.Context
	cancel  context.CancelFunc
	server  *server.Server
	worker  *worker.Worker
	rpcDone <-chan struct{}
}

func (a *standaloneService) Start(s service.Service) (err error) {
	a.ctx, a.cancel = context.WithCancel(a.ctx)
	defer func() {
		if err != nil {
			a.cancel()
			a.worker.Shutdown()
			if a.rpcDone != nil {
				<-a.rpcDone
			}
		}
	}()

	a.worker.SetSize(config.App.PoolSize)
	if err = a.worker.Start(); err != nil {
		return err
	}
	if a.rpcDone, err = chanrpc.Start(a.ctx); err != nil {
		return err
	}
	return a.server.Start()
}

func (a *standaloneService) Stop(s service.Service) error {
	if a.cancel != nil {
		a.cancel()
	}
	a.server.Shutdown()
	a.worker.Shutdown()
	if a.rpcDone != nil {
		<-a.rpcDone
	}
	closeDatabase()
	return xslog.Close()
}

func closeDatabase() {
	if err := config.CloseDB(); err != nil {
		slog.Warn("close database failed", slog.Any("error", err))
	}
}
