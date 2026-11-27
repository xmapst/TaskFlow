package middleware

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// Wrap installs request logging, the client IP allowlist and origin checks
// around all registered routes. A nil allowlist disables IP filtering.
func Wrap(mux http.Handler, allow *IPAllowlist) http.Handler {
	next := allow.Middleware(SameOrigin(mux))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w}
		defer func() {
			status := rw.status
			if status == 0 {
				status = http.StatusOK
			}
			client, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				client = r.RemoteAddr
			}
			slog.InfoContext(r.Context(), "HTTP request", slog.String("client_ip", client),
				slog.String("method", r.Method), slog.String("protocol", r.Proto), slog.Int("status", status),
				slog.String("path", r.URL.Path), slog.Duration("duration", time.Since(start)), slog.String("user_agent", r.UserAgent()))
		}()
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					panic(err)
				}
				// Do not dump request headers: they may contain credentials.
				if isBrokenPipeError(err) {
					slog.WarnContext(r.Context(), "HTTP connection closed", slog.String("path", r.URL.Path), slog.Any("error", err))
					return
				}
				slog.ErrorContext(r.Context(), "HTTP handler panic", slog.String("path", r.URL.Path), slog.Any("error", err), slog.String("stack", string(debug.Stack())))
				if rw.status != 0 {
					panic(http.ErrAbortHandler)
				}
				rw.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(status int) {
	if status == http.StatusSwitchingProtocols || status >= 200 {
		if w.status != 0 {
			return
		}
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return io.Copy(w.ResponseWriter, r)
}

func (w *statusWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Flush() { _ = w.FlushError() }

func isBrokenPipeError(value any) bool {
	err, ok := value.(error)
	if !ok {
		return false
	}
	var ne *net.OpError
	var se *os.SyscallError
	if !errors.As(err, &ne) || !errors.As(ne.Err, &se) {
		return false
	}
	message := strings.ToLower(se.Error())
	return strings.Contains(message, "broken pipe") || strings.Contains(message, "connection reset by peer")
}
