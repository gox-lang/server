package gox

import (
	"go/ast"
	"strings"
	"testing"
)

func TestParseModulePropagatesWebSocketRewriteOrigins(t *testing.T) {
	src := `module websocket

import "github.com/gox-lang/gox"

func Index(socket *gox.Socket) gox.WebSocket {
	var reply string
	socket.On("profile", func(name string) {})
	socket.Emit("update", name).Await(&reply)
}
`
	mod, err := ParseModule("websocket/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	var onRegistered *ast.SelectorExpr
	var emitAwait *ast.SelectorExpr
	var generatedName *ast.BasicLit
	var updateEvent *ast.BasicLit
	ast.Inspect(mod.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			if value.Sel == nil {
				return true
			}
			switch value.Sel.Name {
			case "OnRegistered":
				onRegistered = value
			case "EmitAwait":
				emitAwait = value
			}
		case *ast.BasicLit:
			switch value.Value {
			case `"name"`:
				generatedName = value
			case `"update"`:
				updateEvent = value
			}
		}
		return true
	})
	if onRegistered == nil || emitAwait == nil || generatedName == nil || updateEvent == nil {
		t.Fatalf("rewritten WebSocket nodes were not found: on=%v emitAwait=%v name=%v update=%v", onRegistered != nil, emitAwait != nil, generatedName != nil, updateEvent != nil)
	}

	assertAnchor := func(label string, node ast.Node, offset int) {
		t.Helper()
		origin, ok := mod.Origins.Get(node)
		if !ok || origin.Exact() || origin.Source.URI != "websocket/index.gox" || origin.Source.Range.Start != offset {
			t.Fatalf("%s origin = %#v, %v", label, origin, ok)
		}
	}
	onOffset := strings.Index(src, ".On(") + 1
	assertAnchor("OnRegistered selector", onRegistered, onOffset)
	assertAnchor("OnRegistered name", onRegistered.Sel, onOffset)
	assertAnchor("registered parameter name", generatedName, strings.Index(src, "name string"))

	awaitOffset := strings.Index(src, ".Await(") + 1
	assertAnchor("EmitAwait selector", emitAwait, awaitOffset)
	assertAnchor("EmitAwait name", emitAwait.Sel, awaitOffset)

	updateOrigin, ok := mod.Origins.Get(updateEvent)
	updateOffset := strings.Index(src, `"update"`)
	if !ok || !updateOrigin.Exact() || updateOrigin.Source.URI != "websocket/index.gox" || updateOrigin.Source.Range.Start != updateOffset {
		t.Fatalf("preserved Emit event origin = %#v, %v", updateOrigin, ok)
	}
}
