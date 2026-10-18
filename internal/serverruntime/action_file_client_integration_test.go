package serverruntime

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gox-lang/gox"
)

func TestActionFilesReachJavaScriptRuntimeEndToEnd(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}

	handlerErr := make(chan error, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, state := newRuntimeResponse(w, nil)
		defer unregisterRuntimeResponse(res)
		writer := newGoxWriter(state, r, noCompressionConfig(), true, false)
		ctx := &gox.Context{Response: res, Writer: writer}

		var writeErr error
		switch r.Header.Get("Invoke-Action") {
		case "download":
			writeErr = writeActionIntegrationFile(ctx, "hello.txt", "text/plain; charset=utf-8", strings.NewReader("hello from gox"))
		case "downloadUnicode":
			writeErr = writeActionIntegrationFile(ctx, "reporte año.txt", "text/plain", strings.NewReader("contenido unicode"))
		case "downloadMany":
			first, err := gox.NewFile(gox.FileConfig{Name: "first.txt", Type: "text/plain", Content: strings.NewReader("first from gox")})
			if err != nil {
				writeErr = err
				break
			}
			second, err := gox.NewFile(gox.FileConfig{Name: "second.bin", Type: "application/octet-stream", Content: bytes.NewReader([]byte{0, 1, 2, 255})})
			if err != nil {
				writeErr = err
				break
			}
			writeErr = gox.WriteActionFiles(ctx, []gox.File{first, second})
		case "downloadNone":
			writeErr = gox.WriteActionFiles(ctx, nil)
		case "downloadEmptyEmoji":
			writeErr = writeActionIntegrationFile(ctx, "empty-🚀.txt", "text/plain", strings.NewReader(""))
		case "downloadDuplicates":
			first, err := gox.NewFile(gox.FileConfig{Name: "same-🚀.txt", Type: "text/plain", Content: strings.NewReader("")})
			if err != nil {
				writeErr = err
				break
			}
			second, err := gox.NewFile(gox.FileConfig{Name: "same-🚀.txt", Type: "text/plain", Content: strings.NewReader("second")})
			if err != nil {
				writeErr = err
				break
			}
			writeErr = gox.WriteActionFiles(ctx, []gox.File{first, second})
		default:
			writeErr = fmt.Errorf("unexpected action %q", r.Header.Get("Invoke-Action"))
		}
		if writeErr == nil {
			writeErr = writer.Close()
		}
		if writeErr != nil {
			select {
			case handlerErr <- writeErr:
			default:
			}
		}
	}))
	defer server.Close()

	root := actionFileIntegrationRoot(t)
	script := filepath.Join(root, "internal", "clientbuilder", "actions_file_integration.mjs")
	cmd := exec.Command(node, script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOX_ACTIONS_TEST_URL="+server.URL+"/downloads")
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("JavaScript integration failed: %v\n%s", runErr, output)
	}
	select {
	case err := <-handlerErr:
		t.Fatalf("Action response handler failed: %v", err)
	default:
	}
}

func writeActionIntegrationFile(ctx *gox.Context, name string, contentType string, content *strings.Reader) error {
	file, err := gox.NewFile(gox.FileConfig{Name: name, Type: contentType, Content: content})
	if err != nil {
		return err
	}
	return gox.WriteActionFile(ctx, file)
}

func actionFileIntegrationRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve integration test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}
