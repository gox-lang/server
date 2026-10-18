package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/buildinfo"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

var ErrExitWithoutShutdown = errors.New("LSP client exited without shutdown")

type Server struct {
	documents           *DocumentStore
	symbols             *WorkspaceIndex
	goPackages          *GoPackageIndex
	analysis            *AnalysisStore
	workspaceRoots      []string
	diagnosticsDebounce time.Duration
}

func NewServer() *Server {
	return &Server{
		diagnosticsDebounce: 150 * time.Millisecond,
		documents:           NewDocumentStore(),
		symbols:             NewWorkspaceIndex(),
		goPackages:          NewGoPackageIndex(),
		analysis:            NewAnalysisStore(),
	}
}

func (s *Server) Document(uri string) (Document, bool) {
	return s.documents.Get(uri)
}

func (s *Server) DocumentCount() int {
	return s.documents.Len()
}

func (s *Server) SymbolDefinitions(name string) []IndexedSymbol {
	return s.symbols.Definitions(name)
}

func (s *Server) Serve(reader io.Reader, writer io.Writer) error {
	conn := newConnection(reader, writer)
	type incomingMessage struct {
		request  message
		readErr  error
		parseErr bool
	}
	incoming := make(chan incomingMessage, 64)
	go func() {
		defer close(incoming)
		for {
			body, err := conn.read()
			if err != nil {
				incoming <- incomingMessage{readErr: err}
				return
			}
			var request message
			if err := json.Unmarshal(body, &request); err != nil {
				incoming <- incomingMessage{parseErr: true}
				continue
			}
			if request.Method == "$/cancelRequest" {
				var params struct {
					ID json.RawMessage `json:"id"`
				}
				if json.Unmarshal(request.Params, &params) == nil {
					conn.cancel(params.ID)
				}
				continue
			}
			incoming <- incomingMessage{request: request}
		}
	}()
	// Keep mutations and analysis on the serving goroutine, but coalesce edit
	// bursts before publishing diagnostics. Completion must not queue behind
	// diagnostics for every intermediate, already obsolete document version.
	pendingChanges := make(map[string]bool)
	var diagnosticsTimer *time.Timer
	var diagnosticsTick <-chan time.Time
	defer func() {
		if diagnosticsTimer != nil {
			diagnosticsTimer.Stop()
		}
	}()
	refreshPending := func() {
		for uri, dirty := range pendingChanges {
			if dirty {
				s.refreshOpenDocument(uri)
				pendingChanges[uri] = false
			}
		}
	}
	flushPending := func() error {
		refreshPending()
		for uri := range pendingChanges {
			if err := s.publishAffectedDiagnostics(conn, uri); err != nil {
				return err
			}
			delete(pendingChanges, uri)
		}
		diagnosticsTick = nil
		return nil
	}
	shutdown := false
	for {
		var item incomingMessage
		var ok bool
		select {
		case item, ok = <-incoming:
			if !ok {
				return flushPending()
			}
		case <-diagnosticsTick:
			if err := flushPending(); err != nil {
				return err
			}
			continue
		}

		if item.readErr != nil {
			if errors.Is(item.readErr, io.EOF) {
				return flushPending()
			}
			return item.readErr
		}
		if item.parseErr {
			if writeErr := conn.write(errorResponse(json.RawMessage("null"), -32700, "Parse error")); writeErr != nil {
				return writeErr
			}
			continue
		}
		request := item.request
		if request.Method == "exit" {
			if shutdown {
				return nil
			}
			return ErrExitWithoutShutdown
		}
		if s.diagnosticsDebounce > 0 && len(request.ID) == 0 && request.Method == "textDocument/didChange" {
			var params DidChangeTextDocumentParams
			if json.Unmarshal(request.Params, &params) == nil {
				if err := s.documents.Change(params.TextDocument, params.ContentChanges); err == nil {
					pendingChanges[params.TextDocument.URI] = true
					if document, ok := s.documents.Get(params.TextDocument.URI); ok && s.analysis != nil {
						s.analysis.InvalidateDependencyPathExcept(document.Path, document.URI)
					}
					if diagnosticsTimer == nil {
						diagnosticsTimer = time.NewTimer(s.diagnosticsDebounce)
					} else {
						diagnosticsTimer.Reset(s.diagnosticsDebounce)
					}
					diagnosticsTick = diagnosticsTimer.C
				}
			}
			continue
		}
		if len(request.ID) == 0 && request.Method == "textDocument/didClose" {
			var params DidCloseTextDocumentParams
			if json.Unmarshal(request.Params, &params) == nil {
				delete(pendingChanges, params.TextDocument.URI)
			}
		}
		if len(request.ID) != 0 && !conn.isCancelled(request.ID) {
			if request.Method == "textDocument/completion" {
				var params CompletionParams
				if json.Unmarshal(request.Params, &params) == nil {
					if document, ok := s.documents.Get(params.TextDocument.URI); ok {
						if list, handled := s.fastClientAttributeCompletion(document, params.Position); handled {
							result, err := json.Marshal(list)
							if err != nil {
								return err
							}
							if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
								return err
							}
							continue
						}
					}
				}
			}
			refreshPending()
			if request.Method == "shutdown" {
				if err := flushPending(); err != nil {
					return err
				}
			}
		}
		if len(request.ID) == 0 {
			if err := s.handleNotification(conn, request); err != nil {
				return err
			}
			continue
		}
		if conn.isCancelled(request.ID) {
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage("null")}); err != nil {
				return err
			}
			continue
		}
		switch request.Method {
		case "initialize":
			var params InitializeParams
			if len(request.Params) != 0 {
				_ = json.Unmarshal(request.Params, &params)
			}
			s.indexWorkspaceRoots(workspaceRoots(params))
			result, err := json.Marshal(map[string]any{
				"capabilities": map[string]any{
					"positionEncoding":                "utf-16",
					"textDocumentSync":                map[string]any{"openClose": true, "change": 1},
					"definitionProvider":              true,
					"typeDefinitionProvider":          true,
					"implementationProvider":          true,
					"callHierarchyProvider":           true,
					"typeHierarchyProvider":           true,
					"referencesProvider":              true,
					"documentHighlightProvider":       true,
					"documentSymbolProvider":          true,
					"foldingRangeProvider":            true,
					"selectionRangeProvider":          true,
					"documentFormattingProvider":      true,
					"documentRangeFormattingProvider": true,
					"workspaceSymbolProvider":         true,
					"renameProvider":                  map[string]any{"prepareProvider": true},
					"linkedEditingRangeProvider":      true,
					"hoverProvider":                   true,
					"inlayHintProvider":               true,
					"codeActionProvider": map[string]any{
						"codeActionKinds": []string{"quickfix", "source.organizeImports"},
					},
					"completionProvider":               map[string]any{"triggerCharacters": []string{".", ":", "&", "@", " ", "=", "\"", "{"}},
					"signatureHelpProvider":            map[string]any{"triggerCharacters": []string{"(", ","}, "retriggerCharacters": []string{","}},
					"documentOnTypeFormattingProvider": map[string]any{"firstTriggerCharacter": ">"},
					"semanticTokensProvider": map[string]any{
						"legend": map[string]any{
							"tokenTypes":     semanticTokenTypes,
							"tokenModifiers": []string{},
						},
						"full": true,
					},
					"workspace": map[string]any{
						"fileOperations": map[string]any{
							"didCreate": workspaceFileOperationRegistration(),
							"didRename": workspaceFileOperationRegistration(),
							"didDelete": workspaceFileOperationRegistration(),
						},
					},
				},
				"serverInfo": map[string]string{"name": "gox", "version": buildinfo.Version()},
			})
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "workspace/symbol":
			var params WorkspaceSymbolParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			result, err := json.Marshal(s.workspaceSymbols(params.Query))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/documentSymbol":
			var params DocumentSymbolParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.symbols.Document(params.TextDocument.URI)
			symbols := []DocumentSymbol{}
			if ok {
				symbols = s.documentSymbols(document)
			}
			result, err := json.Marshal(symbols)
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/codeAction":
			var params CodeActionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.codeActions(document, params))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "shutdown":
			shutdown = true
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage("null")}); err != nil {
				return err
			}
		case "textDocument/semanticTokens/full":
			var params SemanticTokensParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(semanticTokensForDocument(document))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/definition":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.definitionResult(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/typeDefinition":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.typeDefinitionLocations(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/implementation":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.implementationLocations(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/prepareCallHierarchy":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.prepareCallHierarchy(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "callHierarchy/incomingCalls":
			var params CallHierarchyIncomingCallsParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			result, err := json.Marshal(s.incomingCalls(params.Item))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "callHierarchy/outgoingCalls":
			var params CallHierarchyOutgoingCallsParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			result, err := json.Marshal(s.outgoingCalls(params.Item))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/prepareTypeHierarchy":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.prepareTypeHierarchy(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "typeHierarchy/supertypes":
			var params TypeHierarchySupertypesParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			result, err := json.Marshal(s.typeHierarchySupertypes(params.Item))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "typeHierarchy/subtypes":
			var params TypeHierarchySubtypesParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			result, err := json.Marshal(s.typeHierarchySubtypes(params.Item))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/references":
			var params ReferenceParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.references(document, params.Position, params.Context.IncludeDeclaration))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/documentHighlight":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.documentHighlights(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/prepareRename":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.prepareRename(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/linkedEditingRange":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(linkedEditingRanges(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/rename":
			var params RenameParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			edit, conflict := s.renameResult(document, params.Position, params.NewName)
			if conflict != "" {
				if err := conn.write(errorResponse(request.ID, -32803, conflict)); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(edit)
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/hover":
			var params TextDocumentPositionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.hover(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/inlayHint":
			var params InlayHintParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.importAliasInlayHints(document, params.Range))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/completion":
			var params CompletionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.completions(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/signatureHelp":
			var params SignatureHelpParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(s.signatureHelp(document, params.Position))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/onTypeFormatting":
			var params DocumentOnTypeFormattingParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(templateFragmentClosingEdits(document, params.Position, params.Ch))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/formatting":
			var params DocumentFormattingParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(documentFormattingEdits(document))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/rangeFormatting":
			var params DocumentRangeFormattingParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(documentRangeFormattingEdits(document, params.Range))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/foldingRange":
			var params FoldingRangeParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(foldingRanges(document))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		case "textDocument/selectionRange":
			var params SelectionRangeParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				if writeErr := conn.write(errorResponse(request.ID, -32602, "Invalid params")); writeErr != nil {
					return writeErr
				}
				continue
			}
			document, ok := s.documents.Get(params.TextDocument.URI)
			if !ok {
				if err := conn.write(errorResponse(request.ID, -32602, "Document is not open")); err != nil {
					return err
				}
				continue
			}
			result, err := json.Marshal(selectionRanges(document, params.Positions))
			if err != nil {
				return err
			}
			if err := conn.write(response{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
				return err
			}
		default:
			if err := conn.write(errorResponse(request.ID, -32601, "Method not found")); err != nil {
				return err
			}
		}
	}
}

func (s *Server) refreshOpenDocument(uri string) {
	if document, ok := s.documents.Get(uri); ok {
		snapshot := s.analysisSnapshot(document)
		if snapshot != nil && snapshot.Err == nil && snapshot.Module != nil {
			_ = s.symbols.UpdateOpenModule(document, snapshot.Module)
			s.syncGeneratedTypesModule(document, snapshot.Module)
		} else {
			_ = s.symbols.UpdateOpenModule(document, nil)
		}
	}
}

func (s *Server) handleNotification(conn *connection, notification message) error {
	switch notification.Method {
	case "textDocument/didOpen":
		var params DidOpenTextDocumentParams
		if json.Unmarshal(notification.Params, &params) == nil {
			if err := s.documents.Open(params.TextDocument); err == nil {
				s.refreshOpenDocument(params.TextDocument.URI)
				return s.publishAffectedDiagnostics(conn, params.TextDocument.URI)
			}
		}
	case "textDocument/didChange":
		var params DidChangeTextDocumentParams
		if json.Unmarshal(notification.Params, &params) == nil {
			if err := s.documents.Change(params.TextDocument, params.ContentChanges); err == nil {
				s.refreshOpenDocument(params.TextDocument.URI)
				return s.publishAffectedDiagnostics(conn, params.TextDocument.URI)
			}
		}
	case "textDocument/didClose":
		var params DidCloseTextDocumentParams
		if json.Unmarshal(notification.Params, &params) == nil {
			s.refreshDiskURI(params.TextDocument.URI)
			s.documents.Close(params.TextDocument.URI)
			s.symbols.CloseOpen(params.TextDocument.URI)
			if s.analysis != nil {
				s.analysis.Close(params.TextDocument.URI)
			}
			if err := s.writeDiagnostics(conn, PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: []Diagnostic{},
			}); err != nil {
				return err
			}
			return s.publishAffectedDiagnostics(conn, params.TextDocument.URI)
		}
	case "workspace/didChangeWatchedFiles":
		var params DidChangeWatchedFilesParams
		if json.Unmarshal(notification.Params, &params) == nil {
			for _, change := range params.Changes {
				goPackagesChanged := s.handleWorkspaceFileChange(change.URI, change.Type)
				if err := s.publishWorkspaceFileDiagnostics(conn, change.URI, goPackagesChanged); err != nil {
					return err
				}
			}
		}
	case "workspace/didCreateFiles":
		var params CreateFilesParams
		if json.Unmarshal(notification.Params, &params) == nil {
			for _, file := range params.Files {
				goPackagesChanged := s.handleWorkspaceFileChange(file.URI, fileCreated)
				if err := s.publishWorkspaceFileDiagnostics(conn, file.URI, goPackagesChanged); err != nil {
					return err
				}
			}
		}
	case "workspace/didDeleteFiles":
		var params DeleteFilesParams
		if json.Unmarshal(notification.Params, &params) == nil {
			for _, file := range params.Files {
				goPackagesChanged := s.handleWorkspaceFileChange(file.URI, fileDeleted)
				if err := s.publishWorkspaceFileDiagnostics(conn, file.URI, goPackagesChanged); err != nil {
					return err
				}
			}
		}
	case "workspace/didRenameFiles":
		var params RenameFilesParams
		if json.Unmarshal(notification.Params, &params) == nil {
			for _, file := range params.Files {
				goPackagesChanged := s.handleWorkspaceFileChange(file.OldURI, fileDeleted)
				goPackagesChanged = s.handleWorkspaceFileChange(file.NewURI, fileCreated) || goPackagesChanged
				if goPackagesChanged {
					if err := s.publishOpenDocumentDiagnostics(conn); err != nil {
						return err
					}
					continue
				}
				if err := s.publishAffectedDiagnostics(conn, file.OldURI); err != nil {
					return err
				}
				if err := s.publishAffectedDiagnostics(conn, file.NewURI); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func workspaceFileOperationRegistration() map[string]any {
	return map[string]any{
		"filters": []map[string]any{
			{"scheme": "file", "pattern": map[string]any{"glob": "**/*.gox", "matches": "file"}},
			{"scheme": "file", "pattern": map[string]any{"glob": "**/*.go", "matches": "file"}},
			{"scheme": "file", "pattern": map[string]any{"glob": "**/go.mod", "matches": "file"}},
			{"scheme": "file", "pattern": map[string]any{"glob": "**/go.sum", "matches": "file"}},
			{"scheme": "file", "pattern": map[string]any{"glob": "**/go.work", "matches": "file"}},
		},
	}
}

func (s *Server) handleWorkspaceFileChange(uri string, changeType int) bool {
	if s.invalidateGoPackageFile(uri) {
		return true
	}
	if changeType == fileDeleted {
		s.removeDiskURI(uri)
	} else if changeType == fileCreated || changeType == fileChanged {
		s.refreshDiskURI(uri)
	}
	return false
}

func (s *Server) invalidateGoPackageFile(uri string) bool {
	if !goDependencyFileURI(uri) {
		return false
	}
	path, err := FilePath(uri)
	if err != nil {
		return true
	}
	switch filepath.Base(path) {
	case "go.work":
		s.goPackages.Invalidate()
		if s.analysis != nil {
			s.analysis.InvalidateAll()
		}
	case "go.mod", "go.sum":
		root := filepath.Dir(path)
		s.goPackages.InvalidateRoot(root)
		if s.analysis != nil {
			s.analysis.InvalidateRoot(root)
		}
	default:
		root := s.projectRoot(path)
		if strings.EqualFold(filepath.Ext(path), ".go") {
			if invalidation, ok := s.goPackages.ProjectGoPackageInvalidationForFile(root, path); ok {
				s.goPackages.InvalidateProjectGoPackage(root, invalidation.ImportPath, invalidation.Affected, invalidation.Candidate)
				if s.analysis != nil {
					// Snapshots record their Go package dependencies independently
					// from the root-wide importer universe. Drop only snapshots that
					// intersect the affected local-package closure, while rotating the
					// importer generation for future analyses of this root.
					s.analysis.InvalidateGoPackages(root, invalidation.Affected)
				}
			} else {
				s.goPackages.InvalidateRoot(root)
				if s.analysis != nil {
					s.analysis.InvalidateRoot(root)
				}
			}
		} else {
			s.goPackages.InvalidateRoot(root)
			if s.analysis != nil {
				s.analysis.InvalidateRoot(root)
			}
		}
	}
	return true
}

func (s *Server) publishWorkspaceFileDiagnostics(conn *connection, uri string, goPackagesChanged bool) error {
	if goPackagesChanged {
		return s.publishGoPackageAffectedDiagnostics(conn, uri)
	}
	if root, ok := s.clientWorkspaceRootForURI(uri); ok {
		return s.publishProjectOpenDocumentDiagnostics(conn, root)
	}
	return s.publishAffectedDiagnostics(conn, uri)
}

func (s *Server) publishProjectOpenDocumentDiagnostics(conn *connection, root string) error {
	for _, document := range s.documents.Documents() {
		documentRoot := s.analysisProjectRoot(document.Path)
		if documentRoot == "" {
			documentRoot = s.projectRoot(document.Path)
		}
		if !samePath(documentRoot, root) {
			continue
		}
		if err := s.publishDiagnostics(conn, document.URI); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) publishGoPackageAffectedDiagnostics(conn *connection, uri string) error {
	path, err := FilePath(uri)
	if err != nil || filepath.Base(path) == "go.work" {
		return s.publishOpenDocumentDiagnostics(conn)
	}
	root := s.projectRoot(path)
	goMod := filepath.Base(path) == "go.mod"
	if goMod {
		root = filepath.Dir(path)
	}
	for _, document := range s.documents.Documents() {
		if goMod {
			if !pathInside(root, document.Path) && !samePath(root, filepath.Dir(document.Path)) {
				continue
			}
		} else {
			documentRoot := s.analysisProjectRoot(document.Path)
			if documentRoot == "" {
				documentRoot = s.projectRoot(document.Path)
			}
			if !samePath(documentRoot, root) {
				continue
			}
		}
		if err := s.publishDiagnostics(conn, document.URI); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) publishOpenDocumentDiagnostics(conn *connection) error {
	for _, document := range s.documents.Documents() {
		if err := s.publishDiagnostics(conn, document.URI); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) publishDiagnostics(conn *connection, uri string) error {
	document, ok := s.documents.Get(uri)
	if !ok {
		return nil
	}
	return s.writeDiagnostics(conn, PublishDiagnosticsParams{
		URI:         document.URI,
		Version:     document.Version,
		Diagnostics: s.diagnostics(document),
	})
}

func (s *Server) publishAffectedDiagnostics(conn *connection, changedURI string) error {
	var invalidatedURIs []string
	if changedPath, err := FilePath(changedURI); err == nil && strings.EqualFold(filepath.Ext(changedPath), ".gox") && s.analysis != nil {
		exceptURI := ""
		if _, open := s.documents.Get(changedURI); open {
			exceptURI = changedURI
		}
		invalidatedURIs = s.analysis.InvalidateDependencyPathExcept(changedPath, exceptURI)
	}

	affected := s.affectedOpenDocuments(changedURI)
	for _, uri := range invalidatedURIs {
		if document, ok := s.documents.Get(uri); ok {
			affected = append(affected, document)
		}
	}
	if document, ok := s.documents.Get(changedURI); ok {
		affected = append([]Document{document}, affected...)
	}
	seen := make(map[string]bool, len(affected))
	for _, document := range affected {
		if seen[document.URI] {
			continue
		}
		seen[document.URI] = true
		if err := s.writeDiagnostics(conn, PublishDiagnosticsParams{
			URI:         document.URI,
			Version:     document.Version,
			Diagnostics: s.diagnostics(document),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) affectedOpenDocuments(changedURI string) []Document {
	changedPath, err := FilePath(changedURI)
	if err != nil {
		return nil
	}
	changedPath = filepath.Clean(changedPath)
	documents := s.documents.Documents()
	affectedPaths := map[string]bool{changedPath: true}
	affectedURIs := make(map[string]bool)
	changed := true
	for changed {
		changed = false
		for _, document := range documents {
			if affectedURIs[document.URI] || filepath.Clean(document.Path) == changedPath {
				continue
			}
			root := s.projectRoot(document.Path)
			for _, declaration := range s.symbols.Imports(document.URI) {
				resolved, resolveErr := goxcompiler.ResolveGoxImport(root, document.Path, declaration.Path)
				if resolveErr != nil || !affectedPaths[filepath.Clean(resolved)] {
					continue
				}
				affectedURIs[document.URI] = true
				affectedPaths[filepath.Clean(document.Path)] = true
				changed = true
				break
			}
		}
	}
	result := make([]Document, 0, len(affectedURIs))
	for _, document := range documents {
		if affectedURIs[document.URI] {
			result = append(result, document)
		}
	}
	return result
}

func (s *Server) writeDiagnostics(conn *connection, params PublishDiagnosticsParams) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return conn.write(message{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params:  data,
	})
}

func errorResponse(id json.RawMessage, code int, message string) response {
	return response{JSONRPC: "2.0", ID: bytes.Clone(id), Error: &responseError{Code: code, Message: message}}
}
