package mocklogger

import (
	"context"
	"log/slog"
	"maps"
	"reflect"
	"strings"
	"sync"
	"time"
)

// LoggedEntry stores a captured log record.
type LoggedEntry struct {
	Level   slog.Level
	Message string
	Time    time.Time
	Attrs   map[string]any
}

// ObservedLogs stores log entries in capture order and protects collection access with a mutex. Returned entries share the stored attribute maps.
type ObservedLogs struct {
	mu   sync.RWMutex
	logs []LoggedEntry
}

func (o *ObservedLogs) Len() int {
	o.mu.RLock()
	n := len(o.logs)
	o.mu.RUnlock()
	return n
}

// All returns a copy of the log entries.
func (o *ObservedLogs) All() []LoggedEntry {
	o.mu.RLock()
	ret := make([]LoggedEntry, len(o.logs))
	copy(ret, o.logs)
	o.mu.RUnlock()
	return ret
}

// TakeAll returns the stored log entries and clears the collection.
func (o *ObservedLogs) TakeAll() []LoggedEntry {
	o.mu.Lock()
	ret := o.logs
	o.logs = nil
	o.mu.Unlock()
	return ret
}

// AllUntimed copies the log entries and sets their timestamps to the zero time.
func (o *ObservedLogs) AllUntimed() []LoggedEntry {
	ret := o.All()
	for i := range ret {
		ret[i].Time = time.Time{}
	}
	return ret
}

// FilterLevelExact selects records with the specified level.
func (o *ObservedLogs) FilterLevelExact(level slog.Level) *ObservedLogs {
	return o.Filter(func(e LoggedEntry) bool {
		return e.Level == level
	})
}

// FilterMessage selects records whose message equals msg.
func (o *ObservedLogs) FilterMessage(msg string) *ObservedLogs {
	return o.Filter(func(e LoggedEntry) bool {
		return e.Message == msg
	})
}

// FilterMessageSnippet selects records whose message contains snippet.
func (o *ObservedLogs) FilterMessageSnippet(snippet string) *ObservedLogs {
	return o.Filter(func(e LoggedEntry) bool {
		return strings.Contains(e.Message, snippet)
	})
}

// FilterAttrKey selects records that contain the attribute key.
func (o *ObservedLogs) FilterAttrKey(key string) *ObservedLogs {
	return o.Filter(func(e LoggedEntry) bool {
		_, ok := e.Attrs[key]
		return ok
	})
}

// FilterAttr selects records with key and a value equal under reflect.DeepEqual. See https://pkg.go.dev/reflect#DeepEqual.
func (o *ObservedLogs) FilterAttr(key string, value any) *ObservedLogs {
	return o.Filter(func(e LoggedEntry) bool {
		v, ok := e.Attrs[key]
		return ok && reflect.DeepEqual(v, value)
	})
}

// Filter returns the entries that satisfy keep in a new collection.
func (o *ObservedLogs) Filter(keep func(LoggedEntry) bool) *ObservedLogs {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var filtered []LoggedEntry
	for _, entry := range o.logs {
		if keep(entry) {
			filtered = append(filtered, entry)
		}
	}
	return &ObservedLogs{logs: filtered}
}

func (o *ObservedLogs) add(entry LoggedEntry) {
	o.mu.Lock()
	o.logs = append(o.logs, entry)
	o.mu.Unlock()
}

type observerHandler struct {
	level slog.Level
	logs  *ObservedLogs
	attrs map[string]any
}

// NewObserverHandler returns a handler and its log collection. The handler accepts records at or above level.
func NewObserverHandler(level slog.Level) (slog.Handler, *ObservedLogs) {
	ol := &ObservedLogs{}
	return &observerHandler{
		level: level,
		logs:  ol,
		attrs: make(map[string]any),
	}, ol
}

func (h *observerHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *observerHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, len(h.attrs))
	maps.Copy(attrs, h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})

	h.logs.add(LoggedEntry{
		Level:   r.Level,
		Message: r.Message,
		Time:    r.Time,
		Attrs:   attrs,
	})
	return nil
}

func (h *observerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make(map[string]any, len(h.attrs)+len(attrs))
	maps.Copy(newAttrs, h.attrs)
	for _, a := range attrs {
		newAttrs[a.Key] = a.Value.Any()
	}
	return &observerHandler{
		level: h.level,
		logs:  h.logs,
		attrs: newAttrs,
	}
}

func (h *observerHandler) WithGroup(name string) slog.Handler {
	_ = name
	return h
}
