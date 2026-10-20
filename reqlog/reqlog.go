// Package reqlog ports app/middleware_log.py: request/response body snapshots
// for debugging, plus the request id formerly carried by a ContextVar.
package reqlog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Dir is the log directory. Python hardcodes /app/reqlog; REQLOG_DIR is the Go
// equivalent override used by the server and tests.
const defaultDir = "/app/reqlog"

type contextKey struct{}

// WithRequestID stores the request id in the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// RequestID returns the request id stored in the context ("" when absent).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// Dir returns the active log directory.
func Dir() string {
	if env := os.Getenv("REQLOG_DIR"); env != "" {
		return env
	}
	return defaultDir
}

var writeMu sync.Mutex

// Write persists one payload as <kind>_<rid>.json. Failures are swallowed, as
// in Python (logging must never break a request).
func Write(rid, kind, payload string) {
	writeMu.Lock()
	defer writeMu.Unlock()
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, kind+"_"+rid+".json"), []byte(payload), 0o644)
}

// Pointer is the compact stdout line emitted per logged request.
type Pointer struct {
	Tag   string `json:"__tf_log"`
	RID   string `json:"rid"`
	ReqB  int    `json:"reqb"`
	RespB int    `json:"respb"`
}

// PrintPointer writes the compact JSON pointer to stdout.
func PrintPointer(rid string, reqBytes, respBytes int) {
	encoded, err := json.Marshal(Pointer{Tag: "x", RID: rid, ReqB: reqBytes, RespB: respBytes})
	if err != nil {
		return
	}
	os.Stdout.Write(append(encoded, '\n'))
}

// LoggedPaths are the paths whose bodies are snapshotted.
var LoggedPaths = map[string]struct{}{
	"/v1/chat/completions": {}, "/chat/completions": {},
	"/v1/responses": {}, "/responses": {},
	"/v1/messages": {}, "/messages": {},
}
