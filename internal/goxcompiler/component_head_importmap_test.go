package gox

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestImportMapInSharedComponentHead(t *testing.T) {
	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	entry := filepath.Join(root, "pages", "index.gox")
	phase1WriteFile(t, entry, `module page
import "github.com/gox-lang/gox"
import shell "./shell.gox"
func GET(ctx *gox.Context) gox.Page {
 ctx.Response.ImportMap.Set("extra", "/extra.js")
 return <&shell.Page />
}
`)
	phase1WriteFile(t, filepath.Join(root, "pages", "shell.gox"), `module shell
import "github.com/gox-lang/gox"
func Page() gox.Component { return <html lang="es"><&Head /><body>ok</body></html> }
func Head() gox.Component { return <head><script type="module" src="/dashboard.js"></script></head> }
func SignOut(ctx *gox.Context) gox.Action { return nil }
`)
	output := filepath.Join(root, ".build", "generated", "generated.go")
	phase1WriteFile(t, output, "")
	if _, err := Compile(entry, output); err != nil {
		t.Fatal(err)
	}
	phase1WriteFile(t, filepath.Join(filepath.Dir(output), "render_test.go"), `package main
import("bytes";"encoding/json";"strings";"testing";"github.com/gox-lang/gox")
type writer struct { gox.Writer; body bytes.Buffer; response *gox.Response; head func(); started bool }
func(w *writer)Write(p []byte)(int,error){ w.started=true;return w.body.Write(p) }
func(w *writer)WriteRawBytes(p []byte){w.Write(p)}
func(w *writer)WriteRawString(s string){w.Write([]byte(s))}
func(w *writer)WriteRawAny(v any){p,_:=json.Marshal(v);w.Write(p)}
func(w *writer)GoxPreparePageHead(a []byte,e [][2]string,s []byte,h string){
 if w.started {panic("metadata prepared after output")}
 w.head=func(){w.Write(a);w.response.ImportMap.Runtime().WriteCompiled(w,e,s,h)}
}
func(w *writer)GoxWritePageHead(){if w.head!=nil{f:=w.head;w.head=nil;f()}}
func TestRender(t *testing.T){
 w:=&writer{response:gox.NewResponse()}
 gox.SetRuntimeResponseMutationGuard(func(*gox.Response)bool{return !w.started})
 w.response.Headers.Set("Content-Type","text/html; charset=utf-8")
 GoxHTTPHandlerGET(&gox.Context{Writer:w,Response:w.response})
 body:=w.body.String()
 csp,_:=w.response.Headers.Get("Content-Security-Policy")
 if strings.Count(body,"type=\"importmap\"")!=1||!strings.Contains(body,"@gox/actions")||!strings.Contains(body,"SignOut")||!strings.Contains(body,"/extra.js"){t.Fatalf("missing metadata: %s",body)}
 if strings.Index(body,"type=\"importmap\"")>strings.Index(body,"src=\"/dashboard.js\""){t.Fatal("import map follows module")}
 if !strings.Contains(csp,"nonce-"){t.Fatalf("CSP: %s",csp)}
}
`)
	cmd := exec.Command("go", "test", "-mod=readonly", "-count=1", ".")
	cmd.Dir = filepath.Dir(output)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("component head: %v\n%s", err, out)
	}
}
