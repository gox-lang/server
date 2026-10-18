package clientbuilder

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVisibleHydrationObservesRenderedBoxes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required")
	}
	source := `import assert from 'node:assert/strict';
const ios=[],mos=[];
class Element {
 constructor(display='contents'){this.display=display;this.children=[];this.parent=null}
 prepend(child){this.children.unshift(child);child.parent=this}
 remove(){if(this.parent)this.parent.children=this.parent.children.filter(x=>x!==this);this.parent=null}
 setAttribute(){}
}
globalThis.HTMLElement=Element;
globalThis.customElements={get:()=>true};
globalThis.getComputedStyle=element=>({display:element.display});
globalThis.document={createElement:()=>new Element('inline-block')};
globalThis.IntersectionObserver=class{
 constructor(callback){this.callback=callback;this.targets=[];ios.push(this)}
 observe(target){this.targets.push(target)}
 disconnect(){this.targets=[]}
};
globalThis.MutationObserver=class{
 constructor(callback){this.callback=callback;this.disconnected=false;mos.push(this)}
 observe(){}
 disconnect(){this.disconnected=true}
};
` + goxIsleRuntimeSource + `
// Observe a real descendant, including through display:contents wrappers.
const isle=new Element(),container=new Element(),child=new Element('block');
isle.prepend(container);container.prepend(child);
let starts=0;const cancel=goxObserveVisible(isle,()=>starts++);
const io=ios.at(-1);assert.deepEqual(io.targets,[child]);
io.callback([{isIntersecting:false}]);assert.equal(starts,0);
io.callback([{isIntersecting:true}]);assert.equal(starts,1);
assert.equal(io.targets.length,0);assert.equal(mos.at(-1).disconnected,true);
io.callback([{isIntersecting:true}]);assert.equal(starts,1);cancel();
// Empty/text-only islands use a disposable observation marker.
const empty=new Element();const stop=goxObserveVisible(empty,()=>starts++);
const marker=empty.children[0];assert.ok(marker);
assert.equal(marker.className,'gox-isle-visible-marker');
assert.deepEqual(ios.at(-1).targets,[marker]);
// Later SSRT replaces the marker as an observation target.
const content=new Element('block');empty.prepend(content);mos.at(-1).callback();
assert.deepEqual(ios.at(-1).targets,[content]);assert.equal(marker.parent,null);
stop();ios.at(-1).callback([{isIntersecting:true}]);assert.equal(starts,1);
// Cancellation removes the placeholder even if it has not intersected yet.
const removed=new Element();const stopRemoved=goxObserveVisible(removed,()=>starts++);
stopRemoved();assert.equal(removed.children.length,0);
// A host with its own box can be observed directly.
const boxed=new Element('block');const stopBoxed=goxObserveVisible(boxed,()=>starts++);
assert.deepEqual(ios.at(-1).targets,[boxed]);stopBoxed();
`
	path := filepath.Join(t.TempDir(), "visible.mjs")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, path).CombinedOutput(); err != nil {
		t.Fatalf("visible hydration: %v\n%s", err, out)
	}
}
