package serverruntime

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox/server"
)

type requestPipelineTrace struct {
	enabled bool
	deep    bool
	start   time.Time
	fields  []string
	stages  []requestPipelineStage
}

type requestPipelineStage struct {
	name     string
	duration time.Duration
}

func newRequestPipelineTrace(enabled bool, deep bool) *requestPipelineTrace {
	trace := &requestPipelineTrace{
		enabled: enabled,
		deep:    deep,
	}
	if enabled {
		trace.start = time.Now()
	}
	return trace
}

func (t *requestPipelineTrace) now() time.Time {
	if t == nil || !t.enabled {
		return time.Time{}
	}
	return time.Now()
}

func (t *requestPipelineTrace) nowDeep() time.Time {
	if t == nil || !t.enabled || !t.deep {
		return time.Time{}
	}
	return time.Now()
}

func (t *requestPipelineTrace) add(name string, started time.Time) {
	if t == nil || !t.enabled || started.IsZero() {
		return
	}
	t.stages = append(t.stages, requestPipelineStage{name: name, duration: time.Since(started)})
}

func (t *requestPipelineTrace) addDeep(name string, started time.Time) {
	if t == nil || !t.enabled || !t.deep || started.IsZero() {
		return
	}
	t.stages = append(t.stages, requestPipelineStage{name: name, duration: time.Since(started)})
}

func (t *requestPipelineTrace) addDuration(name string, duration time.Duration) {
	if t == nil || !t.enabled {
		return
	}
	t.stages = append(t.stages, requestPipelineStage{name: name, duration: duration})
}

func (t *requestPipelineTrace) addDeepField(name string, value string) {
	if t == nil || !t.enabled || !t.deep {
		return
	}
	t.fields = append(t.fields, name+"="+value)
}

func (t *requestPipelineTrace) addDeepDuration(name string, duration time.Duration) {
	if t == nil || !t.enabled || !t.deep {
		return
	}
	t.stages = append(t.stages, requestPipelineStage{name: name, duration: duration})
}

func (t *requestPipelineTrace) addSessionTrace(sessionTrace *server.SessionTrace) {
	if t == nil || !t.enabled || !t.deep || sessionTrace == nil || !sessionTrace.Enabled {
		return
	}

	for _, field := range sessionTrace.Fields() {
		if field.Name == "" {
			continue
		}
		t.fields = append(t.fields, field.Name+"="+field.Value)
	}

	for _, stage := range sessionTrace.Stages() {
		if stage.Name == "" {
			continue
		}
		t.stages = append(t.stages, requestPipelineStage{name: stage.Name, duration: stage.Duration})
	}
}

func (t *requestPipelineTrace) logHTTP(status int, method string, path string, writer *GoxWriter) {
	if t == nil || !t.enabled {
		return
	}

	parts := make([]string, 0, len(t.fields)+len(t.stages)+8)
	parts = append(parts, "total="+time.Since(t.start).String())
	parts = append(parts, t.fields...)
	for _, stage := range t.stages {
		parts = append(parts, stage.name+"="+stage.duration.String())
	}

	if writer != nil {
		stats := writer.Stats()
		parts = append(parts,
			"bytesIn="+formatInt64(stats.BytesIn),
			"bytesOut="+formatInt64(stats.BytesOut),
			"flushes="+formatInt64(stats.FlushCount),
			"buffer="+formatInt(stats.BufferSize),
			"compressed="+strconv.FormatBool(stats.Compressed),
		)
		if stats.Encoding != "" {
			parts = append(parts, "encoding="+stats.Encoding)
		}
		if stats.CompressionDuration > 0 {
			parts = append(parts, "writer.compress="+stats.CompressionDuration.String())
		}
	}

	logs.Trace("http status=%d method=%s path=%s %s", status, method, path, strings.Join(parts, " "))
}

func devEnabled(config server.DevConfig) bool {
	return config.Enabled
}

func devTraceEnabled(config server.DevConfig) bool {
	return config.Enabled && config.Logs.Trace
}

func devTraceDeepEnabled(config server.DevConfig) bool {
	return config.Enabled && config.Logs.Trace && config.Logs.TraceDeep
}

func devGoxTraceEnabled(config server.DevConfig) bool {
	return config.Enabled && config.Logs.GoxFile && config.Logs.Trace
}

func devGoxTraceDeepEnabled(config server.DevConfig) bool {
	return config.Enabled && config.Logs.GoxFile && config.Logs.Trace && config.Logs.TraceDeep
}

func shouldTraceRequest(config server.DevConfig, kind string, path string) bool {
	if !config.Enabled || !config.Logs.Trace {
		return false
	}
	return shouldLogRequest(config, kind, path)
}

func requestLoggingPossible(config server.DevConfig, path string) bool {
	if !config.Enabled {
		return filepath.Ext(path) == ""
	}
	return config.Logs.Static || config.Logs.GoxFile
}

func shouldLogRequest(config server.DevConfig, kind string, path string) bool {
	if !config.Enabled {
		return filepath.Ext(path) == ""
	}

	switch kind {
	case "static":
		return config.Logs.Static
	case "gox":
		return config.Logs.GoxFile
	case "404":
		if isStaticLikeRequestPath(path) {
			return config.Logs.Static
		}
		return config.Logs.GoxFile
	default:
		return true
	}
}

func isStaticLikeRequestPath(path string) bool {
	if path == "" || path == "/" {
		return false
	}
	if strings.HasPrefix(path, "/.well-known/") {
		return true
	}
	return filepath.Ext(path) != ""
}

func formatInt(value int) string {
	return formatInt64(int64(value))
}

func formatInt64(value int64) string {
	if value == 0 {
		return "0"
	}

	var buf [20]byte
	i := len(buf)
	v := value
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
