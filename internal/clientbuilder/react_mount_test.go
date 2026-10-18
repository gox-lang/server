package clientbuilder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The host removes SSRT before mount and needs committed content on return.
// Model a deferred React scheduler so this contract cannot pass by timing luck.
func TestReactMountCommitsBeforeReturning(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the mount contract test")
	}
	wrapper := reactFrameworkWrapperSource(Asset{AbsPath: "/component.jsx"})
	lines := strings.Split(wrapper, "\n")
	var body []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "import ") {
			body = append(body, line)
		}
	}
	source := `import assert from 'node:assert/strict';
let pending=[],renders=0,unmounts=0;
const React={createElement:(component,props)=>({component,props})};
const Component=props=>'Hola '+props.name;
function createRoot(target){return {
 render(element){pending.push(()=>{target.text=element.component(element.props);renders++})},
 unmount(){target.text='';unmounts++}
}}
function flushSync(callback){callback();for(const commit of pending.splice(0))commit()}
` + strings.Join(body, "\n") + `
// Independent islands must each commit their initial mount before returning.
for(let i=0;i<2;i++){
 const target={text:''};const cleanup=mount(target,{name:'Mundo'});
 assert.equal(target.text,'Hola Mundo','mount returned before committing the DOM');
 assert.equal(pending.length,0);assert.equal(typeof cleanup,'function');
 cleanup();assert.equal(target.text,'');
}
assert.equal(renders,2);assert.equal(unmounts,2);
`
	path := filepath.Join(t.TempDir(), "mount.mjs")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, path).CombinedOutput(); err != nil {
		t.Fatalf("React mount contract: %v\n%s", err, out)
	}
}

func TestReactDOMFacadeExposesFlushSync(t *testing.T) {
	found := false
	for _, facade := range frameworkModuleFacades("react") {
		for _, specifier := range facade.Specifiers {
			if specifier == "react-dom" {
				found = true
				if facade.Name != "vendor/react-dom" {
					t.Fatalf("unexpected facade %s", facade.Name)
				}
			}
		}
	}
	if !found {
		t.Fatal("react-dom missing from import map facades")
	}
	source := frameworkModuleFacadeSource("/react", "react", "vendor/react-dom")
	if !strings.Contains(source, "export const flushSync = ReactDOM.flushSync") {
		t.Fatal("flushSync missing from shared ReactDOM facade")
	}
}
