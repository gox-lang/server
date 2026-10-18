package gox

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportMapCSPPrecedesDynamicTemplateWrites(t *testing.T) {
	for _, markup := range []string{
		`<html lang={{ lang }}><head><title>test</title></head><body /></html>`,
		`<><p>{{ lang }}</p><html><head data-lang={{ lang }}></head><body /></html></>`,
		`<html><head></head><body /></html>`,
		`<html lang={{ lang }}><:if condition={{ true }}><head></head></:if></html>`,
	} {
		src := "module page\nimport gox \"github.com/gox-lang/gox\"\nfunc GET(ctx *gox.Context) gox.Page {\nlang:=\"es\"\n_ = lang\nreturn " + markup + "\n}\n"
		mod, err := ParseModule("pages/index.gox", src)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
		if err != nil {
			t.Fatal(err)
		}
		out, mapping, err := FormatBundleMapped(bundle, ".build/pages/index/generated.go")
		if err != nil {
			t.Fatal(err)
		}
		csp := strings.Index(out, "ctx.Response.ImportMap.Runtime().ApplyCSP")
		firstWrite := strings.Index(out, "ctx.Writer.Write")
		if csp < 0 || firstWrite < csp || strings.Count(out, ".ApplyCSP(") != 1 {
			t.Fatalf("CSP not prepared before template output:\n%s", out)
		}
		origin, ok := mapping.OriginalPosition(csp)
		head := strings.Index(src, "<head") + 1
		if !ok || origin.URI != "pages/index.gox" || origin.Offset != head {
			t.Fatalf("CSP origin=%#v %v want head at %d", origin, ok, head)
		}
	}
}

func TestGeneratedImportMapWithDynamicHTMLAndResponseMutationGuard(t *testing.T) {
	source := `module page
import gox "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
 lang := "es"
 return <html lang={{ lang }}><head data-lang={{ lang }}><title>CSP</title></head><body>ok</body></html>
}
`
	mod, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	AddActionsClientScriptDecl(bundle, "", map[string]string{"react": "/react.js"})
	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	if err := WriteBundle(bundle, filepath.Join(root, "generated.go")); err != nil {
		t.Fatal(err)
	}
	phase1WriteFile(t, filepath.Join(root, "render_test.go"), `package main
import("bytes";"html";"encoding/json";"strings";"testing";"github.com/gox-lang/gox")
type guardWriter struct { gox.Writer; body bytes.Buffer; started bool }
func(w *guardWriter)Write(p []byte)(int,error){ w.started=true;return w.body.Write(p) }
func(w *guardWriter)WriteRawBytes(p []byte){_,_=w.Write(p)}
func(w *guardWriter)WriteRawAny(v any){p,err:=json.Marshal(v);if err!=nil{panic(err)};w.WriteRawBytes(p)}
func(w *guardWriter)WriteRawString(s string){_,_=w.Write([]byte(s))}
func(w *guardWriter)WriteTextString(s string){w.WriteRawString(html.EscapeString(s))}
func TestRequest(t *testing.T){
 for _,dynamic:=range []bool{false,true}{
  w:=&guardWriter{};response:=gox.NewResponse()
  gox.SetRuntimeResponseMutationGuard(func(*gox.Response)bool{return !w.started})
  response.Headers.Set("Content-Type","text/html; charset=utf-8")
  if dynamic{response.ImportMap.Set("extra","/extra.js")}
  ctx:=&gox.Context{Writer:w,Response:response}
  GoxHTTPHandlerGET(ctx)
  csp,_:=response.Headers.Get("Content-Security-Policy")
  body:=w.body.String()
  if !strings.Contains(body,"<html lang=\"es\"><head data-lang=\"es\">")||!strings.Contains(body,"type=\"importmap\""){t.Fatalf("unexpected body: %s",body)}
  if !dynamic&&!strings.Contains(csp,goxStaticImportMapHash){t.Fatalf("static CSP: %s",csp)}
  if dynamic&&(!strings.Contains(csp,"nonce-")||!strings.Contains(body,"/extra.js")){t.Fatalf("dynamic CSP/body: %s %s",csp,body)}
 }
}
`)
	cmd := exec.Command("go", "test", "-mod=readonly", "-count=1", ".")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated request: %v\n%s", err, out)
	}
}
