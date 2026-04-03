package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"time"
)

const DefaultLogsDir = ".logs"

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		slog.Info("request completed",
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start).String(),
		)
	})
}

func DebugMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := dumpRequest(r)
		if err != nil {
			slog.Error("dump request failed",
				"method", r.Method,
				"path", r.URL.Path,
				"error", err,
			)
		} else {
			go writeDebugRequest(payload)
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func dumpRequest(r *http.Request) ([]byte, error) {
	body, err := snapshotRequestBody(r)
	if err != nil {
		return nil, err
	}

	clonedReq, err := cloneRequest(r, body)
	if err != nil {
		return nil, err
	}

	return httputil.DumpRequest(clonedReq, true)
}

func writeDebugRequest(payload []byte) {
	if err := os.MkdirAll(DefaultLogsDir, 0o755); err != nil {
		slog.Error("create debug log dir failed", "error", err)
		return
	}

	filePath := filepath.Join(DefaultLogsDir, fmt.Sprintf("request_%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(filePath, payload, 0o600); err != nil {
		slog.Error("write debug request failed", "path", filePath, "error", err)
	}
}
