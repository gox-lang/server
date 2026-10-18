package serverruntime

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"
)

func TestBuildFilesUsesNewFileConfigAndPreservesMetadata(t *testing.T) {
	t.Parallel()

	values := map[string][]*multipart.FileHeader{
		"documents": {
			newMultipartFileHeader(t, "report.txt", "text/plain", []byte("first")),
			newMultipartFileHeader(t, "data.json", "application/custom+json", []byte(`{"ok":true}`)),
		},
	}

	files, _ := buildFiles(values).GetAll("documents")
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	if got := files[0].Name(); got != "report.txt" {
		t.Fatalf("unexpected first name: %q", got)
	}
	if got := files[0].Type(); got != "text/plain" {
		t.Fatalf("unexpected first MIME type: %q", got)
	}
	if got := files[0].Size(); got != 5 {
		t.Fatalf("unexpected first size: %d", got)
	}
	assertFileContent(t, files[0], "first")

	if got := files[1].Name(); got != "data.json" {
		t.Fatalf("unexpected second name: %q", got)
	}
	if got := files[1].Type(); got != "application/custom+json" {
		t.Fatalf("unexpected second MIME type: %q", got)
	}
	assertFileContent(t, files[1], `{"ok":true}`)
}

func TestBuildFilesInfersMissingMIMETypeAndKeepsReadersIndependent(t *testing.T) {
	t.Parallel()

	values := map[string][]*multipart.FileHeader{
		"upload": {
			newMultipartFileHeader(t, "payload.json", "", []byte(`{"value":1}`)),
		},
	}

	file, _ := buildFiles(values).Get("upload")
	if file.IsZero() {
		t.Fatal("expected a file")
	}
	if got := file.Type(); got != "application/json" {
		t.Fatalf("expected inferred application/json MIME type, got %q", got)
	}

	readerA, err := file.Open()
	if err != nil {
		t.Fatalf("open first reader: %v", err)
	}
	defer readerA.Close()
	readerB, err := file.Open()
	if err != nil {
		t.Fatalf("open second reader: %v", err)
	}
	defer readerB.Close()

	first := make([]byte, 1)
	if _, err := io.ReadFull(readerA, first); err != nil {
		t.Fatalf("read first reader: %v", err)
	}
	allB, err := io.ReadAll(readerB)
	if err != nil {
		t.Fatalf("read second reader: %v", err)
	}
	if string(allB) != `{"value":1}` {
		t.Fatalf("second reader shared cursor: %q", allB)
	}
}

func TestBuildFilesAllowsMetadataChangesWithoutChangingContent(t *testing.T) {
	t.Parallel()

	values := map[string][]*multipart.FileHeader{
		"upload": {
			newMultipartFileHeader(t, "original.txt", "", []byte("content")),
		},
	}

	file, _ := buildFiles(values).Get("upload")
	if err := file.SetName("renamed.json"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if got := file.Type(); got != "application/json" {
		t.Fatalf("expected MIME reinference after rename, got %q", got)
	}
	if err := file.SetMIMEType("application/x-gox"); err != nil {
		t.Fatalf("set MIME type: %v", err)
	}
	if err := file.SetName("final.pdf"); err != nil {
		t.Fatalf("set final name: %v", err)
	}
	if got := file.Type(); got != "application/x-gox" {
		t.Fatalf("explicit MIME changed after rename: %q", got)
	}
	assertFileContent(t, file, "content")
}

func newMultipartFileHeader(t *testing.T, name, contentType string, data []byte) *multipart.FileHeader {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if err := request.ParseMultipartForm(int64(len(data) + 4096)); err != nil {
		t.Fatalf("parse multipart form: %v", err)
	}
	return request.MultipartForm.File["file"][0]
}

func assertFileContent(t *testing.T, file interface{ Open() (io.ReadCloser, error) }, expected string) {
	t.Helper()
	reader, err := file.Open()
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != expected {
		t.Fatalf("unexpected content: %q", data)
	}
}
