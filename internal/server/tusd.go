package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"uuid"

	"github.com/tus/tusd/v2/pkg/filelocker"
	"github.com/tus/tusd/v2/pkg/filestore"
	tusd "github.com/tus/tusd/v2/pkg/handler"

	"github.com/xmapst/taskflow/internal/config"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/pkg/utility"
)

// These standalone operations document the tusd handler mounted in startServer.

// @Summary 创建续传上传
// @Description 创建 tus 1.0 上传并返回 Location。Upload-Metadata 必须包含 task_name，值为任务名称的 Base64 编码。指定 Upload-Length 或 Upload-Defer-Length: 1；携带首段数据时使用 application/offset+octet-stream。
// @Tags 续传上传
// @Accept application/offset+octet-stream
// @Param Tus-Resumable header string true "tus 版本" Enums(1.0.0)
// @Param Upload-Length header integer false "上传总字节数"
// @Param Upload-Defer-Length header integer false "总长度暂未知" Enums(1)
// @Param Upload-Metadata header string true "tus 元数据（必含 task_name）"
// @Param chunk body string false "可选的首段二进制数据"
// @Success 201 "上传已创建"
// @Header 201 {string} Location "后续请求使用的上传地址"
// @Header 201 {integer} Upload-Offset "已接收字节数（携带首段数据时）"
// @Failure 400 {string} string "上传头或元数据不合法"
// @Failure 412 {string} string "缺少或不支持 Tus-Resumable"
// @Router /api/v1/files/ [post]
// @Router /api/v1/files [post]

// @Summary 查询续传能力
// @Description 返回 tus 版本与扩展，不需要发送 Tus-Resumable。
// @Tags 续传上传
// @Success 200 "无响应体"
// @Header 200 {string} Tus-Version "支持的 tus 版本"
// @Header 200 {string} Tus-Extension "支持的 tus 扩展"
// @Router /api/v1/files/ [options]
// @Router /api/v1/files [options]

// @Summary 查询上传进度
// @Tags 续传上传
// @Param task path string true "Location 中的任务名称段"
// @Param upload path string true "Location 中的上传 ID"
// @Success 200 "无响应体"
// @Header 200 {integer} Upload-Offset "已接收字节数"
// @Header 200 {integer} Upload-Length "上传总字节数（长度已知时）"
// @Header 200 {integer} Upload-Defer-Length "长度未知时为 1"
// @Header 200 {string} Upload-Metadata "上传元数据"
// @Failure 404 "上传不存在"
// @Router /api/v1/files/{task}/{upload} [head]

// @Summary 追加上传数据
// @Description 使用 Location 中的上传地址；Upload-Offset 必须等于当前上传偏移量。
// @Tags 续传上传
// @Accept application/offset+octet-stream
// @Param task path string true "Location 中的任务名称段"
// @Param upload path string true "Location 中的上传 ID"
// @Param Tus-Resumable header string true "tus 版本" Enums(1.0.0)
// @Param Upload-Offset header integer true "当前字节偏移量"
// @Param Upload-Length header integer false "为延迟长度的上传设置总字节数"
// @Param chunk body string true "二进制数据块"
// @Success 204 "无响应体"
// @Header 204 {integer} Upload-Offset "追加后的字节偏移量"
// @Failure 400 {string} string "上传头或 Content-Type 不合法"
// @Failure 404 {string} string "上传不存在"
// @Failure 409 {string} string "上传偏移量不匹配"
// @Failure 412 {string} string "缺少或不支持 Tus-Resumable"
// @Router /api/v1/files/{task}/{upload} [patch]

// @Summary 读取续传数据
// @Description 从上传地址读取暂存数据；上传完成后暂存文件可能已被移至工作区。
// @Tags 续传上传
// @Produce application/octet-stream
// @Param task path string true "Location 中的任务名称段"
// @Param upload path string true "Location 中的上传 ID"
// @Param Range header string false "字节范围"
// @Success 200 {file} file "暂存数据（媒体类型可能根据上传元数据而变化）"
// @Success 204 "尚无可下载数据"
// @Success 206 {file} file "部分数据"
// @Failure 404 {string} string "上传不存在"
// @Failure 416 {string} string "字节范围不可满足"
// @Router /api/v1/files/{task}/{upload} [get]

// @Summary 终止续传上传
// @Tags 续传上传
// @Param task path string true "Location 中的任务名称段"
// @Param upload path string true "Location 中的上传 ID"
// @Param Tus-Resumable header string true "tus 版本" Enums(1.0.0)
// @Success 204 "上传已终止，无响应体"
// @Failure 404 {string} string "上传不存在"
// @Failure 412 {string} string "缺少或不支持 Tus-Resumable"
// @Router /api/v1/files/{task}/{upload} [delete]

// @Summary 查询上传地址的续传能力
// @Tags 续传上传
// @Param task path string true "Location 中的任务名称段"
// @Param upload path string true "Location 中的上传 ID"
// @Success 200 "无响应体"
// @Header 200 {string} Tus-Version "支持的 tus 版本"
// @Header 200 {string} Tus-Extension "支持的 tus 扩展"
// @Router /api/v1/files/{task}/{upload} [options]

func (p *Server) newTusSvr(basePath string) error {
	tmpdir := filepath.Join(config.App.TempDir(), "upload")
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		return fmt.Errorf("create upload directory: %w", err)
	}
	composer := tusd.NewStoreComposer()
	store := filestore.New(tmpdir)
	store.UseIn(composer)
	filelocker.New(tmpdir).UseIn(composer)
	handler, err := tusd.NewHandler(tusd.Config{
		BasePath:      basePath,
		StoreComposer: composer,
		Cors: &tusd.CorsConfig{
			Disable: true,
		},
		// tusd 依赖 x/exp/slog，类型与标准库不兼容，经 utility.BridgeXExpLogger 桥接
		Logger: utility.BridgeXExpLogger(slog.Default().Handler()),
		PreUploadCreateCallback: func(hook tusd.HookEvent) (tusd.HTTPResponse, tusd.FileInfoChanges, error) {
			id := uuid.NewV7().String()
			taskName := hook.Upload.MetaData["task_name"]
			if _, err := p.lookupUploadWorkspace(hook.Context, taskName); err != nil {
				return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, err
			}
			// tusd 对上传 ID 有严格的 URL-safe 字符校验，且 ID 是 tus 协议里的
			// 虚拟路径（永远用 /分隔），不是文件系统路径，因此：
			//  1. 任务名允许中文等非 ASCII 字符，必须先转义再拼接；
			//  2. 必须用 path.Join 而非 filepath.Join——后者在 Windows 上会用
			//     \ 拼接，导致 ID 里混入反斜杠，同样无法通过校验。
			return tusd.HTTPResponse{}, tusd.FileInfoChanges{
				ID: path.Join(url.PathEscape(taskName), id),
			}, nil
		},
		PreFinishResponseCallback: func(hook tusd.HookEvent) (tusd.HTTPResponse, error) {
			// hook.Upload.ID 始终是 / 分隔的虚拟路径，用 path 包而非 filepath 解析
			taskDir := path.Dir(hook.Upload.ID)
			taskName, unescapeErr := url.PathUnescape(taskDir)
			if unescapeErr != nil {
				return tusd.HTTPResponse{}, tusd.NewError("ERR_INVALID_TASK_NAME", "invalid task name", http.StatusBadRequest)
			}
			workdir, err := p.lookupUploadWorkspace(hook.Context, taskName)
			if err != nil {
				return tusd.HTTPResponse{}, err
			}

			if !hook.Upload.IsPartial {
				filename := hook.Upload.MetaData["filename"]
				if filename == "" {
					filename = path.Base(hook.Upload.ID)
				}

				// hook.Upload.ID 是 / 分隔的虚拟路径，落到真实文件系统前
				// 需转换成本机分隔符，再用 filepath.Join 拼接
				if !filepath.IsLocal(filename) || strings.Contains(filename, "\\") {
					return tusd.HTTPResponse{
						StatusCode: http.StatusBadRequest,
					}, errors.New("invalid upload filename")
				}
				if err = p.copyUploadedFile(tmpdir, hook.Upload.ID, workdir, filename); err != nil {
					return tusd.HTTPResponse{
						StatusCode: http.StatusInternalServerError,
						Body:       "failed to copy file",
					}, err
				}

				// 数据已经落到工作区：最终上传的副本、以及已经并进最终文件的分片
				// 都不再需要，立即删掉载荷，否则暂存目录会随每次上传不断增长。
				consumed := append([]string{hook.Upload.ID}, hook.Upload.PartialUploads...)
				p.releaseStagedUploads(hook.Context, store, consumed)
				// 任务名子目录要等锁释放后才删得掉，交给 uploadStaging
				p.enqueueStaging(hook.Context, consumed...)
			}
			return tusd.HTTPResponse{
				Header: map[string]string{
					"ID":   path.Base(hook.Upload.ID),
					"Path": taskName,
				},
			}, nil
		},
	})
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		return err
	}
	// 暂存目录要等 tusd 的锁释放之后才删得掉，由 uploadStaging 收尾
	p.tusd = &uploadStaging{next: handler, root: tmpdir}
	return nil
}

// uploadStaging 在 tusd 响应写完之后清理暂存目录。
//
// PreFinishResponseCallback 里删不掉任务名子目录：那一刻 filelocker 还持着
// <id>.lock，Windows 上会直接失败，Unix 上也会让随后的 Unlock 落到一个已解除
// 链接的文件上。载荷（<id> 与 <id>.info）不受此限，Terminate 不碰锁文件，
// 所以在回调里就删；目录留到 ServeHTTP 返回、锁释放之后再删。
type uploadStaging struct {
	next http.Handler
	// root 是暂存根目录，上传按 <任务名>/<上传 ID> 落在其下
	root string
}

type stagingKey struct{}

// stagingRun 收集一次请求里完成的上传 ID。
type stagingRun struct {
	mu  sync.Mutex
	ids []string
}

func (s *uploadStaging) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	run := new(stagingRun)
	s.next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stagingKey{}, run)))
	run.sweep(s.root)
}

func (run *stagingRun) add(ids ...string) {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.ids = append(run.ids, ids...)
}

// sweep 删除已经空掉的任务名子目录。同一任务还有别的上传在跑时目录非空，
// Remove 失败即可，无需重试。
func (run *stagingRun) sweep(root string) {
	for _, id := range run.ids {
		dir := filepath.FromSlash(path.Dir(id))
		// 空 ID 或无目录层的 ID 会指到暂存根目录本身，不能删
		if !filepath.IsLocal(dir) {
			continue
		}
		_ = os.Remove(filepath.Join(root, dir))
	}
}

func (p *Server) enqueueStaging(ctx context.Context, ids ...string) {
	run, ok := ctx.Value(stagingKey{}).(*stagingRun)
	if !ok {
		return
	}
	run.add(ids...)
}

// releaseStagedUploads 删除已完成上传在暂存目录中的载荷。
// 只删 <id> 与 <id>.info：<id>.lock 归 filelocker 管，请求期间仍在使用。
func (p *Server) releaseStagedUploads(ctx context.Context, store filestore.FileStore, ids []string) {
	for _, id := range ids {
		upload, err := store.GetUpload(ctx, id)
		if err != nil {
			slog.ErrorContext(ctx, "load staged upload", slog.String("id", id), slog.Any("error", err))
			continue
		}
		if err := store.AsTerminatableUpload(upload).Terminate(ctx); err != nil {
			slog.ErrorContext(ctx, "drop staged upload", slog.String("id", id), slog.Any("error", err))
		}
	}
}

func (p *Server) lookupUploadWorkspace(ctx context.Context, name string) (string, error) {
	if !filepath.IsLocal(name) || name == "." || strings.ContainsAny(name, "/\\:") {
		return "", tusd.NewError("ERR_INVALID_TASK_NAME", "task_name is required and must be a valid task name", http.StatusBadRequest)
	}
	workdir, err := storage.Task(name).Workspace(ctx)
	if errors.Is(err, storage.ErrTaskNotFound) {
		return "", tusd.NewError("ERR_INVALID_TASK_NAME", "task does not exist", http.StatusBadRequest)
	}
	if err != nil {
		slog.ErrorContext(ctx, "look up upload task", slog.String("task", name), slog.Any("error", err))
		return "", tusd.NewError("ERR_TASK_LOOKUP", "could not look up task workspace", http.StatusInternalServerError)
	}
	return workdir, nil
}

// copyUploadedFile uses rooted filesystem operations so symlinks in a task's
// workspace cannot redirect uploads outside that workspace.
func (p *Server) copyUploadedFile(uploadDir, id, workdir, filename string) error {
	if !filepath.IsLocal(filename) || filename == "." ||
		strings.ContainsAny(filename, "\\:") {
		return fmt.Errorf("invalid upload destination")
	}
	sourceRoot, err := os.OpenRoot(uploadDir)
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	source, err := sourceRoot.Open(filepath.FromSlash(id))
	if err != nil {
		return err
	}
	defer source.Close()

	workspaceRoot, err := os.OpenRoot(config.App.WorkspaceDir())
	if err != nil {
		return err
	}
	defer workspaceRoot.Close()
	if err := workspaceRoot.MkdirAll(workdir, 0o755); err != nil {
		return err
	}
	taskRoot, err := workspaceRoot.OpenRoot(workdir)
	if err != nil {
		return err
	}
	defer taskRoot.Close()
	if parent := filepath.Dir(filename); parent != "." {
		if err := taskRoot.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	destination, err := taskRoot.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		return fmt.Errorf("copy uploaded file: %w", copyErr)
	}
	return closeErr
}
