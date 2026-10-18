import assert from "node:assert/strict";
import { File as NodeFile } from "node:buffer";
import { readFile } from "node:fs/promises";
import test from "node:test";

if (typeof globalThis.File === "undefined") globalThis.File = NodeFile;

class FakeNode {
  constructor(nodeType, textContent = "") {
    this.nodeType = nodeType;
    this.textContent = textContent;
  }
}

class FakeHTMLElement extends FakeNode {
  constructor(tagName, textContent = "") {
    super(1, textContent);
    this.tagName = tagName.toUpperCase();
  }
}

class FakeDocumentFragment extends FakeNode {
  constructor(nodes = []) {
    super(11, "");
    this.childNodes = nodes;
  }
  removeChild(node) {
    const index = this.childNodes.indexOf(node);
    if (index < 0) throw new Error("node not found");
    this.childNodes.splice(index, 1);
    return node;
  }
}

class FakeTemplate {
  constructor() {
    this.content = new FakeDocumentFragment();
  }
  set innerHTML(source) {
    const nodes = [];
    const pattern = /<([a-z][a-z0-9-]*)\b[^>]*>([\s\S]*?)<\/\1>/gi;
    let cursor = 0;
    let match;
    while ((match = pattern.exec(source)) !== null) {
      if (match.index > cursor) nodes.push(new FakeNode(3, source.slice(cursor, match.index)));
      nodes.push(new FakeHTMLElement(match[1], match[2].replace(/<[^>]+>/g, "")));
      cursor = pattern.lastIndex;
    }
    if (cursor < source.length) nodes.push(new FakeNode(3, source.slice(cursor)));
    this.content = new FakeDocumentFragment(nodes);
  }
}

globalThis.HTMLElement = FakeHTMLElement;
globalThis.DocumentFragment = FakeDocumentFragment;

async function loadActionsRuntime() {
  const goSource = await readFile(new URL("./runtime.go", import.meta.url), "utf8");
  const match = goSource.match(/const goxActionsRuntimeSource = `([\s\S]*?)`\n\nconst goxIsleBaseStyleSource/);
  assert.ok(match, "goxActionsRuntimeSource must be extractable from runtime.go");

  const signatures = {
    download: [],
    downloadUnicode: [],
    downloadMany: [],
    downloadNone: [],
    invalidFile: [],
    invalidFiles: [],
    htmlSingle: [],
    htmlMultiple: [],
    htmlMixed: [],
    htmlEmpty: [],
  };
  globalThis.document = {
    createElement(name) {
      assert.equal(name, "template");
      return new FakeTemplate();
    },
    getElementById(id) {
      if (id !== "_GOX-ACTIONS") return null;
      return { textContent: JSON.stringify(signatures) };
    },
  };
  globalThis.location = { pathname: "/downloads" };

  const source = match[1].replace(
    'import {htmlToNode,normalizeClientValue} from "/_gox/client/values.js";',
    "const normalizeClientValue=value=>({value});\nconst htmlToNode=source=>{const template=document.createElement(\"template\");template.innerHTML=String(source);const fragment=template.content;const significant=Array.from(fragment.childNodes).filter(node=>node.nodeType!==3||String(node.textContent??\"\").trim()!==\"\");return significant.length===1&&significant[0] instanceof HTMLElement?fragment.removeChild(significant[0]):fragment};",
  );
  const url = `data:text/javascript;base64,${Buffer.from(source).toString("base64")}`;
  return await import(url);
}

const runtime = await loadActionsRuntime();
const Actions = runtime.default;
let responseFactory;
globalThis.fetch = async () => responseFactory();

function successResponse(body, headers = {}) {
  return new Response(body, {
    status: 200,
    headers: {
      "Action-Response": "success",
      ...headers,
    },
  });
}

test("Action-Type file returns a browser File with metadata and content", async () => {
  responseFactory = () => successResponse("hello file", {
    "Action-Type": "file",
    "Content-Type": "text/plain; charset=utf-8",
    "Content-Disposition": 'attachment; filename="hello.txt"',
  });

  const [file, error] = await Actions.download();
  assert.equal(error, null);
  assert.ok(file instanceof File);
  assert.equal(file.name, "hello.txt");
  assert.equal(file.type, "text/plain;charset=utf-8");
  assert.equal(await file.text(), "hello file");
});

test("Action-Type file decodes RFC 5987 UTF-8 filenames", async () => {
  responseFactory = () => successResponse("contenido", {
    "Action-Type": "file",
    "Content-Type": "text/plain",
    "Content-Disposition": "attachment; filename*=UTF-8''reporte%20a%C3%B1o.txt",
  });

  const [file, error] = await Actions.downloadUnicode();
  assert.equal(error, null);
  assert.equal(file.name, "reporte año.txt");
  assert.equal(await file.text(), "contenido");
});

test("Action-Type files returns ordered File[] from multipart", async () => {
  responseFactory = () => {
    const form = new FormData();
    form.append("files", new File(["first"], "first.txt", { type: "text/plain" }));
    form.append("files", new File([Uint8Array.from([1, 2, 3])], "second.bin", { type: "application/octet-stream" }));
    return successResponse(form, { "Action-Type": "files" });
  };

  const [files, error] = await Actions.downloadMany();
  assert.equal(error, null);
  assert.ok(Array.isArray(files));
  assert.equal(files.length, 2);
  assert.ok(files.every(file => file instanceof File));
  assert.deepEqual(files.map(file => file.name), ["first.txt", "second.bin"]);
  assert.equal(await files[0].text(), "first");
  assert.deepEqual([...new Uint8Array(await files[1].arrayBuffer())], [1, 2, 3]);
});

test("Action-Type files supports a valid empty multipart response", async () => {
  responseFactory = () => successResponse(new FormData(), { "Action-Type": "files" });
  const [files, error] = await Actions.downloadNone();
  assert.equal(error, null);
  assert.deepEqual(files, []);
});

test("Action-Type file rejects a missing filename as an unsupported response", async () => {
  responseFactory = () => successResponse("broken", {
    "Action-Type": "file",
    "Content-Type": "text/plain",
  });

  const [value, error] = await Actions.invalidFile();
  assert.equal(value, null);
  assert.equal(error?.type, "UNSUPPORTED_RESPONSE_TYPE");
  assert.match(error?.cause?.message || "", /filename/i);
});

test("Action-Type files rejects non-file multipart parts", async () => {
  responseFactory = () => {
    const form = new FormData();
    form.append("files", "not-a-file");
    return successResponse(form, { "Action-Type": "files" });
  };

  const [value, error] = await Actions.invalidFiles();
  assert.equal(value, null);
  assert.equal(error?.type, "UNSUPPORTED_RESPONSE_TYPE");
  assert.match(error?.cause?.message || "", /part/i);
});

test("Action-Type file preserves an empty file and emoji filename", async () => {
  responseFactory = () => successResponse("", {
    "Action-Type": "file",
    "Content-Type": "text/plain",
    "Content-Disposition": "attachment; filename*=UTF-8''empty-%F0%9F%9A%80.txt",
  });

  const [file, error] = await Actions.downloadUnicode();
  assert.equal(error, null);
  assert.ok(file instanceof File);
  assert.equal(file.name, "empty-🚀.txt");
  assert.equal(file.size, 0);
  assert.equal(await file.text(), "");
});

test("Action-Type file falls back to filename when filename* is invalid", async () => {
  responseFactory = () => successResponse("fallback", {
    "Action-Type": "file",
    "Content-Type": "text/plain",
    "Content-Disposition": "attachment; filename*=UTF-8''%ZZ; filename=backup.txt",
  });

  const [file, error] = await Actions.download();
  assert.equal(error, null);
  assert.equal(file.name, "backup.txt");
  assert.equal(await file.text(), "fallback");
});

test("Action-Type files preserves duplicate names, order, empty files, and ignores unrelated fields", async () => {
  responseFactory = () => {
    const form = new FormData();
    form.append("metadata", "ignored");
    form.append("files", new File([""], "same-🚀.txt", { type: "text/plain" }));
    form.append("files", new File(["second"], "same-🚀.txt", { type: "text/plain" }));
    return successResponse(form, { "Action-Type": "files" });
  };

  const [files, error] = await Actions.downloadMany();
  assert.equal(error, null);
  assert.deepEqual(files.map(file => file.name), ["same-🚀.txt", "same-🚀.txt"]);
  assert.deepEqual(files.map(file => file.size), [0, 6]);
  assert.equal(await files[0].text(), "");
  assert.equal(await files[1].text(), "second");
});


test("Action-Type html returns an HTMLElement for one root element", async () => {
  responseFactory = () => successResponse("  <nav>Menu</nav>  ", { "Action-Type": "html" });
  const [value, error] = await Actions.htmlSingle();
  assert.equal(error, null);
  assert.ok(value instanceof HTMLElement);
  assert.equal(value.tagName, "NAV");
  assert.equal(value.textContent, "Menu");
});

test("Action-Type html returns a DocumentFragment for multiple roots", async () => {
  responseFactory = () => successResponse("<header>Top</header><main>Body</main>", { "Action-Type": "html" });
  const [value, error] = await Actions.htmlMultiple();
  assert.equal(error, null);
  assert.ok(value instanceof DocumentFragment);
  assert.equal(value.childNodes.length, 2);
  assert.deepEqual(value.childNodes.map(node => node.tagName), ["HEADER", "MAIN"]);
});

test("Action-Type html preserves mixed text and elements in a DocumentFragment", async () => {
  responseFactory = () => successResponse("Hello <strong>George</strong>", { "Action-Type": "html" });
  const [value, error] = await Actions.htmlMixed();
  assert.equal(error, null);
  assert.ok(value instanceof DocumentFragment);
  assert.equal(value.childNodes.length, 2);
  assert.equal(value.childNodes[0].textContent, "Hello ");
  assert.equal(value.childNodes[1].tagName, "STRONG");
});

test("legacy text/html also returns an insertable DOM node", async () => {
  responseFactory = () => successResponse("<section>Legacy</section>", { "Content-Type": "text/html" });
  const [value, error] = await Actions.htmlSingle();
  assert.equal(error, null);
  assert.ok(value instanceof HTMLElement);
  assert.equal(value.tagName, "SECTION");
});

test("empty Action HTML returns an empty DocumentFragment", async () => {
  responseFactory = () => successResponse("", { "Action-Type": "html" });
  const [value, error] = await Actions.htmlEmpty();
  assert.equal(error, null);
  assert.ok(value instanceof DocumentFragment);
  assert.equal(value.childNodes.length, 0);
});
