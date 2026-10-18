package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditBurstCompletesLatestTemplateBeforeDiagnostics(t *testing.T) {
	uri := "file:///tmp/gox-edit-burst.gox"
	source := func(n int) string {
		return fmt.Sprintf("module page\nclient Menu react { src: \"menu.jsx\" }\nfunc Render() string { return <@Menu name=\"v%d\" ad /> }\n", n)
	}
	var input strings.Builder
	send := func(v any) { b, _ := json.Marshal(v); input.WriteString(framedMessage(string(b))) }
	send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "gox", "version": 1, "text": source(1)}}})
	for n := 2; n <= 31; n++ {
		send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "version": n}, "contentChanges": []map[string]any{{"text": source(n)}}}})
	}
	doc := Document{Text: source(31)}
	pos, _ := doc.Position(strings.LastIndex(doc.Text, " ad ") + 3)
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "textDocument/completion", "params": map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}})
	send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "shutdown"})
	send(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	s := NewServer()
	var output bytes.Buffer
	if err := s.Serve(strings.NewReader(input.String()), &output); err != nil {
		t.Fatal(err)
	}
	for _, r := range decodeFramedResponses(t, output.String()) {
		if string(r.ID) == "2" {
			var list CompletionList
			if err := json.Unmarshal(r.Result, &list); err != nil {
				t.Fatal(err)
			}
			requireCompletionItem(t, list.Items, "adapt")
		}
	}
	completed := false
	latest := false
	for _, m := range decodeFramedMessages(t, output.String()) {
		if string(m.ID) == "2" {

			completed = true
		}
		if m.Method == "textDocument/publishDiagnostics" {
			var p PublishDiagnosticsParams
			_ = json.Unmarshal(m.Params, &p)
			if p.Version > 1 {
				if p.Version != 31 {
					t.Fatalf("obsolete diagnostics: %d", p.Version)
				}
				if !completed {
					t.Fatal("diagnostics blocked completion")
				}
				latest = true
			}
		}
	}
	if !completed || !latest {
		t.Fatalf("completion=%v latest diagnostics=%v", completed, latest)
	}
	if doc, ok := s.Document(uri); !ok || doc.Text != source(31) {
		t.Fatal("lost latest edit")
	}
}

func TestEditBurstPublishesAfterIdle(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	done := make(chan error, 1)
	go func() { defer serverSide.Close(); done <- NewServer().Serve(serverSide, serverSide) }()
	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))
	send := func(body string) {
		t.Helper()
		if _, err := io.WriteString(clientSide, framedMessage(body)); err != nil {
			t.Fatal(err)
		}
	}
	conn := newConnection(clientSide, clientSide)
	read := func() message {
		t.Helper()
		body, err := conn.read()
		if err != nil {
			t.Fatal(err)
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	send(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/gox-idle.gox","languageId":"gox","version":1,"text":"module page"}}}`)
	read()
	for _, version := range []int{2, 3} {
		send(fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///tmp/gox-idle.gox","version":%d},"contentChanges":[{"text":"module page\nfunc broken("}]}}`, version))
	}
	m := read()
	var params PublishDiagnosticsParams
	_ = json.Unmarshal(m.Params, &params)
	if m.Method != "textDocument/publishDiagnostics" || params.Version != 3 || len(params.Diagnostics) == 0 {
		t.Fatalf("latest idle diagnostics: %#v %#v", m, params)
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}`)
	read()
	send(`{"jsonrpc":"2.0","method":"exit"}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLargeTemplateClientControlsAvoidSemanticRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := "module page\nclient Menu react { src: \"menu.jsx\" }\nfunc Render() string { return <main>\n" + strings.Repeat("<@Menu />\n", 4400) + "<@Menu />\n</main> }"
	server := NewServer()
	indexCompletionDocument(t, server, path, source)
	server.analysis = newAnalysisStore(func(string, Document) (*goxcompiler.Module, error) {
		t.Fatal("static client attribute completion rebuilt semantic analysis")
		return nil, nil
	})
	for _, tag := range []string{`<@Menu ad/*cursor*/ />`, `<@Menu adapt="bo/*cursor*/" />`} {
		text := strings.Replace(source, "<@Menu />\n</main>", tag+"\n</main>", 1)
		items := lexicalItemsAtMarker(t, server, Document{URI: FileURI(path), Path: path, Text: text}, "/*cursor*/")
		if strings.Contains(tag, `adapt=`) {
			requireCompletionItem(t, items, "bottom")
		} else {
			requireCompletionItem(t, items, "adapt")
		}
	}
}

func TestFastClientControlsRespectOtherLanguages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	server := NewServer()
	base := "module page\nclient Menu react { src: \"menu.jsx\" }\n"
	indexCompletionDocument(t, server, path, base)
	for _, suffix := range []string{
		"func F(){ s := `return <@Menu ad/*cursor*/` }",
		"// return <@Menu ad/*cursor*/",
		`func F() string { return <script>const s = "<@Menu ad/*cursor*/";</script> }`,
		`func F() string { return <@Menu name={{ ad/*cursor*/ }} /> }`,
	} {
		text := base + suffix
		offset := strings.Index(text, "/*cursor*/")
		text = strings.Replace(text, "/*cursor*/", "", 1)
		doc := Document{URI: FileURI(path), Path: path, Text: text}
		pos, _ := doc.Position(offset)
		if _, ok := server.fastClientAttributeCompletion(doc, pos); ok {
			t.Fatalf("wrong language ownership: %s", suffix)
		}
	}
}
