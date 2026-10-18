import assert from "node:assert/strict";
import { File as NodeFile } from "node:buffer";
import { readFile } from "node:fs/promises";

if (typeof globalThis.File === "undefined") globalThis.File = NodeFile;

const baseURL = process.env.GOX_ACTIONS_TEST_URL;
assert.ok(baseURL, "GOX_ACTIONS_TEST_URL is required");
const endpoint = new URL(baseURL);

const goSource = await readFile(new URL("./runtime.go", import.meta.url), "utf8");
const match = goSource.match(/const goxActionsRuntimeSource = `([\s\S]*?)`\n\nconst goxIsleBaseStyleSource/);
assert.ok(match, "goxActionsRuntimeSource must be extractable from runtime.go");

const signatures = {
  download: [],
  downloadUnicode: [],
  downloadMany: [],
  downloadNone: [],
  downloadEmptyEmoji: [],
  downloadDuplicates: [],
};
globalThis.document = {
  getElementById(id) {
    if (id !== "_GOX-ACTIONS") return null;
    return { textContent: JSON.stringify(signatures) };
  },
};
globalThis.location = { pathname: endpoint.pathname };

const nativeFetch = globalThis.fetch;
globalThis.fetch = (input, init) => nativeFetch(new URL(String(input), endpoint), init);

const source = match[1].replace(
  'import {htmlToNode,normalizeClientValue} from "/_gox/client/values.js";',
  "const normalizeClientValue=value=>({value});\nconst htmlToNode=value=>value;",
);
const runtimeURL = `data:text/javascript;base64,${Buffer.from(source).toString("base64")}`;
const { default: Actions } = await import(runtimeURL);

{
  const [file, error] = await Actions.download();
  assert.equal(error, null);
  assert.ok(file instanceof File);
  assert.equal(file.name, "hello.txt");
  assert.equal(file.type, "text/plain;charset=utf-8");
  assert.equal(await file.text(), "hello from gox");
}

{
  const [file, error] = await Actions.downloadUnicode();
  assert.equal(error, null);
  assert.ok(file instanceof File);
  assert.equal(file.name, "reporte año.txt");
  assert.equal(file.type, "text/plain");
  assert.equal(await file.text(), "contenido unicode");
}

{
  const [files, error] = await Actions.downloadMany();
  assert.equal(error, null);
  assert.ok(Array.isArray(files));
  assert.equal(files.length, 2);
  assert.ok(files.every(file => file instanceof File));
  assert.deepEqual(files.map(file => file.name), ["first.txt", "second.bin"]);
  assert.deepEqual(files.map(file => file.type), ["text/plain", "application/octet-stream"]);
  assert.equal(await files[0].text(), "first from gox");
  assert.deepEqual([...new Uint8Array(await files[1].arrayBuffer())], [0, 1, 2, 255]);
}

{
  const [files, error] = await Actions.downloadNone();
  assert.equal(error, null);
  assert.deepEqual(files, []);
}


{
  const [file, error] = await Actions.downloadEmptyEmoji();
  assert.equal(error, null);
  assert.ok(file instanceof File);
  assert.equal(file.name, "empty-🚀.txt");
  assert.equal(file.size, 0);
  assert.equal(await file.text(), "");
}

{
  const [files, error] = await Actions.downloadDuplicates();
  assert.equal(error, null);
  assert.deepEqual(files.map(file => file.name), ["same-🚀.txt", "same-🚀.txt"]);
  assert.deepEqual(files.map(file => file.size), [0, 6]);
  assert.equal(await files[0].text(), "");
  assert.equal(await files[1].text(), "second");
}
