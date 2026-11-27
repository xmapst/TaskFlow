package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/config"
	"github.com/xmapst/taskflow/pkg/xnet/listener"
	"github.com/xmapst/taskflow/pkg/xnet/sockets"
)

type Server struct {
	ctx       context.Context
	cancel    context.CancelFunc
	listeners []net.Listener
	http      *http.Server
	tusd      http.Handler
	wg        *sync.WaitGroup
}

func New(ctx context.Context) *Server {
	ctx, cancel := context.WithCancel(ctx)
	return &Server{
		ctx:    ctx,
		cancel: cancel,
		wg:     new(sync.WaitGroup),
	}
}

func (p *Server) Start() error {
	newHandler, err := p.newHandler()
	if err != nil {
		slog.Error("operation failed", slog.Any("error", err))
		return err
	}
	p.http = &http.Server{
		Handler:           newHandler,
		ReadHeaderTimeout: 120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    15 << 20, // 15MB
		BaseContext: func(_ net.Listener) context.Context {
			return p.ctx
		},
	}

	if err = p.loadListeners([]string{
		config.App.Addr,
		sockets.DefaultPipePath(api.ServiceName),
	}); err != nil {
		return err
	}
	for _, ln := range p.listeners {
		p.wg.Add(1)
		go func(ln net.Listener) {
			defer p.wg.Done()
			if err := p.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error(
					"operation failed",
					slog.Any("error", err),
				)
			}
		}(ln)
	}

	return nil
}

func (p *Server) loadListeners(hosts []string) error {
	for _, ln := range p.listeners {
		_ = ln.Close()
	}
	p.listeners = []net.Listener{}
	for _, host := range hosts {
		proto, addr, ok := strings.Cut(host, "://")
		if !ok {
			slog.Warn(
				"listener address has no protocol; using TCP",
				slog.String("address", host),
			)
			proto = "tcp"
			addr = host
		}
		ln, err := listener.New(p.ctx, proto, addr, config.App.TrustedProxies)
		if err != nil {
			slog.Error(
				"operation failed",
				slog.Any("error", err),
			)
			return err
		}
		slog.Info(
			"listener created",
			slog.String("protocol", proto),
			slog.String("address", addr),
		)
		p.listeners = append(p.listeners, ln)
	}
	return nil
}

func (p *Server) Shutdown() {
	slog.Info("shutdown server")
	// Cancel the server context first so streaming responses and upgraded
	// WebSocket connections can exit before the listeners are closed.
	p.cancel()

	if p.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()
		if err := p.http.Shutdown(ctx); err != nil {
			slog.Warn(
				"graceful HTTP shutdown failed",
				slog.Any("error", err),
			)
			_ = p.http.Close()
		}
	}

	if p.listeners != nil {
		for _, ln := range p.listeners {
			_ = ln.Close()
		}
	}
	p.wg.Wait()
}
