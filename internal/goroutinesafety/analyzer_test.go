package goroutinesafety

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func analyzeSource(t *testing.T, body string) []Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "worker.go", "package sample\n"+body, 0)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	return AnalyzeFiles(fset, []*ast.File{file})
}

func TestAnonymousGoroutineRecoveryContract(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantUnsafe int
	}{
		{
			name: "direct deferred recover is ineffective",
			source: `func start() {
	go func() {
		defer recover()
		work()
	}()
}
func work() {}`,
			wantUnsafe: 1,
		},
		{
			name: "deferred closure calling recover is safe",
			source: `func start() {
	go func() {
		defer func() { recover() }()
		work()
	}()
}
func work() {}`,
		},
		{
			name: "deferred closure may inspect recovered value",
			source: `func start() {
	go func() {
		defer func() {
			if err := recover(); err != nil { _ = err }
		}()
		work()
	}()
}
func work() {}`,
		},
		{
			name: "missing recovery is unsafe",
			source: `func start() {
	go func() { work() }()
}
func work() {}`,
			wantUnsafe: 1,
		},
		{
			name: "non deferred recover is unsafe",
			source: `func start() {
	go func() {
		recover()
		work()
	}()
}
func work() {}`,
			wantUnsafe: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeSource(t, tt.source)
			if len(got) != tt.wantUnsafe {
				t.Fatalf("unsafe goroutines = %d, want %d: %#v", len(got), tt.wantUnsafe, got)
			}
		})
	}
}

func TestFirstRegisteredDeferMustProvideRecovery(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantUnsafe int
	}{
		{
			name: "recovery registered first is final LIFO barrier",
			source: `func start() {
	go func() {
		defer func() { recover() }()
		defer cleanup()
		work()
	}()
}
func cleanup() {}
func work() {}`,
		},
		{
			name: "recovery registered after another defer is unsafe",
			source: `func start() {
	go func() {
		defer cleanup()
		defer recover()
		work()
	}()
}
func cleanup() {}
func work() {}`,
			wantUnsafe: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeSource(t, tt.source)
			if len(got) != tt.wantUnsafe {
				t.Fatalf("unsafe goroutines = %d, want %d: %#v", len(got), tt.wantUnsafe, got)
			}
		})
	}
}

func TestNamedGoroutineFunctionRecoveryContract(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantUnsafe int
	}{
		{
			name: "protected named function is safe",
			source: `func worker() {
	defer func() { recover() }()
	work()
}
func start() { go worker() }
func work() {}`,
		},
		{
			name: "unprotected named function is unsafe",
			source: `func worker() { work() }
func start() { go worker() }
func work() {}`,
			wantUnsafe: 1,
		},
		{
			name: "verified recovery helper is safe",
			source: `func recoverPanic() { recover() }
func worker() {
	defer recoverPanic()
	work()
}
func start() { go worker() }
func work() {}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeSource(t, tt.source)
			if len(got) != tt.wantUnsafe {
				t.Fatalf("unsafe goroutines = %d, want %d: %#v", len(got), tt.wantUnsafe, got)
			}
		})
	}
}

func TestNestedGoroutinesAreValidatedIndependently(t *testing.T) {
	source := `func start() {
	go func() {
		defer func() { recover() }()
		go func() { work() }()
	}()
}
func work() {}`

	got := analyzeSource(t, source)
	if len(got) != 1 {
		t.Fatalf("unsafe goroutines = %d, want 1: %#v", len(got), got)
	}
}

func TestRecoveryHelperMustActuallyCallRecover(t *testing.T) {
	source := `func fakeRecovery() {}
func start() {
	go func() {
		defer fakeRecovery()
		work()
	}()
}
func work() {}`

	got := analyzeSource(t, source)
	if len(got) != 1 {
		t.Fatalf("unsafe goroutines = %d, want 1: %#v", len(got), got)
	}
}

func TestRecoveryBarrierMustBeRegisteredBeforeExecutableWork(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "work before recovery defer",
			source: `func start() {
	go func() {
		work()
		defer recover()
	}()
}
func work() {}`,
		},
		{
			name: "conditional recovery defer",
			source: `func start() {
	go func() {
		if enabled() { defer recover() }
		work()
	}()
}
func enabled() bool { return false }
func work() {}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeSource(t, tt.source)
			if len(got) != 1 {
				t.Fatalf("unsafe goroutines = %d, want 1: %#v", len(got), got)
			}
		})
	}
}

func TestDeferredRecoveryFunctionMustRecoverBeforeOtherWork(t *testing.T) {
	source := `func start() {
	go func() {
		defer func() {
			cleanup()
			recover()
		}()
		work()
	}()
}
func cleanup() {}
func work() {}`

	got := analyzeSource(t, source)
	if len(got) != 1 {
		t.Fatalf("unsafe goroutines = %d, want 1: %#v", len(got), got)
	}
}
