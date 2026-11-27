package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
)

// Handler implements workspace endpoints.
type Handler struct {
	WorkspaceDir string
}

// DeleteWorkspace 删除工作区内指定的文件或目录。
// @Summary		删除
// @Description	删除指定文件或递归删除目录。path 可重复传递以一次删除多项；单个路径失败不会中断其余路径，全部执行完后汇总失败项返回。
// @Tags		工作目录
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Param		path query []string false "工作区相对路径，可重复传递多个" collectionFormat(multi)
// @Success		200 {object} api.Response[any]
// @Router		/api/v1/task/{task}/workspace [delete]
func (h *Handler) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	task := r.PathValue("task")
	if task == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	queries := r.URL.Query()["path"]
	if len(queries) == 0 {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("workspace path is required")))
		return
	}
	// 先整体校验再动手：路径不合法时一个都不删，避免批量请求出现删一半的局面
	paths := make([]string, len(queries))
	for i, query := range queries {
		path, err := h.cleanWorkspacePath(query)
		if err != nil {
			httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
			return
		}
		if path == "." {
			// 空路径意味着删除整个工作区，这个动作只允许显式发起，不跟随批量请求
			httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("workspace path is required")))
			return
		}
		paths[i] = path
	}
	root, err := h.openWorkspace(r.Context(), task)
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	defer root.Close()
	
	// 逐项删除并收集失败项：一项删不掉不应让其余选中项留在原地
	var failures []string
	for _, path := range paths {
		if err := root.RemoveAll(path); err != nil {
			slog.ErrorContext(r.Context(), "remove workspace path",
				slog.String("task", task), slog.String("path", path), slog.Any("error", err))
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
		}
	}
	if len(failures) > 0 {
		httpx.Send(w, r, api.WithCode[any](api.CodeFailed).WithError(errors.New(strings.Join(failures, "; "))))
		return
	}
	httpx.Send(w, r, api.WithCode[any](api.CodeSuccess))
}

// GetWorkspace 列出工作区目录内容或下载文件。
// @Summary		列表或下载
// @Description	目录返回 API 包；文件直接返回字节和 Content-Disposition: attachment，Content-Type 按文件类型决定
// @Tags		工作目录
// @Produce		application/json,application/yaml,application/octet-stream
// @Param		task path string true "任务名称"
// @Param		path query string false "工作区相对路径，省略时列出根目录"
// @Success		200 {object} api.Response[iproto.SFileListRes] "目录或错误为 API 响应包；有 Content-Disposition 时为原始文件字节"
// @Header 200 {string} Content-Disposition "下载文件时返回 attachment 及文件名"
// @Router		/api/v1/task/{task}/workspace [get]
func (h *Handler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	taskName := r.PathValue("task")
	if taskName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	root, err := h.openWorkspace(r.Context(), taskName)
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	defer root.Close()
	path, err := h.cleanWorkspacePath(r.URL.Query().Get("path"))
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	file, err := root.Open(path)
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	if !fileInfo.IsDir() {
		ctype := mime.TypeByExtension(filepath.Ext(fileInfo.Name()))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileInfo.Name()))
		_, _ = io.Copy(w, file)
		return
	}
	entries, err := file.ReadDir(-1)
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	
	var infos = new(iproto.SFileListRes)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		_path := filepath.Join(path, info.Name())
		isDir := info.IsDir()
		size := info.Size()
		if info.Mode()&os.ModeSymlink != 0 {
			s, err := root.Stat(_path)
			if err == nil {
				isDir = s.IsDir()
				size = s.Size()
			}
		}
		_path = "/" + filepath.ToSlash(_path)
		infos.Files = append(infos.Files, &iproto.SFileRes{
			Name:    info.Name(),
			Path:    _path,
			Size:    size,
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().UnixNano(),
			IsDir:   isDir,
		})
	}
	infos.Total = len(infos.Files)
	httpx.Send(w, r, api.WithData(infos))
}

// openWorkspace keeps operations rooted for their entire lifetime, avoiding
// check-then-open races when a workspace contains user-controlled symlinks.
func (h *Handler) openWorkspace(ctx context.Context, task string) (*os.Root, error) {
	workdir, err := storage.Task(task).Workspace(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrTaskNotFound) {
			return nil, err
		}
		slog.ErrorContext(ctx, "look up workspace task", slog.String("task", task), slog.Any("error", err))
		return nil, errors.New("could not look up task workspace")
	}
	root, err := os.OpenRoot(h.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.OpenRoot(workdir)
}

// cleanWorkspacePath normalises a path relative to the task workspace.
//
// Directory listings expose paths beginning with '/'; these are relative to
// the task workspace, not absolute paths on the server. An empty path means the
// workspace root itself and is returned as ".".
func (h *Handler) cleanWorkspacePath(path string) (string, error) {
	path = strings.TrimLeft(path, "/")
	if path == "" {
		return ".", nil
	}
	if !filepath.IsLocal(path) || strings.ContainsAny(path, "\\:") {
		return "", errors.New("invalid workspace path")
	}
	for part := range strings.SplitSeq(path, "/") {
		if part == ".." {
			return "", errors.New("invalid workspace path")
		}
	}
	return filepath.Clean(path), nil
}
