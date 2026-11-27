package server

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	_ "net/http/pprof"
	"runtime"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/config"
	"github.com/xmapst/taskflow/internal/server/handler/event"
	"github.com/xmapst/taskflow/internal/server/handler/execution"
	"github.com/xmapst/taskflow/internal/server/handler/system"
	"github.com/xmapst/taskflow/internal/server/handler/terminal"
	"github.com/xmapst/taskflow/internal/server/handler/workspace"
	"github.com/xmapst/taskflow/internal/server/middleware"
	"github.com/xmapst/taskflow/pkg/utility"
)

//go:embed static/index.html static/assets/*
var frontendFS embed.FS

// newHandler registers HTTP routes.
// @title			TaskFlow API
// @version			1.0
// @Description		An `API` for cross-platform custom orchestration of execution steps
// @Description		without any third-party dependencies. Based on `DAG`, it implements the scheduling
// @Description		function of sequential execution of dependent steps and concurrent execution of
// @Description		non-dependent steps. <br /><br /> It provides `API` remote operation mode, batch
// @Description		execution of `Shell` , `Powershell` , `Python` and other commands, and easily
// @Description		completes common management tasks such as running automated operation and maintenance
// @Description		scripts, polling processes, installing or uninstalling software, updating applications,
// @Description		and installing patches.
// @contact.name	TaskFlow
// @contact.url		https://github.com/xmapst/taskflow/issues
// @license.name	GPL-3.0
// @license.url		https://github.com/xmapst/taskflow/blob/main/LICENSE
func (p *Server) newHandler() (http.Handler, error) {
	r := &routes{
		mux:        http.NewServeMux(),
		events:     &event.Handler{},
		systems:    &system.Handler{},
		tasks:      &execution.Handler{},
		workspaces: &workspace.Handler{WorkspaceDir: config.App.WorkspaceDir()},
		terminals:  &terminal.Handler{ReplayDir: config.App.ReplayDir()},
		streams:    httpx.NewStreamRegistry(),
	}
	mounts := []func(*routes) error{
		p.mountFrontend,
		p.mountProbes,
		p.mountFiles,
		p.mountStream,
		p.mountSystem,
		p.mountApprovals,
		p.mountTasks,
		p.mountWorkspace,
		p.mountTerminal,
	}
	for _, mount := range mounts {
		if err := mount(r); err != nil {
			return nil, err
		}
	}
	allow, err := middleware.NewIPAllowlist(config.App.AllowIPs, config.App.TrustedProxies)
	if err != nil {
		return nil, err
	}
	allow.Log()
	return middleware.Wrap(r.mux, allow), nil
}

// routes is what the mount functions share: the single mux every route lands
// on, plus the handler instances those routes delegate to. Mounting one route
// group in several places must not construct a second handler, so they are
// built once here.
type routes struct {
	mux        *http.ServeMux
	events     *event.Handler
	systems    *system.Handler
	tasks      *execution.Handler
	workspaces *workspace.Handler
	terminals  *terminal.Handler
	streams    *httpx.StreamRegistry
}

// mountFrontend serves the embedded web UI. HashRouter handles page paths in
// the browser; the fragment never reaches Go.
func (p *Server) mountFrontend(r *routes) error {
	root, err := fs.Sub(frontendFS, "static")
	if err != nil {
		return err
	}
	frontend := http.FileServerFS(root)
	r.mux.Handle("GET /{$}", frontend)
	r.mux.Handle("GET /assets/", frontend)
	return nil
}

// mountProbes registers service metadata, health probes and pprof.
func (p *Server) mountProbes(r *routes) error {
	r.mux.Handle("/debug/pprof/", http.DefaultServeMux)
	r.mux.HandleFunc("GET /version", p.version)
	r.mux.HandleFunc("GET /healthyz", p.healthyz)
	r.mux.HandleFunc("GET /heartbeat", p.heartbeat)
	return nil
}

// mountFiles registers the tusd upload endpoints.
func (p *Server) mountFiles(r *routes) error {
	if err := p.newTusSvr("/api/v1/files/"); err != nil {
		return err
	}
	r.mux.Handle("/api/v1/files", http.StripPrefix("/api/v1/files", p.tusd))
	r.mux.Handle("/api/v1/files/", http.StripPrefix("/api/v1/files/", p.tusd))
	return nil
}

// mountStream registers the WebSocket for multiplexed subscriptions and RPC.
//
// The client sends one JSON request per text frame:
//
//	{"id":"<id>","action":"subscribe","topic":"<topic>","params":{...}}
//	{"id":"<id>","action":"unsubscribe"}
//	{"id":"<id>","action":"call","method":"<method>","params":{...}}
//
// The server answers with JSON frames: "update" (a new value for a
// subscription), "result" (the reply to a call), "error" and finally "done" for
// a subscription that will not produce anything else.
//
// Topics: event, system, tasks, task, steps, step, step-log, terminal-sessions,
// terminal-watch, terminal-replay. Methods: task.create, task.manage,
// task.delete, task.dump, step.manage, system.update, terminal.disconnect,
// replay.list.
//
// The workspace stays on HTTP, and the interactive shell stays on its own
// socket: a PTY carries raw binary frames, which is not something this
// multiplexed JSON connection should mix in.
func (p *Server) mountStream(r *routes) error {
	r.events.RegisterStream(r.streams)
	r.systems.RegisterStream(r.streams)
	r.tasks.RegisterStream(r.streams)
	r.terminals.RegisterStream(r.streams)
	r.mux.HandleFunc("GET /api/v1/ws", r.streams.Serve)
	return nil
}

func (p *Server) mountSystem(r *routes) error {
	r.mux.HandleFunc("GET /api/v1/system", r.systems.GetSystemConfig)
	return nil
}

// mountApprovals registers the callback endpoints that approval steps call
// back into. Both verbs are accepted because the task definition decides which
// one the remote side uses.
func (p *Server) mountApprovals(r *routes) error {
	r.mux.HandleFunc("POST /api/v1/approval/callback", r.tasks.ApprovalCallback)
	r.mux.HandleFunc("PUT /api/v1/approval/callback", r.tasks.ApprovalCallback)
	return nil
}

func (p *Server) mountTasks(r *routes) error {
	r.mux.HandleFunc("GET /api/v1/task", r.tasks.ListTasks)
	r.mux.HandleFunc("POST /api/v1/task", r.tasks.CreateTask)
	r.mux.HandleFunc("GET /api/v1/task/{task}", r.tasks.GetTask)
	r.mux.HandleFunc("PUT /api/v1/task/{task}", r.tasks.ManageTask)
	r.mux.HandleFunc("DELETE /api/v1/task/{task}", r.tasks.DeleteTask)
	r.mux.HandleFunc("GET /api/v1/task/{task}/dump", r.tasks.DumpTask)

	r.mux.HandleFunc("GET /api/v1/task/{task}/step", r.tasks.ListSteps)
	r.mux.HandleFunc("GET /api/v1/task/{task}/step/{step}", r.tasks.GetStep)
	r.mux.HandleFunc("PUT /api/v1/task/{task}/step/{step}", r.tasks.ManageStep)
	r.mux.HandleFunc("GET /api/v1/task/{task}/step/{step}/log", r.tasks.GetStepLog)
	return nil
}

func (p *Server) mountWorkspace(r *routes) error {
	r.mux.HandleFunc("GET /api/v1/task/{task}/workspace", r.workspaces.GetWorkspace)
	r.mux.HandleFunc("DELETE /api/v1/task/{task}/workspace", r.workspaces.DeleteWorkspace)
	return nil
}

// mountTerminal registers the interactive shell channel, the terminal HTTP
// surface and indexes the recordings.
//
// The shell keeps its own socket because it carries raw PTY frames; the live
// feeds (session list, watch, replay, disconnect) also ride the shared
// /api/v1/ws. The HTTP routes below are the same data for plain clients: the
// session list and the recording index are one-shot JSON, watch and replay are
// NDJSON line streams.
func (p *Server) mountTerminal(r *routes) error {
	if err := r.terminals.ImportReplays(); err != nil {
		return fmt.Errorf("import terminal recordings: %w", err)
	}
	r.mux.HandleFunc("GET /api/v1/terminal", r.terminals.OpenTerminal)
	r.mux.HandleFunc("GET /api/v1/terminal/sessions", r.terminals.ListWebShellSessionsHTTP)
	r.mux.HandleFunc("DELETE /api/v1/terminal/sessions/{id}", r.terminals.DisconnectWebShellHTTP)
	r.mux.HandleFunc("GET /api/v1/terminal/sessions/{id}/watch", r.terminals.WatchWebShell)
	r.mux.HandleFunc("GET /api/v1/terminal/replays", r.terminals.ListTerminalReplays)
	r.mux.HandleFunc("GET /api/v1/terminal/replays/{id}", r.terminals.GetTerminalReplay)
	return nil
}

// healthyz 返回服务运行状态和连接信息。
// @Summary		健康
// @Description 用于检测服务是否正常
// @Tags		默认
// @Produce		application/json,application/yaml
// @Success		200 {object} api.Response[iproto.SHealthyz]
// @Router		/healthyz [get]
func (p *Server) healthyz(w http.ResponseWriter, r *http.Request) {
	httpx.Send(w, r, api.WithData(&iproto.SHealthyz{
		Server: r.Host,
		Client: r.RemoteAddr,
		State:  "Running",
	}))
}

// heartbeat 返回无响应体的 HTTP 200 心跳。
// @Summary		心跳
// @Description	用于判断服务是否正常
// @Tags		默认
// @Success		200 "服务可用，无响应体"
// @Router		/heartbeat [get]
// @Router		/heartbeat [head]
func (p *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// version 返回当前程序版本和构建信息。
// @Summary		版本
// @Description	获取版本信息
// @Tags		默认
// @Produce		application/json,application/yaml
// @Success		200 {object} api.Response[iproto.SVersion]
// @Router		/version [get]
func (p *Server) version(w http.ResponseWriter, r *http.Request) {
	httpx.Send(w, r, api.WithData(&iproto.SVersion{
		Version:   utility.Version,
		BuildTime: utility.BuildTime,
		Git: &iproto.SVersionGit{
			URL:    utility.GitUrl,
			Branch: utility.GitBranch,
			Commit: utility.GitCommit,
		},
		Go: &iproto.SVersionGO{
			Version: runtime.Version(),
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
		},
		User: &iproto.SVersionUser{
			Name:  utility.UserName,
			Email: utility.UserEmail,
		},
	}))
}
