package clientbuilder

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEmptyIslandExpansionLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required")
	}
	source := `import assert from 'node:assert/strict';
let reduce=false,mounts=0,unmounts=0;
const appliedClasses=[];
const mountedTargets=[];
let sizes=null;const resizes=[];let finishAll=false;
globalThis.requestAnimationFrame=callback=>queueMicrotask(callback);
globalThis.window={matchMedia:()=>({matches:reduce})};
globalThis.mountFixture=target=>{mounts++;mountedTargets.push(target);target.childNodes=[{nodeType:1,state:{count:1}}];return ()=>unmounts++};
const url='data:text/javascript,export const mount=globalThis.mountFixture';
globalThis.HTMLElement=class{
 constructor(){this.style={};this.nodeType=1;this.childNodes=[];this.attributes={};this.classes=new Set();this.classList={add:x=>{this.classes.add(x);appliedClasses.push(x)},remove:x=>this.classes.delete(x)};this.scrollHeight=120;this.animations=[]}
 remove(){if(this.parent)this.parent.childNodes=this.parent.childNodes.filter(x=>x!==this)}
 removeAttribute(name){if(name==='style')this.style={}}
 getBoundingClientRect(){return {width:120,height:120}}
 get firstChild(){return this.childNodes[0]}
 appendChild(child){if(child.parent)child.parent.childNodes=child.parent.childNodes.filter(x=>x!==child);this.childNodes.push(child);child.parent=this}
 getAttribute(name){return name==='data-script'?url:(this.attributes[name]??null)}
 replaceChildren(){this.childNodes=[]}
 animate(frames,options){this.frames=frames;this.options=options;let finish;const animation={playState:'running',finished:new Promise(r=>finish=r),cancel(){this.playState='idle';finish()},finish(){this.playState='finished';finish()}};this.animations=[animation];this.onAnimation?.(animation);if(this.className==='gox-isle-stage')resizes.push(frames);if(finishAll||this.className==='gox-isle-stage')queueMicrotask(()=>animation.finish());return animation}
 getAnimations(){return this.animations}
};
globalThis.document={createRange:()=>({selectNodeContents(host){this.host=host},getBoundingClientRect(){return sizes?(this.host.className==='gox-isle-ssrt'?sizes[0]:sizes[1]):{width:120,height:120}}}),createElement:()=>new HTMLElement()};
globalThis.customElements={get:()=>false,define:(name,type)=>globalThis.Isle=type};
` + goxIsleRuntimeSource + `
async function expand(disconnect=false){
 const el=new Isle();el.__goxConnected=true;
 let ready;const animationReady=new Promise(r=>ready=r);el.onAnimation=ready;
 const mounting=el.__goxStartOnce();const animation=await animationReady;
 assert.deepEqual(el.frames,[{height:'0px'},{height:'120px'}]);
 assert.equal(el.options.duration,220);assert.equal(el.classes.has('gox-isle-expand'),true);
 if(disconnect)el.disconnectedCallback();else animation.finish();
 await mounting;assert.equal(el.classes.has('gox-isle-expand'),false);
 if(!disconnect){assert.equal(el.__goxMounted,true);el.disconnectedCallback()}
}
await expand();assert.equal(mounts,1);assert.equal(unmounts,1);
await expand(true);assert.equal(mounts,2);assert.equal(unmounts,2);
// Existing SSRT is replaced normally, without an expansion animation.
const ssrt=new Isle();ssrt.__goxConnected=true;ssrt.appendChild(new HTMLElement());await ssrt.__goxStartOnce();assert.equal(ssrt.animations.length,0);ssrt.disconnectedCallback();
// Automatic expansion does not depend on browser motion emulation.
reduce=true;await expand();
assert.equal(mounts,4);assert.equal(unmounts,4);
// Reduced motion must not bypass a transition explicitly supplied by the author.
for(const hasSSRT of [false,true]){
 appliedClasses.length=0;
 const el=new Isle();el.__goxConnected=true;el.attributes['data-transition']='fade';
 if(hasSSRT)el.appendChild(new HTMLElement());
 const beforeMounts=mounts,beforeUnmounts=unmounts;
 // Finish the simulated height animation while preserving the fade classes.
 el.onAnimation=animation=>queueMicrotask(()=>animation.finish());
 await el.__goxStartOnce();
 assert.equal(mounts,beforeMounts+1,'one mount per activation');
 assert.equal(unmounts,beforeUnmounts,'no cleanup at animation completion');
 const target=mountedTargets.at(-1);
 assert.equal(target.childNodes[0].state.count,1);
 if(hasSSRT){
  assert.equal(el.firstChild.firstChild,target,'framework root remains in place');
  assert.equal(target.className,'gox-isle-mounted');
 }
 assert.ok(appliedClasses.includes('fade-in'));
 assert.equal(appliedClasses.includes('fade-out'),hasSSRT);
 assert.equal(el.animations.length,hasSSRT?0:1);
 el.disconnectedCallback();
 assert.equal(unmounts,beforeUnmounts+1);
}
assert.equal(mounts,6);assert.equal(unmounts,6);
for(const pair of [[{width:100,height:140},{width:260,height:220}],[{width:260,height:220},{width:100,height:140}]]){
 sizes=pair;
 const el=new Isle();el.__goxConnected=true;el.appendChild(new HTMLElement());
 await el.__goxStartOnce();
 assert.deepEqual(resizes.at(-1),pair.map(size=>({width:size.width+'px',height:size.height+'px'})));
 assert.equal(el.__goxMounted,true);
 assert.equal(el.classes.size,0);
 el.disconnectedCallback();
}

// Exercise every explicit mode through the real lifecycle, including contraction
// and empty islands. This checks mount identity and cleanup as well as geometry.
finishAll=true;
const modes={top:[false,true,0,0],bottom:[false,true,0,1],left:[true,false,0,0],right:[true,false,1,0],
 'vertical-center':[false,true,0,.5],'horizontal-center':[true,false,.5,0],
 'top-left':[true,true,0,0],'top-right':[true,true,1,0],'bottom-left':[true,true,0,1],'bottom-right':[true,true,1,1],center:[true,true,.5,.5],none:[false,false,0,0]};
for(const [name,[wx,hy,x,y]] of Object.entries(modes)){
 for(const initial of [{width:100,height:140},{width:400,height:320},{width:0,height:0}]){
  const target={width:260,height:220};sizes=[initial,target];
  const el=new Isle();el.__goxConnected=true;el.attributes['data-adapt']=name;el.attributes['data-transition']='fade';
  if(initial.width)el.appendChild(new HTMLElement());
  const before=mounts,cleanups=unmounts,resizeCount=resizes.length;
  appliedClasses.length=0;
  await el.__goxStartOnce();
  assert.equal(mounts,before+1,name);assert.equal(unmounts,cleanups,name);
  assert.equal(el.firstChild.firstChild,mountedTargets.at(-1),name+' preserves root');
  assert.ok(appliedClasses.includes('fade-in'),name+' preserves transition');
  assert.equal(appliedClasses.includes('fade-out'),initial.width>0);
  if(name==='none')assert.equal(resizes.length,resizeCount);
  else{
   const frames=resizes.at(-1);
   assert.equal(frames[0].width,(wx?initial.width:target.width)+'px',name+' immediate width');
   assert.equal(frames[0].height,(hy?initial.height:target.height)+'px',name+' immediate height');
   assert.equal(frames[1].width,target.width+'px');assert.equal(frames[1].height,target.height+'px');
   if(x||y)assert.equal(frames[0].translate,((target.width-(wx?initial.width:target.width))*x)+'px '+((target.height-(hy?initial.height:target.height))*y)+'px',name+' anchor');
  }
  el.disconnectedCallback();assert.equal(unmounts,cleanups+1,name);
 }
}
for(const value of ['Top','top-center','vertical-bottom','','constructor'])assert.throws(()=>goxAdaptMode(value,true));
assert.deepEqual(goxAdaptMode(null,true),modes['top-left']);assert.deepEqual(goxAdaptMode(null,false),modes.top);



`
	path := filepath.Join(t.TempDir(), "expand.mjs")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, path).CombinedOutput(); err != nil {
		t.Fatalf("island expansion: %v\n%s", err, out)
	}
}
