package serverruntime

import (
	"strings"
	"testing"
)

func TestComponentHeadMetadata(t *testing.T) {
	for _, action := range []bool{false, true} {
		writer, recorder := newTestGoxWriter(t, noCompressionConfig(), "", action)
		writer.res.response.Headers.Set("Content-Type", "text/html")
		writer.GoxPreparePageHead([]byte(`<script id="_GOX-ACTIONS">{}</script>`), nil, []byte(`<script type="importmap">{}</script>`), "")
		writer.Write([]byte("<html><head>"))
		writer.GoxWritePageHead()
		writer.GoxWritePageHead()
		writer.Write([]byte("</head></html>"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		want := 1
		if action {
			want = 0
		}
		if got := strings.Count(recorder.Body.String(), `type="importmap"`); got != want {
			t.Fatalf("action=%v maps=%d want=%d", action, got, want)
		}
	}
}
