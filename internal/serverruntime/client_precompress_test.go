package serverruntime

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"

	"github.com/gox-lang/gox/server"
)

func TestBuildClientCompressedArtifactPathUsesSiblingLevelEncoding(t *testing.T) {
	got := buildClientCompressedArtifactPath("/tmp/menu.js", "br", 7)
	want := "/tmp/menu.js.l7.br"
	if got != want {
		t.Fatalf("artifact path = %q, want %q", got, want)
	}
}

func TestPrecompressClientAssetsWritesConfiguredSiblingArtifacts(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".build", "client", "react", "prod", "menu.js")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("console.log('gox');\n"), 128)
	if err := os.WriteFile(file, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	policy := NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientAssetMap: map[string]string{"/_gox/client/menu.js": file},
		ClientBundles: []goxcompiler.CompileClientBundle{{
			Name:      "menu",
			Framework: "react",
			Mode:      "prod",
			EntryURL:  "/_gox/client/menu.js",
			EntryFile: file,
			Assets:    []goxcompiler.CompileClientAsset{{Kind: "entry", URL: "/_gox/client/menu.js", File: file}},
		}},
	})
	cfg := server.CompressionConfig{
		Enabled:       true,
		OrderPriority: []string{"gzip"},
		Gzip:          server.CompressionAlgorithmConfig{Enabled: true, Level: 6},
	}

	precompressClientAssets(root, cfg, policy)

	artifact := file + ".l6.gzip"
	compressed, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("expected gzip artifact %s: %v", artifact, err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gzip artifact is invalid: %v", err)
	}
	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("read gzip artifact: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("decompressed artifact does not match source")
	}
}

func TestPrecompressClientAssetsSkipsDevBundles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".build", "client", "react", "dev", "menu.js")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("console.log('dev')"), 0o644); err != nil {
		t.Fatal(err)
	}

	policy := NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientAssetMap: map[string]string{"/_gox/client/menu.js": file},
		ClientBundles: []goxcompiler.CompileClientBundle{{
			Name:      "menu",
			Framework: "react",
			Mode:      "dev",
			EntryURL:  "/_gox/client/menu.js",
			EntryFile: file,
			Assets:    []goxcompiler.CompileClientAsset{{Kind: "entry", URL: "/_gox/client/menu.js", File: file}},
		}},
	})
	cfg := server.CompressionConfig{
		Enabled:       true,
		OrderPriority: []string{"gzip"},
		Gzip:          server.CompressionAlgorithmConfig{Enabled: true, Level: 6},
	}

	precompressClientAssets(root, cfg, policy)

	if _, err := os.Stat(file + ".l6.gzip"); !os.IsNotExist(err) {
		t.Fatalf("dev artifact should not exist, stat err=%v", err)
	}
}

func TestResolveClientCompressedArtifact(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".build", "client", "react", "prod", "menu.js")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := file + ".l6.gzip"
	if err := os.WriteFile(artifact, []byte("gzip-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, info, err := resolveClientCompressedArtifact(file, "gzip", 6)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got != artifact || info.Size() != int64(len("gzip-bytes")) {
		t.Fatalf("resolved = %q size=%d; want %q", got, info.Size(), artifact)
	}
}
