import assert from "node:assert/strict"
import test from "node:test"

import { ClientValueKind, GoxClientValueError, normalizeClientValue, normalizeClientValues } from "./values.js"

class Input { constructor(values = {}) { Object.assign(this, { type: "text", value: "", multiple: false, files: null }, values) } }
class TextArea { constructor(value) { this.value = value } }
class Select { constructor(values = {}) { Object.assign(this, { multiple: false, value: "", selectedOptions: [] }, values) } }
class File { constructor(name, type = "application/octet-stream") { this.name = name; this.type = type } }
class FileList extends Array {}
class Form {}
class Button {}

Object.assign(globalThis, {
	HTMLInputElement: Input,
	HTMLTextAreaElement: TextArea,
	HTMLSelectElement: Select,
	HTMLFormElement: Form,
	HTMLButtonElement: Button,
	File,
	FileList,
})

test("raw client values preserve their semantic kinds", () => {
	const date = new Date("2026-07-15T12:30:00.000Z")
	const values = normalizeClientValues([null, "hello", 42, true, 2n ** 80n, date, ["a", "b"]])
	assert.deepEqual(values.map(value => value.kind), ["null", "text", "number", "boolean", "bigint", "date", "json"])
	assert.notEqual(values[5].value, date)
	assert.equal(values[5].value.toISOString(), date.toISOString())
})

test("text, numeric, boolean and textual date controls normalize directly", () => {
	const date = new Date("2026-07-15T00:00:00.000Z")
	const localDateTime = "2026-07-15T10:30"
	const values = normalizeClientValues([
		new Input({ value: "Javier" }),
		new Input({ type: "number", valueAsNumber: 42 }),
		new Input({ type: "range", valueAsNumber: 7 }),
		new Input({ type: "checkbox", checked: true }),
		new Input({ type: "radio", checked: false }),
		new Input({ type: "date", valueAsDate: date }),
		new Input({ type: "datetime-local", value: localDateTime }),
		new Input({ type: "time", value: "10:30" }),
		new TextArea("biography"),
	])
	assert.deepEqual(values.map(value => value.kind), ["text", "number", "number", "boolean", "boolean", "date", "date", "text", "text"])
	assert.deepEqual(values.slice(0, 5).map(value => value.value), ["Javier", 42, 7, true, false])
	assert.equal(values[5].value.toISOString(), date.toISOString())
	assert.equal(values[6].value.toISOString(), new Date(localDateTime).toISOString())
	assert.equal(values[7].value, "10:30")
})

test("select-one is text and select-multiple is a JSON array", () => {
	assert.deepEqual(normalizeClientValue(new Select({ value: "admin" })), { kind: ClientValueKind.TEXT, value: "admin" })
	assert.deepEqual(normalizeClientValue(new Select({ multiple: true, selectedOptions: [{ value: "read" }, { value: "write" }] })), {
		kind: ClientValueKind.JSON,
		value: ["read", "write"],
	})
})

test("file controls, FileList and File arrays preserve names and order", () => {
	const avatar = new File("avatar.png", "image/png")
	const resume = new File("resume.pdf", "application/pdf")
	const list = new FileList(avatar, resume)
	const values = [
		normalizeClientValue(new Input({ type: "file", files: new FileList(avatar) })),
		normalizeClientValue(new Input({ type: "file", multiple: true, files: list })),
		normalizeClientValue(list),
		normalizeClientValue([avatar, resume]),
	]
	assert.deepEqual(values.map(value => value.kind), ["file", "files", "files", "files"])
	assert.equal(values[0].value.name, "avatar.png")
	for (const value of values.slice(1)) assert.deepEqual(value.value.map(file => file.name), ["avatar.png", "resume.pdf"])
	list.pop()
	assert.deepEqual(values[1].value.map(file => file.name), ["avatar.png", "resume.pdf"])
	assert.deepEqual(normalizeClientValue(new Input({ type: "file", multiple: true, files: new FileList() })), { kind: "files", value: [] })
})

test("invalid or non-value controls fail explicitly", () => {
	for (const value of [
		new Input({ type: "number", valueAsNumber: Number.NaN }),
		new Input({ type: "date", valueAsDate: null }),
		new Input({ type: "datetime-local", value: "" }),
		new Input({ type: "file", files: new FileList() }),
		new Input({ type: "submit" }),
		new Button(),
		new Form(),
	]) assert.throws(() => normalizeClientValue(value), GoxClientValueError)
})
