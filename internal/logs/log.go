package logs

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var fgcolor = struct {
	red     string
	green   string
	blue    string
	yellow  string
	cyan    string
	magenta string
	white   string
	reset   string
}{
	red:     "\033[31m",
	green:   "\033[32m",
	blue:    "\033[34m",
	yellow:  "\033[33m",
	cyan:    "\033[36m",
	magenta: "\033[35m",
	white:   "\033[37m",
	reset:   "\033[0m",
}

type message struct {
	header []headerItem
	format string
	args   []any
	flush  chan struct{}
}

type headerItem struct {
	color string
	value any
}

var (
	initOnce       sync.Once
	logChan        chan message
	terminalMu     sync.Mutex
	enabled        = true
	enabledMu      sync.RWMutex
	requestEnabled = true
	requestMu      sync.RWMutex
)

func SetEnabled(value bool) {
	enabledMu.Lock()
	enabled = value
	enabledMu.Unlock()
}

func Enabled() bool {
	enabledMu.RLock()
	value := enabled
	enabledMu.RUnlock()
	return value
}

func SetRequestEnabled(value bool) {
	requestMu.Lock()
	requestEnabled = value
	requestMu.Unlock()
}

func RequestEnabled() bool {
	requestMu.RLock()
	value := requestEnabled
	requestMu.RUnlock()
	return value
}

func Init(bufferSize int) {
	if bufferSize <= 0 {
		bufferSize = 1024
	}

	initOnce.Do(func() {
		log.SetOutput(os.Stdout)

		logChan = make(chan message, bufferSize)

		go func() {
			for msg := range logChan {
				if msg.flush != nil {
					close(msg.flush)
					continue
				}

				format := msg.format
				if len(msg.header) > 0 {
					var prefix strings.Builder
					for _, h := range msg.header {
						prefix.WriteString(h.color)
						prefix.WriteByte('[')
						fmt.Fprint(&prefix, h.value)
						prefix.WriteByte(']')
						prefix.WriteString(fgcolor.reset)
						prefix.WriteByte(' ')
					}

					prefix.WriteByte(' ')
					prefix.WriteString(format)
					format = prefix.String()
				}

				terminalMu.Lock()
				clearProgressLine()
				log.Printf(format, msg.args...)
				terminalMu.Unlock()
			}
		}()
	})
}

func Close() {
	if logChan != nil {
		close(logChan)
	}
}

func Flush() {
	Init(1024)

	done := make(chan struct{})

	select {
	case logChan <- message{flush: done}:
		<-done
	default:
		terminalMu.Lock()
		terminalMu.Unlock()
	}
}

func send(header []headerItem, format string, args ...any) {
	if !Enabled() {
		return
	}
	Init(1024)

	msg := message{
		header: header,
		format: format,
		args:   args,
	}

	select {
	case logChan <- msg:
	default:
		// Never block the request path because logging is saturated.
	}
}

func Info(format string, v ...any) {
	send([]headerItem{{color: fgcolor.green, value: "Info"}}, format, v...)
}

func Error(format string, v ...any) {
	send([]headerItem{{color: fgcolor.red, value: "Error"}}, format, v...)
}

func Warn(format string, v ...any) {
	send([]headerItem{{color: fgcolor.yellow, value: "Warn"}}, format, v...)
}

func Warm(format string, v ...any) {
	send([]headerItem{{color: fgcolor.yellow, value: "Warm"}}, format, v...)
}

func Trace(format string, v ...any) {
	send([]headerItem{{color: fgcolor.cyan, value: "Trace"}}, format, v...)
}

func Struct(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		Error("struct log marshal failed: %v", err)
		return
	}

	send([]headerItem{{color: fgcolor.magenta, value: "Struct"}}, "%s", string(b))
}

func Progress(label string, format string, v ...any) {
	if !Enabled() {
		return
	}
	Init(1024)

	terminalMu.Lock()
	defer terminalMu.Unlock()

	timestamp := time.Now().Format("2006/01/02 15:04:05")
	message := fmt.Sprintf(format, v...)
	label = strings.TrimSpace(label)

	clearProgressLine()

	if label == "" {
		fmt.Fprintf(
			os.Stdout,
			"%s %s[%s]%s  %s",
			timestamp,
			fgcolor.cyan,
			"Progress",
			fgcolor.reset,
			message,
		)
		return
	}

	fmt.Fprintf(
		os.Stdout,
		"%s %s[%s]%s  %s: %s",
		timestamp,
		fgcolor.cyan,
		"Progress",
		fgcolor.reset,
		label,
		message,
	)
}

func ProgressDone() {
	if !Enabled() {
		return
	}
	Init(1024)

	terminalMu.Lock()
	defer terminalMu.Unlock()

	fmt.Fprintln(os.Stdout)
}

func clearProgressLine() {
	fmt.Fprint(os.Stdout, "\r\033[K")
}

func Request(status int, elapsed time.Duration, method string, path string, isDev bool) {
	if !Enabled() || !RequestEnabled() {
		return
	}
	if !isDev && filepath.Ext(path) != "" {
		return
	}

	statusColor := fgcolor.white
	switch {
	case status >= 500:
		statusColor = fgcolor.red
	case status >= 400:
		statusColor = fgcolor.yellow
	case status >= 300:
		statusColor = fgcolor.magenta
	case status >= 200:
		statusColor = fgcolor.green
	case status >= 100:
		statusColor = fgcolor.cyan
	}

	send(
		requestHeader(status, elapsed, method, statusColor),
		"%s",
		path,
	)
}

func requestHeader(status int, elapsed time.Duration, method, statusColor string) []headerItem {
	return []headerItem{
		{color: statusColor, value: status},
		{color: fgcolor.cyan, value: elapsed},
		{color: fgcolor.magenta, value: method},
	}
}
