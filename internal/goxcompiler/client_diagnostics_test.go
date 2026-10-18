package gox

import (
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestClientDiagnosticKindsRemainStable(t *testing.T) {
	got := []string{
		diagnosticInvalidClientName,
		diagnosticInvalidClientFrameworkCase,
		diagnosticInvalidClientFramework,
		diagnosticClientFrameworkNotSupportedYet,
		diagnosticInvalidClientDeclaration,
		diagnosticUnclosedClientDeclaration,
		diagnosticInvalidClientProperty,
		diagnosticDuplicateClientProperty,
		diagnosticInvalidClientSrc,
		diagnosticInvalidClientDev,
		diagnosticUnknownClientProperty,
		diagnosticMissingClientSrc,
		diagnosticDuplicateClientComponent,
		diagnosticClientNameConflict,
		diagnosticInvalidClientEntryPath,
		diagnosticMissingClientEntry,
		diagnosticClientSourceAccessError,
		diagnosticInvalidClientSourceExtension,
	}
	want := []string{
		"invalid-client-name",
		"invalid-client-framework-case",
		"invalid-client-framework",
		"client-framework-not-supported-yet",
		"invalid-client-declaration",
		"unclosed-client-declaration",
		"invalid-client-property",
		"duplicate-client-property",
		"invalid-client-src",
		"invalid-client-dev",
		"unknown-client-property",
		"missing-client-src",
		"duplicate-client-component",
		"client-name-conflict",
		"invalid-client-entry-path",
		"missing-client-entry",
		"client-source-access-error",
		"invalid-client-source-extension",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("client diagnostic %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestClientDiagnosticsExposeCompilerContract(t *testing.T) {
	_, err := ParseModule("pages/index.gox", `module main

client Menu react {
	src: ""
}
`)
	compileErr := assertClientSemanticError(t, err, diagnosticInvalidClientSrc)
	if compileErr.Type != goxerrors.TypeCompiler {
		t.Fatalf("Type = %q, want %q", compileErr.Type, goxerrors.TypeCompiler)
	}
	if compileErr.Stage != "compile" {
		t.Fatalf("Stage = %q, want compile", compileErr.Stage)
	}
	if compileErr.File != "pages/index.gox" || compileErr.Line == 0 || compileErr.Column == 0 {
		t.Fatalf("invalid source location: %+v", compileErr)
	}
	if compileErr.Hint == "" {
		t.Fatalf("diagnostic has no corrective hint: %+v", compileErr)
	}
}
