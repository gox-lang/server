package lsp

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/gox-lang/server/internal/goxmap"
)

var (
	ErrDocumentAlreadyOpen           = errors.New("document is already open")
	ErrDocumentNotOpen               = errors.New("document is not open")
	ErrDocumentVersionNotIncreasing  = errors.New("document version is not increasing")
	ErrIncrementalChangesUnsupported = errors.New("incremental document changes are not supported")
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Document struct {
	URI         string
	Path        string
	LanguageID  string
	Version     int
	Text        string
	lineIndex   *goxmap.LineIndex
	indexedText string
}

// The index is immutable and belongs to this exact text version. Copies whose
// Text is replaced (e.g. completion recovery) must never reuse an old index.
func (d Document) positionIndex() *goxmap.LineIndex {
	if d.lineIndex != nil && d.indexedText == d.Text {
		return d.lineIndex
	}
	return goxmap.NewLineIndex(d.Text)
}
func (d Document) withLineIndex() Document {
	d.lineIndex = d.positionIndex()
	d.indexedText = d.Text
	return d
}

func (d Document) Offset(position Position) (int, error) {
	return d.positionIndex().UTF16Offset(goxmap.UTF16Position{
		Line:      position.Line,
		Character: position.Character,
	})
}

func (d Document) Position(offset int) (Position, error) {
	position, err := d.positionIndex().UTF16Position(offset)
	if err != nil {
		return Position{}, err
	}
	return Position{Line: position.Line, Character: position.Character}, nil
}

type DocumentStore struct {
	mu        sync.RWMutex
	documents map[string]Document
}

func NewDocumentStore() *DocumentStore {
	return &DocumentStore{documents: make(map[string]Document)}
}

func (s *DocumentStore) Open(document TextDocumentItem) error {
	path, err := FilePath(document.URI)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.documents[document.URI]; exists {
		return ErrDocumentAlreadyOpen
	}
	s.documents[document.URI] = Document{
		URI:        document.URI,
		Path:       path,
		LanguageID: document.LanguageID,
		Version:    document.Version,
		Text:       document.Text,
	}.withLineIndex()
	return nil
}

func (s *DocumentStore) Change(identifier VersionedTextDocumentIdentifier, changes []TextDocumentContentChangeEvent) error {
	if len(changes) == 0 {
		return fmt.Errorf("document change has no content")
	}
	for _, change := range changes {
		if change.Range != nil || change.RangeLength != nil {
			return ErrIncrementalChangesUnsupported
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	document, exists := s.documents[identifier.URI]
	if !exists {
		return ErrDocumentNotOpen
	}
	if identifier.Version <= document.Version {
		return ErrDocumentVersionNotIncreasing
	}
	document.Version = identifier.Version
	document.Text = changes[len(changes)-1].Text
	s.documents[identifier.URI] = document.withLineIndex()
	return nil
}

func (s *DocumentStore) Close(uri string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.documents[uri]; !exists {
		return false
	}
	delete(s.documents, uri)
	return true
}

func (s *DocumentStore) Get(uri string) (Document, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	document, exists := s.documents[uri]
	return document, exists
}

func (s *DocumentStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.documents)
}

func (s *DocumentStore) Documents() []Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	documents := make([]Document, 0, len(s.documents))
	for _, document := range s.documents {
		documents = append(documents, document)
	}
	return documents
}

func FilePath(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("document URI %q does not use file://", uri)
	}
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		return "", fmt.Errorf("document URI %q uses unsupported host %q", uri, parsed.Host)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("document URI %q contains query or fragment data", uri)
	}
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	path = filepath.Clean(filepath.FromSlash(path))
	if path == "." || !filepath.IsAbs(path) {
		return "", fmt.Errorf("document URI %q does not contain an absolute path", uri)
	}
	return path, nil
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentContentChangeEvent struct {
	Range       *Range `json:"range,omitempty"`
	RangeLength *int   `json:"rangeLength,omitempty"`
	Text        string `json:"text"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}
