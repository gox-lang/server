package clientbuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

const goxIsleRuntimeSource = `const sheetCache=new Map();
async function goxLoadSheet(url){
  if(sheetCache.has(url)) return sheetCache.get(url);
  const response=await fetch(url);
  if(!response.ok) throw new Error("gox isle failed to load css: "+url);
  const css=await response.text();
  const sheet=new CSSStyleSheet();
  sheet.replaceSync(css);
  sheetCache.set(url,sheet);
  return sheet;
}
function goxParseJSON(value,fallback,label){
  if(value===null||value===undefined||value==="") return fallback;
  try{return JSON.parse(value)}catch(e){throw new Error("invalid gox isle "+label+": "+e.message)}
}
function goxIsTrueAttr(el,name){
  const value=el.getAttribute(name);
  if(value===null) return false;
  if(value===""||value==="true") return true;
  if(value==="false") return false;
  throw new Error("invalid gox isle "+name.replace(/^data-/,"")+": "+value);
}
function goxWaitAnimation(el){
  const animations=el.getAnimations?el.getAnimations().filter(a=>a.playState!=="finished"):[];
  if(animations.length===0) return Promise.resolve();
  return Promise.all(animations.map(a=>a.finished.catch(()=>{})));
}
function goxNextFrame(){
  return new Promise(resolve=>requestAnimationFrame(()=>resolve()));
}
// Axis flags and normalized anchor within the final layout rectangle.
function goxAdaptMode(value,hasSSRT){
  const modes={
    top:[false,true,0,0],bottom:[false,true,0,1],
    left:[true,false,0,0],right:[true,false,1,0],
    "vertical-center":[false,true,0,.5],"horizontal-center":[true,false,.5,0],
    "top-left":[true,true,0,0],"top-right":[true,true,1,0],
    "bottom-left":[true,true,0,1],"bottom-right":[true,true,1,1],
    center:[true,true,.5,.5],none:[false,false,0,0]
  };
  const name=value===null?(hasSSRT?"top-left":"top"):value;
  if(!Object.prototype.hasOwnProperty.call(modes,name)) throw new Error("invalid gox isle adapt: "+name);
  return modes[name];
}
function goxAdaptFrames(initial,target,mode){
  const [width,height,x,y]=mode;
  const start={width:width?initial.width:target.width,height:height?initial.height:target.height};
  const frames=[{width:start.width+"px",height:start.height+"px"},{width:target.width+"px",height:target.height+"px"}];
  if(x||y){
    frames[0].translate=((target.width-start.width)*x)+"px "+((target.height-start.height)*y)+"px";
    frames[1].translate="0px 0px";
  }
  return {start,frames,x,y};
}
function goxIsHydrateMode(mode){
  return mode==="load"||mode==="idle"||mode==="visible"||mode==="hover"||mode==="interaction";
}
function goxCancelAll(list){
  for(const cancel of list.splice(0)){
    try{cancel()}catch{}
  }
}
function goxOnce(target,type,fn,options){
  const handler=(event)=>{
    target.removeEventListener(type,handler,options);
    fn(event);
  };
  target.addEventListener(type,handler,options);
  return ()=>target.removeEventListener(type,handler,options);
}
function goxAnyOnce(target,events,fn){
  let done=false;
  const removers=[];
  const handler=(event)=>{
    if(done) return;
    done=true;
    goxCancelAll(removers);
    fn(event);
  };
  for(const event of events){
    target.addEventListener(event.type,handler,event.options);
    removers.push(()=>target.removeEventListener(event.type,handler,event.options));
  }
  return ()=>{
    if(done) return;
    done=true;
    goxCancelAll(removers);
  };
}
function goxRequireArray(value,label){
  if(!Array.isArray(value)) throw new Error("invalid gox isle "+label+": expected array");
  return value;
}
function goxApplyShadowStyles(shadow,assets){
  if(!assets.length) return Promise.resolve();
  if("adoptedStyleSheets" in shadow && "CSSStyleSheet" in window){
    return Promise.all(assets.map(goxLoadSheet)).then(sheets=>{shadow.adoptedStyleSheets=sheets});
  }
  for(const asset of assets){
    const link=document.createElement("link");
    link.rel="stylesheet";
    link.href=asset;
    shadow.appendChild(link);
  }
  return Promise.resolve();
}
// display:contents has no observable box. Observe the rendered SSRT boxes.
function goxObserveVisible(island,start){
  let marker=null;
  let stopped=false;
  let mutations;
  const cancel=()=>{
    stopped=true;
    observer.disconnect();
    mutations?.disconnect();
    marker?.remove();
    marker=null;
  };
  const observer=new IntersectionObserver(entries=>{
    if(stopped||!entries.some(entry=>entry.isIntersecting)) return;
    cancel();
    start();
  });
  const refresh=()=>{
    if(stopped) return;
    observer.disconnect();
    const targets=[];
    const collect=element=>{
      if(element===marker) return;
      if(getComputedStyle(element).display!=="contents"){
        targets.push(element);
      }else{
        for(const child of element.children) collect(child);
      }
    };
    collect(island);
    if(targets.length){
      if(marker){marker.remove();marker=null;}
      for(const target of targets) observer.observe(target);
    }else{
      // Empty and text-only islands still need a position to observe.
      if(!marker){
        marker=document.createElement("span");
        marker.className="gox-isle-visible-marker";
        marker.setAttribute("aria-hidden","true");
        island.prepend(marker);
      }
      observer.observe(marker);
    }
  };
  mutations=new MutationObserver(refresh);
  refresh();
  mutations.observe(island,{childList:true,subtree:true,attributes:true,attributeFilter:["class","style","hidden"]});
  return cancel;
}
class GoxIsle extends HTMLElement{
  connectedCallback(){
    this.__goxConnected=true;
    if(this.__goxMounted||this.__goxPending) return;
    this.__goxPending=true;
    try{this.__goxSchedule()}catch(e){this.__goxPending=false; throw e}
  }
  __goxSchedule(){
    const hydrate=this.getAttribute("data-hydrate")||"load";
    if(!goxIsHydrateMode(hydrate)) throw new Error("invalid gox isle hydrate: "+hydrate);
    const start=()=>this.__goxStartOnce();
    if(hydrate==="load"){
      start();
      return;
    }
    if(hydrate==="idle"){
      if("requestIdleCallback" in window){
        const id=window.requestIdleCallback(start);
        this.__goxCancelHydrate=()=>window.cancelIdleCallback&&window.cancelIdleCallback(id);
      }else{
        const id=setTimeout(start,1);
        this.__goxCancelHydrate=()=>clearTimeout(id);
      }
      return;
    }
    if(hydrate==="visible"){
      if("IntersectionObserver" in window){
        this.__goxCancelHydrate=goxObserveVisible(this,start);
      }else{
        start();
      }
      return;
    }
    if(hydrate==="hover"){
      this.__goxCancelHydrate=goxOnce(this,"pointerenter",start,{passive:true});
      return;
    }
    if(hydrate==="interaction"){
      this.__goxCancelHydrate=goxAnyOnce(this,[
        {type:"pointerdown",options:{passive:true}},
        {type:"keydown"},
        {type:"touchstart",options:{passive:true}},
        {type:"focusin"}
      ],start);
      return;
    }
  }
  async __goxStartOnce(){
    if(this.__goxMounted) return;
    this.__goxCancelHydrate?.();
    this.__goxCancelHydrate=null;
    try{
      await this.__goxStart();
      if(!this.__goxConnected){
        if(typeof this.__goxCleanup==="function") this.__goxCleanup();
        this.__goxCleanup=null;
        this.__goxMounted=false;
        this.__goxPending=false;
        return;
      }
      this.__goxMounted=true;
      this.__goxPending=false;
    }catch(e){
      this.__goxPending=false;
      throw e;
    }
  }
  async __goxStart(){
    const script=this.getAttribute("data-script");
    if(!script) throw new Error("gox isle missing data-script");
    const props=goxParseJSON(this.getAttribute("data-props"),{},"props");
    const isolate=goxIsTrueAttr(this,"data-isolate");
    const transition=this.getAttribute("data-transition")||"";
    const assets=goxRequireArray(goxParseJSON(this.getAttribute("data-assets"),[],"assets"),"assets");
    const mod=await import(script);
    if(typeof mod.mount!=="function") throw new Error("gox isle module does not export mount(): "+script);
    const prepareShadow=async()=>{
      const shadow=this.shadowRoot||this.attachShadow({mode:"open"});
      shadow.replaceChildren();
      await goxApplyShadowStyles(shadow,assets);
      return shadow;
    };
    const mountFinal=async()=>{
      if(isolate){
        const shadow=await prepareShadow();
        if(!this.__goxConnected) return null;
        this.replaceChildren();
        return mod.mount(shadow,props);
      }
      this.replaceChildren();
      if(!this.__goxConnected) return null;
      return mod.mount(this,props);
    };
    const hasSSRT=Array.from(this.childNodes).some(node=>node.nodeType===1||(node.nodeType===3&&node.textContent.trim()!==""));
    const adaptValue=this.getAttribute("data-adapt");
    const adapt=goxAdaptMode(adaptValue,hasSSRT);
    if(!hasSSRT&&adaptValue===null&&typeof this.animate==="function"){
      this.classList.add("gox-isle-expand");
      let expansion;
      const finish=()=>{
        expansion?.cancel();
        this.classList.remove("gox-isle-expand");
        if(transition) this.classList.remove(transition+"-in");
      };
      this.__goxCancelExpand=finish;
      try{
        this.__goxCleanup=await mountFinal();
        if(!this.__goxConnected) return;
        // Measure the mounted content while its host still occupies zero height.
        const height=this.scrollHeight;
        expansion=this.animate([{height:"0px"},{height:height+"px"}],{
          duration:220,easing:"ease-out",fill:"forwards"
        });
        if(transition) this.classList.add(transition+"-in");
        await goxWaitAnimation(this);
      }finally{
        finish();
        this.__goxCancelExpand=null;
      }
      return;
    }
    if(!transition&&!hasSSRT&&adaptValue===null){
      this.__goxCleanup=await mountFinal();
      return;
    }
    const stage=document.createElement("div");
    stage.className="gox-isle-stage";
    const ssrtHost=document.createElement("div");
    ssrtHost.className="gox-isle-ssrt";
    const clientHost=document.createElement("div");
    clientHost.className="gox-isle-client";
    while(this.firstChild) ssrtHost.appendChild(this.firstChild);
    stage.appendChild(ssrtHost);
    stage.appendChild(clientHost);
    this.appendChild(stage);
    // Measure each tree independently: a grid containing both would report
    // the larger height and lose the starting size when the client grows.
    const measure=host=>{
      const range=document.createRange();
      range.selectNodeContents(host.shadowRoot||host);
      const rect=range.getBoundingClientRect();
      return {width:rect.width,height:Math.max(rect.height,host.scrollHeight)};
    };
    const availableWidth=stage.getBoundingClientRect().width;
    const initial=hasSSRT?measure(ssrtHost):{width:0,height:0};
    stage.style.width=initial.width+"px";
    stage.style.height=initial.height+"px";
    stage.style.position="relative";
    stage.style.overflow="hidden";
    for(const host of [ssrtHost,clientHost]){
      host.style.position="absolute";
      host.style.width=availableWidth+"px";
      host.style.top="0";
      host.style.left="0";
    }
    clientHost.style.visibility="hidden";
    let committed=false;
    const resizing=[];
    const cancelResize=()=>{for(const animation of resizing) animation.cancel()};
    this.__goxCancelExpand=cancelResize;
    try{
      let tempTarget=clientHost;
      if(isolate){
        const tempShadow=clientHost.attachShadow({mode:"open"});
        await goxApplyShadowStyles(tempShadow,assets);
        tempTarget=tempShadow;
      }
      if(!this.__goxConnected) return;
      const cleanup=await mod.mount(tempTarget,props);
      if(!this.__goxConnected){
        if(typeof cleanup==="function") cleanup();
        return;
      }
      this.__goxCleanup=cleanup;
      await goxNextFrame();
      if(!this.__goxConnected) return;
      const target=measure(clientHost);
      clientHost.style.visibility="";
      if(transition){
        clientHost.classList.add(transition+"-in");
        if(hasSSRT) ssrtHost.classList.add(transition+"-out");
      }else{
        ssrtHost.style.visibility="hidden";
      }
      const geometry=goxAdaptFrames(initial,target,adapt);
      // The nonanimated axis adopts its FINAL size before any animation starts.
      stage.style.width=geometry.start.width+"px";
      stage.style.height=geometry.start.height+"px";
      if(typeof stage.animate==="function"&&(geometry.start.width!==target.width||geometry.start.height!==target.height)){
        const options={duration:220,easing:"ease-out",fill:"forwards"};
        resizing.push(stage.animate(geometry.frames,options));
        if(geometry.x||geometry.y){
          // Counter the clipping box movement: content stays in its final layout
          // position. Animate offsets, leaving CSS transition transforms alone.
          resizing.push(clientHost.animate([
            {left:(geometry.start.width-target.width)*geometry.x+"px",top:(geometry.start.height-target.height)*geometry.y+"px"},
            {left:"0px",top:"0px"}
          ],options));
          if(hasSSRT) resizing.push(ssrtHost.animate([
            {left:(geometry.start.width-initial.width)*geometry.x+"px",top:(geometry.start.height-initial.height)*geometry.y+"px"},
            {left:(target.width-initial.width)*geometry.x+"px",top:(target.height-initial.height)*geometry.y+"px"}
          ],options));
        }
      }else{
        stage.style.width=target.width+"px";
        stage.style.height=target.height+"px";
      }
      await Promise.all([goxWaitAnimation(stage),goxWaitAnimation(clientHost),goxWaitAnimation(ssrtHost)]);
      if(!this.__goxConnected) return;
      // Keep the framework's root and all its descendants in place. Moving
      // children or remounting would lose state, delegated events and focus.
      ssrtHost.remove();
      stage.removeAttribute("style");
      clientHost.removeAttribute("style");
      stage.className="gox-isle-mounted";
      clientHost.className="gox-isle-mounted";
      committed=true;
    }finally{
      if(!committed){
        if(typeof this.__goxCleanup==="function") this.__goxCleanup();
        this.__goxCleanup=null;
      }
      cancelResize();
      this.__goxCancelExpand=null;
      this.classList.remove("gox-isle-commit");
    }
  }
  disconnectedCallback(){
    this.__goxConnected=false;
    this.__goxCancelExpand?.();
    this.__goxCancelExpand=null;
    this.__goxCancelHydrate?.();
    this.__goxCancelHydrate=null;
    if(typeof this.__goxCleanup==="function") this.__goxCleanup();
    this.__goxCleanup=null;
    this.__goxMounted=false;
    this.__goxPending=false;
  }
}
if(!customElements.get("gox-isle")) customElements.define("gox-isle",GoxIsle);
`

const goxActionsRuntimeSource = `import {htmlToNode,normalizeClientValue} from "/_gox/client/values.js";
const ERRORS={
  ACTION_NOT_DEFINED:[1000,"ACTION_NOT_DEFINED"],
  NETWORK_ERROR:[1001,"NETWORK_ERROR"],
  SERVER_ERROR:[1002,"SERVER_ERROR"],
  ACTION_ERROR:[1003,"ACTION_ERROR"],
  INVALID_ARGUMENTS:[1004,"INVALID_ARGUMENTS"],
  METHOD_NOT_ALLOWED:[1005,"METHOD_NOT_ALLOWED"],
  INVALID_RESPONSE:[1006,"INVALID_RESPONSE"],
  UNSUPPORTED_RESPONSE_TYPE:[1007,"UNSUPPORTED_RESPONSE_TYPE"]
};
class GoxActionError extends Error{
  constructor(kind,message,action,status,cause){
    const meta=ERRORS[kind]||[1999,"UNKNOWN_ERROR"];
    super(message||meta[1]);
    this.name="GoxActionError";
    this.code=meta[0];
    this.type=meta[1];
    this.action=action||"";
    this.status=status||0;
    if(cause!==undefined) this.cause=cause;
  }
}
function readActionSignatures(){
  const el=document.getElementById("_GOX-ACTIONS");
  if(!el) return {};
  try{
    const value=JSON.parse(el.textContent||"{}");
    return value&&typeof value==="object"&&!Array.isArray(value)?value:{};
  }catch{return {};}
}
const signatures=readActionSignatures();
const known=new Set(Object.keys(signatures));
const route=location.pathname||"/";
const cache=new Map();
function err(kind,message,action,status,cause){return new GoxActionError(kind,message,action,status,cause)}
function isFileArrayArg(value){
  return Array.isArray(value)||(typeof FileList!=="undefined"&&value instanceof FileList);
}
function isBinaryArg(value){
  if(value instanceof File || value instanceof Blob || value instanceof ArrayBuffer || ArrayBuffer.isView(value)) return true;
  if(isFileArrayArg(value)){
    for(const item of value){
      if(item instanceof File || item instanceof Blob || item instanceof ArrayBuffer || ArrayBuffer.isView(item)) return true;
    }
  }
  return false;
}
function appendBinaryArg(body,key,value){
  if(value instanceof File){body.append(key,value);return null;}
  if(value instanceof Blob){body.append(key,value);return null;}
  if(value instanceof ArrayBuffer){body.append(key,new Blob([value]));return null;}
  if(ArrayBuffer.isView(value)){body.append(key,new Blob([value]));return null;}
  return new TypeError("expected File");
}
function appendArg(body,index,value,binary){
  const key=String(index);
  if(binary){
    if(isFileArrayArg(value)){
      for(const item of value){
        const e=appendBinaryArg(body,key+"[]",item);
        if(e) return e;
      }
      return null;
    }
    const e=appendBinaryArg(body,key,value);
    if(!e) return null;
  }
  if(value instanceof Date){body.append(key,value.toISOString());return null;}
  if(value===undefined){body.append(key,"");return null;}
  if(value===null){body.append(key,"null");return null;}
  const t=typeof value;
  if(t==="object"){
    try{body.append(key,JSON.stringify(value));return null;}catch(e){return e;}
  }
  if(t==="bigint"){body.append(key,value.toString());return null;}
  body.append(key,String(value));
  return null;
}
function buildActionBody(args,action){
  let binary=false;
  for(let i=0;i<args.length;i++){ if(isBinaryArg(args[i])){binary=true;break;} }
  const body=binary?new FormData():new URLSearchParams();
  for(let i=0;i<args.length;i++){
    const e=appendArg(body,i,args[i],binary);
    if(e) return [null,err("INVALID_ARGUMENTS","Action argument "+i+" for "+String(action)+" cannot be serialized.",String(action),0,e)];
  }
  return [body,null];
}
function validFormFile(value){
  return !(value instanceof File && value.name==="" && value.size===0);
}
function isMultipleFileField(field){
  if(typeof HTMLInputElement!=="undefined"&&field instanceof HTMLInputElement){
    return field.type==="file"&&field.multiple;
  }
  if(typeof RadioNodeList!=="undefined"&&field instanceof RadioNodeList){
    for(const item of field){
      if(typeof HTMLInputElement!=="undefined"&&item instanceof HTMLInputElement&&item.type==="file"&&item.multiple) return true;
    }
  }
  return false;
}
function formField(form,name){
  const elements=form.elements;
  if(elements&&typeof elements.namedItem==="function") return elements.namedItem(name);
  return elements?elements[name]:null;
}
function argsFromForm(action,form){
  const signature=signatures[action];
  if(!Array.isArray(signature)) return [null,err("INVALID_ARGUMENTS",action+"(): action signature not found",action)];
  const fd=new FormData(form);
  const args=[];
  for(const name of signature){
    const field=formField(form,name);
    if(!field) return [null,err("INVALID_ARGUMENTS",action+"(): argument "+JSON.stringify(name)+" cannot be undefined",action)];
    if((typeof HTMLInputElement!=="undefined"&&field instanceof HTMLInputElement)||
       (typeof HTMLSelectElement!=="undefined"&&field instanceof HTMLSelectElement)||
       (typeof HTMLTextAreaElement!=="undefined"&&field instanceof HTMLTextAreaElement)){
      try { args.push(normalizeClientValue(field).value); }
      catch(e) { return [null,err("INVALID_ARGUMENTS",action+"(): argument "+JSON.stringify(name)+" cannot be normalized",action,0,e)]; }
      continue;
    }
    if(isMultipleFileField(field)){
      const values=fd.getAll(name).filter(validFormFile);
      if(values.length===0) return [null,err("INVALID_ARGUMENTS",action+"(): argument "+JSON.stringify(name)+" cannot be undefined",action)];
      args.push(values);
      continue;
    }
    const value=fd.get(name);
    if(value===null||!validFormFile(value)) return [null,err("INVALID_ARGUMENTS",action+"(): argument "+JSON.stringify(name)+" cannot be undefined",action)];
    args.push(value);
  }
  return [args,null];
}
function normalizeActionArgs(action,args){
  if(args.length===1&&typeof HTMLFormElement!=="undefined"&&args[0] instanceof HTMLFormElement){
    return argsFromForm(action,args[0]);
  }
  try{return [args.map(value=>normalizeClientValue(value).value),null];}
  catch(e){return [null,err("INVALID_ARGUMENTS",action+"(): arguments cannot be normalized",action,0,e)];}
}
function parseActionNumber(text){
  const value=Number(text);
  if(!Number.isFinite(value)) throw new TypeError("Invalid Action number response");
  return value;
}
function parseActionBigInt(text){
  if(!/^(0|-?[1-9][0-9]*)$/.test(text)) throw new TypeError("Invalid Action bigint response");
  return BigInt(text);
}
function parseActionBoolean(text){
  if(text==="true") return true;
  if(text==="false") return false;
  throw new TypeError("Invalid Action boolean response");
}
function actionResponseFilename(response){
  const disposition=response.headers.get("Content-Disposition")||"";
  const extended=disposition.match(/(?:^|;)\s*filename\*=\s*UTF-8''([^;]*)/i);
  if(extended){
    try{
      const value=decodeURIComponent(extended[1].trim());
      if(value) return value;
    }catch{}
  }
  const quoted=disposition.match(/(?:^|;)\s*filename\s*=\s*"((?:\\.|[^"\\])*)"/i);
  if(quoted){
    const value=quoted[1].replace(/\\(["\\])/g,"$1");
    if(value) return value;
  }
  const token=disposition.match(/(?:^|;)\s*filename\s*=\s*([^;\s]+)/i);
  if(token&&token[1]) return token[1];
  throw new TypeError("Invalid Action file response filename");
}
async function readActionFile(response){
  const name=actionResponseFilename(response);
  const blob=await response.blob();
  const contentType=blob.type||(response.headers.get("Content-Type")||"");
  return new File([blob],name,{type:contentType});
}
async function readActionFiles(response){
  const form=await response.formData();
  const values=form.getAll("files");
  for(const value of values){
    if(!(value instanceof File)) throw new TypeError("Invalid Action files response part");
  }
  return values;
}
async function readLegacyBody(response){
  const contentType=(response.headers.get("Content-Type")||"").toLowerCase();
  if(response.body&&(contentType.includes("application/octet-stream")||contentType.includes("application/stream")||contentType.includes("application/x-ndjson"))){
    return response.body;
  }
  if(contentType.includes("application/json")) return await response.json();
  if(contentType.includes("text/html")) return htmlToNode(await response.text());
  if(contentType.includes("text/")||contentType==="") return await response.text();
  return response.body||await response.blob();
}
async function readBody(response){
  const actionType=(response.headers.get("Action-Type")||"").trim().toLowerCase();
  if(actionType==="") return await readLegacyBody(response);
  switch(actionType){
    case "null": return null;
    case "text": return await response.text();
    case "number": return parseActionNumber(await response.text());
    case "bigint": return parseActionBigInt(await response.text());
    case "boolean": return parseActionBoolean(await response.text());
    case "json": return await response.json();
    case "html": return htmlToNode(await response.text());
    case "file": return await readActionFile(response);
    case "files": return await readActionFiles(response);
    case "binary":
    case "stream": return response.body||await response.blob();
    default: throw new TypeError("Unsupported Action-Type "+JSON.stringify(actionType));
  }
}
async function call(action,args){
  action=String(action);
  if(!known.has(action)) return [null,err("ACTION_NOT_DEFINED","Action "+action+" is not defined on this page.",action)];
  const normalized=normalizeActionArgs(action,args);
  if(normalized[1]) return [null,normalized[1]];
  args=normalized[0];
  const built=buildActionBody(args,action);
  if(built[1]) return [null,built[1]];
  let response;
  try{
    response=await fetch(route,{method:"POST",headers:{"Invoke-Action":action,"Gox-Action-Redirect":"1"},body:built[0],redirect:"manual"});
  }catch(e){
    return [null,err("NETWORK_ERROR","Network error while invoking action "+action+".",action,0,e)];
  }
  const navigationStatus=response.headers.get("Action-Response")==="redirect"
    ? Number(response.headers.get("Action-Redirect-Status")) : response.status;
  if(response.headers.get("Action-Response")==="redirect"||[301,302,303,307,308].includes(response.status)){
    const destination=response.headers.get("Location");
    try{
      if(![301,302,303,307,308].includes(navigationStatus)||!destination||!destination.trim()||/[\u0000-\u001f\u007f]/.test(destination)) throw new Error("Invalid redirect");
      const url=new URL(destination,location.href);
      if(!["http:","https:"].includes(url.protocol)||url.username||url.password) throw new Error("Invalid redirect");
      location.assign(url.href);
      // Navigation replaces this document. Do not run success/error callbacks
      // from the old page while the destination is loading.
      return await new Promise(()=>{});
    }catch(e){
      return [null,err("INVALID_REDIRECT","Invalid action redirect.",action,response.status)];
    }
  }
  if(response.type==="opaqueredirect") return [null,err("INVALID_REDIRECT","The server did not negotiate an action redirect.",action,response.status)];
  if(response.status===405) return [null,err("METHOD_NOT_ALLOWED","Action "+action+" is not allowed on this route.",action,response.status)];
  if(response.status>=500) return [null,err("SERVER_ERROR","Server error while invoking action "+action+".",action,response.status)];
  const actionResponse=(response.headers.get("Action-Response")||"").toLowerCase();
  if(actionResponse!=="success"&&actionResponse!=="error") return [null,err("INVALID_RESPONSE","Invalid action response for "+action+".",action,response.status)];
  let body;
  try{body=await readBody(response)}catch(e){return [null,err("UNSUPPORTED_RESPONSE_TYPE","Unable to read action response for "+action+".",action,response.status,e)]}
  if(actionResponse==="error"){
    const message=typeof body==="string"?body:String(body||"Action error");
    return [null,err("ACTION_ERROR",message,action,response.status)];
  }
  return [body,null];
}
const target={};
const Actions=new Proxy(target,{
  get(_,prop){
    if(prop==="__goxActions") return Array.from(known);
    if(prop===Symbol.toStringTag) return "GoxActions";
    if(typeof prop!=="string") return undefined;
    if(!cache.has(prop)) cache.set(prop,(...args)=>call(prop,args));
    return cache.get(prop);
  },
  has(_,prop){return typeof prop==="string"&&known.has(prop)},
  ownKeys(){return Array.from(known)},
  getOwnPropertyDescriptor(_,prop){if(typeof prop==="string"&&known.has(prop)) return {enumerable:true,configurable:true};}
});
export {GoxActionError};
export default Actions;
`

const goxIsleBaseStyleSource = `*,*::before,*::after{box-sizing:border-box}
html,body{margin:0;padding:0}
:where(h1,h2,h3,h4,h5,h6,p,figure,blockquote,dl,dd){margin:0}
gox-isle{display:contents}
gox-isle.gox-isle-commit{visibility:hidden}
gox-isle.gox-isle-expand{display:block;height:0;overflow:hidden}
gox-isle.gox-isle-expand>*{content-visibility:visible}
gox-isle>*{content-visibility:auto}
.gox-isle-mounted{display:contents}
.gox-isle-visible-marker{display:inline-block;width:1px;height:1px;margin-right:-1px;opacity:0;pointer-events:none}
.gox-isle-stage{display:grid;grid-template-areas:"gox-isle-stack";align-items:stretch;justify-items:stretch;min-width:0;min-height:0;box-sizing:border-box}
.gox-isle-ssrt,.gox-isle-client{grid-area:gox-isle-stack;min-width:0;min-height:0;box-sizing:border-box}
`

type RuntimeAsset struct {
	URL  string
	File string
}

func EnsureGoxIsleRuntime(projectRoot string) (RuntimeAsset, error) {
	return ensureRuntimeAsset(projectRoot, "gox-isle-", ".js", goxIsleRuntimeSource)
}

func EnsureGoxIsleBaseStyle(projectRoot string) (RuntimeAsset, error) {
	return ensureRuntimeAsset(projectRoot, "gox-isle-", ".css", goxIsleBaseStyleSource)
}

func EnsureGoxActionsRuntime(projectRoot string) (RuntimeAsset, error) {
	return ensureRuntimeAsset(projectRoot, "gox-actions-", ".js", goxActionsRuntimeSource)
}

func ensureRuntimeAsset(projectRoot string, prefix string, ext string, source string) (RuntimeAsset, error) {
	sum := sha256.Sum256([]byte(source))
	hash := hex.EncodeToString(sum[:])[:8]
	name := prefix + hash + ext
	file := filepath.Join(projectRoot, ".build", "client", "runtime", name)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return RuntimeAsset{}, err
	}
	if cur, err := os.ReadFile(file); err != nil || string(cur) != source {
		if err := os.WriteFile(file, []byte(source), 0644); err != nil {
			return RuntimeAsset{}, err
		}
	}
	return RuntimeAsset{URL: PublicClientPrefix + "/" + name, File: file}, nil
}

func WriteFrameworkWrappers(assets []Asset) ([]Asset, error) {
	out := make([]Asset, len(assets))
	for i, asset := range assets {
		next := asset
		wrapper, err := writeFrameworkWrapper(asset)
		if err != nil {
			return nil, err
		}
		next.WrapperAbsPath = wrapper
		out[i] = next
	}
	return out, nil
}

func writeFrameworkWrapper(asset Asset) (string, error) {
	mode := asset.Mode
	if mode == "" {
		if asset.Dev {
			mode = ModeDev
		} else {
			mode = ModeProd
		}
	}
	dir := filepath.Join(asset.ProjectRoot, ".build", "client", "runtime", "wrappers", asset.Framework, mode)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	ext := frameworkWrapperExtension(asset)
	file := filepath.Join(dir, asset.PublicBaseName()+".wrapper"+ext)
	content := frameworkWrapperSource(asset)
	if cur, err := os.ReadFile(file); err != nil || string(cur) != content {
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			return "", err
		}
	}
	return file, nil
}

func frameworkWrapperExtension(asset Asset) string {
	framework := strings.ToLower(strings.TrimSpace(asset.Framework))
	switch framework {
	case "vue", "svelte":
		return ".js"
	case "react", "preact", "solid":
		return ".jsx"
	default:
		ext := filepath.Ext(asset.AbsPath)
		if ext == "" || ext == ".vue" || ext == ".svelte" {
			return ".js"
		}
		return ext
	}
}

func frameworkWrapperSource(asset Asset) string {
	switch strings.ToLower(strings.TrimSpace(asset.Framework)) {
	case "react":
		return reactFrameworkWrapperSource(asset)
	case "preact":
		return preactFrameworkWrapperSource(asset)
	case "solid":
		return solidFrameworkWrapperSource(asset)
	case "vue":
		return vueFrameworkWrapperSource(asset)
	case "svelte":
		return svelteFrameworkWrapperSource(asset)
	default:
		return `import Component from ` + jsString(asset.AbsPath) + `

export function mount(target, props = {}) {
  if (Component && typeof Component.mount === "function") {
    return Component.mount(target, props)
  }
  throw new Error("GoX framework wrapper is not implemented for ` + asset.Framework + `")
}
`
	}
}

func preactFrameworkWrapperSource(asset Asset) string {
	return `import { h, render } from "preact"
import Component from ` + jsString(asset.AbsPath) + `

export function mount(target, props = {}) {
  render(h(Component, props), target)
  return () => render(null, target)
}
`
}

func solidFrameworkWrapperSource(asset Asset) string {
	return `import { render } from "solid-js/web"
import Component from ` + jsString(asset.AbsPath) + `

export function mount(target, props = {}) {
  return render(() => Component(props), target)
}
`
}

func vueFrameworkWrapperSource(asset Asset) string {
	return `import { createApp } from "vue"
import Component from ` + jsString(asset.AbsPath) + `

export function mount(target, props = {}) {
  const app = createApp(Component, props)
  app.mount(target)
  return () => app.unmount()
}
`
}

func svelteFrameworkWrapperSource(asset Asset) string {
	return `import { mount as svelteMount, unmount as svelteUnmount } from "svelte"
import Component from ` + jsString(asset.AbsPath) + `

export function mount(target, props = {}) {
  const instance = svelteMount(Component, { target, props })
  return () => svelteUnmount(instance)
}
`
}

func assetFrameworkRoot(asset Asset) string {
	frameworkRoot := strings.TrimSpace(asset.FrameworkRoot)
	if frameworkRoot == "" {
		frameworkRoot = filepath.Join(asset.ProjectRoot, "client", asset.Framework)
	}
	return frameworkRoot
}

func reactFrameworkWrapperSource(asset Asset) string {
	return `import React from "react"
import { createRoot } from "react-dom/client"
import { flushSync } from "react-dom"
import Component from ` + jsString(asset.AbsPath) + `

export function mount(target, props = {}) {
  const root = createRoot(target)
  // GOX replaces SSRT immediately; mount must commit before returning.
  flushSync(() => root.render(React.createElement(Component, props)))
  return () => root.unmount()
}
`
}
