package logger

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var (
	LogMu   sync.Mutex
	LogRows []string
)

func AddLog(msg string) {
	LogMu.Lock()
	defer LogMu.Unlock()
	if len(LogRows) > 1000 {
		LogRows = LogRows[1:]
	}
	LogRows = append(LogRows, fmt.Sprintf("[%s] %s", time.Now().Format(time.RFC3339), msg))
}

func GetLogs() []string {
	LogMu.Lock()
	defer LogMu.Unlock()
	res := make([]string, len(LogRows))
	copy(res, LogRows)
	return res
}

type MemoryHandler struct {
	parent slog.Handler
}

func NewMemoryHandler(parent slog.Handler) *MemoryHandler {
	return &MemoryHandler{parent: parent}
}

func (h *MemoryHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.parent.Enabled(ctx, level)
}

func (h *MemoryHandler) Handle(ctx context.Context, record slog.Record) error {
	attrs := ""
	record.Attrs(func(attr slog.Attr) bool {
		attrs += fmt.Sprintf(" %s=%v", attr.Key, attr.Value)
		return true
	})
	AddLog(fmt.Sprintf("%s: %s%s", record.Level, record.Message, attrs))
	return h.parent.Handle(ctx, record)
}

func (h *MemoryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &MemoryHandler{parent: h.parent.WithAttrs(attrs)}
}

func (h *MemoryHandler) WithGroup(name string) slog.Handler {
	return &MemoryHandler{parent: h.parent.WithGroup(name)}
}
