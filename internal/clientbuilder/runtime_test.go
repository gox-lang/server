package clientbuilder

import (
	"strings"
	"testing"
)

func TestGoxIsleRuntimeHydrateModesV1(t *testing.T) {
	src := goxIsleRuntimeSource
	for _, want := range []string{
		`mode==="load"`,
		`mode==="idle"`,
		`mode==="visible"`,
		`mode==="hover"`,
		`mode==="interaction"`,
		`requestIdleCallback`,
		`IntersectionObserver`,
		`pointerenter`,
		`pointerdown`,
		`keydown`,
		`touchstart`,
		`focusin`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("runtime missing %q", want)
		}
	}
	if strings.Contains(src, `mode==="focus"`) {
		t.Fatal("runtime must not expose focus as a hydrate mode")
	}
}

func TestGoxIsleRuntimeUsesDataPropsAndMountContract(t *testing.T) {
	src := goxIsleRuntimeSource
	for _, want := range []string{
		`this.getAttribute("data-props")`,
		`mod.mount(this,props)`,
		`mod.mount(shadow,props)`,
		`mod.mount(tempTarget,props)`,
		`gox isle module does not export mount()`,
		`gox isle missing data-script`,
		`invalid gox isle `,
		`props`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("runtime missing %q", want)
		}
	}
}

func TestGoxIsleRuntimeTransitionAndIsolateContract(t *testing.T) {
	src := goxIsleRuntimeSource
	for _, want := range []string{
		`this.getAttribute("data-transition")`,
		`transition+"-in"`,
		`transition+"-out"`,
		`goxWaitAnimation(clientHost)`,
		`stage.className="gox-isle-stage"`,
		`ssrtHost.className="gox-isle-ssrt"`,
		`clientHost.className="gox-isle-client"`,
		`while(this.firstChild) ssrtHost.appendChild(this.firstChild)`,
		`this.replaceChildren()`,
		`data-assets`,
		`adoptedStyleSheets`,
		`goxRequireArray`,
		`const prepareShadow=async()=>`,
		`const mountFinal=async()=>`,
		`this.__goxCleanup=cleanup`,
		`stage.className="gox-isle-mounted"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("runtime missing %q", want)
		}
	}
	if strings.Contains(src, `this.style.`) {
		t.Fatalf("runtime must not overwrite author styles on the island")
	}
}

func TestGoxIsleBaseStyleContract(t *testing.T) {
	src := goxIsleBaseStyleSource
	for _, want := range []string{
		`gox-isle{display:contents}`,
		`gox-isle>*{content-visibility:auto}`,
		`.gox-isle-stage{display:grid;grid-template-areas:"gox-isle-stack"`,
		`.gox-isle-ssrt,.gox-isle-client{grid-area:gox-isle-stack`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("base style missing %q:\n%s", want, src)
		}
	}
}

func TestGoxActionsRuntimeUsesMetadataAndFormSupport(t *testing.T) {
	src := goxActionsRuntimeSource
	for _, want := range []string{
		`import {htmlToNode,normalizeClientValue} from "/_gox/client/values.js"`,
		`document.getElementById("_GOX-ACTIONS")`,
		`Object.keys(readActionSignatures())`,
		`argsFromForm(action,args[0],signatures)`,
		`new FormData(form)`,
		`elements.namedItem`,
		`field.type==="file"&&field.multiple`,
		`fd.getAll(name).filter(validFormFile)`,
		`args.map(value=>normalizeClientValue(value).value)`,
		`contentType.includes("text/html")`,
		`htmlToNode(await response.text())`,
		`export default Actions`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("actions runtime missing %q", want)
		}
	}
	if strings.Contains(src, "data-gox-actions") {
		t.Fatal("actions runtime must not depend on data-gox-actions")
	}
	if strings.Contains(src, "globalThis.Actions") {
		t.Fatal("actions runtime must not expose global Actions")
	}
}

func TestGoxActionsRuntimeReadsFileResponses(t *testing.T) {
	src := goxActionsRuntimeSource
	for _, want := range []string{
		`case "file": return await readActionFile(response)`,
		`response.headers.get("Content-Disposition")`,
		`filename\*=`,
		`decodeURIComponent`,
		`await response.blob()`,
		`new File([blob],name,{type:contentType})`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("actions runtime file response support missing %q", want)
		}
	}
}

func TestGoxActionsRuntimeReadsFilesResponses(t *testing.T) {
	src := goxActionsRuntimeSource
	for _, want := range []string{
		`case "files": return await readActionFiles(response)`,
		`await response.formData()`,
		`form.getAll("files")`,
		`value instanceof File`,
		`Invalid Action files response part`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("actions runtime files response support missing %q", want)
		}
	}
}
