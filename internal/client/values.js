export const ClientValueKind = Object.freeze({
	NULL: "null",
	TEXT: "text",
	NUMBER: "number",
	BOOLEAN: "boolean",
	DATE: "date",
	JSON: "json",
	BINARY: "binary",
	FILE: "file",
	FILES: "files",
	BIGINT: "bigint",
	HTML: "html",
})

export class GoxClientValueError extends TypeError {
	constructor(message) {
		super(message)
		this.name = "GoxClientValueError"
	}
}

export function htmlToNode(source) {
	if (typeof document !== "object" || document === null || typeof document.createElement !== "function") {
		throw new Error("HTML responses require a DOM document")
	}
	const template = document.createElement("template")
	template.innerHTML = String(source)
	const fragment = template.content
	const significant = Array.from(fragment.childNodes).filter(node =>
		node.nodeType !== 3 || String(node.textContent ?? "").trim() !== "",
	)
	if (significant.length === 1 && typeof HTMLElement !== "undefined" && significant[0] instanceof HTMLElement) {
		return fragment.removeChild(significant[0])
	}
	return fragment
}

export function normalizeClientValue(value) {
	if (isInstance(value, "HTMLFormElement")) throw new GoxClientValueError("HTMLFormElement requires form signature normalization")
	if (isInstance(value, "HTMLButtonElement")) throw new GoxClientValueError("HTMLButtonElement cannot be used as an argument")
	if (isInstance(value, "DocumentFragment")) return normalized(ClientValueKind.HTML, serializeFragment(value))
	if (isInstance(value, "HTMLInputElement")) return normalizeInput(value)
	if (isInstance(value, "HTMLTextAreaElement")) return normalized(ClientValueKind.TEXT, value.value)
	if (isInstance(value, "HTMLSelectElement")) return normalizeSelect(value)
	if (isInstance(value, "Element")) return normalized(ClientValueKind.HTML, String(value.outerHTML ?? ""))
	if (isInstance(value, "File")) return normalized(ClientValueKind.FILE, value)
	if (isInstance(value, "FileList")) return normalized(ClientValueKind.FILES, Array.from(value))
	if (value === null) return normalized(ClientValueKind.NULL, null)
	if (typeof value === "string") return normalized(ClientValueKind.TEXT, value)
	if (typeof value === "boolean") return normalized(ClientValueKind.BOOLEAN, value)
	if (typeof value === "number") {
		if (!Number.isFinite(value)) throw new GoxClientValueError("Numeric arguments must be finite")
		return normalized(ClientValueKind.NUMBER, value)
	}
	if (typeof value === "bigint") return normalized(ClientValueKind.BIGINT, value)
	if (value instanceof Date) {
		if (Number.isNaN(value.getTime())) throw new GoxClientValueError("Date arguments must be valid")
		return normalized(ClientValueKind.DATE, new Date(value.getTime()))
	}
	if (value instanceof ArrayBuffer || ArrayBuffer.isView(value) || isInstance(value, "Blob")) {
		return normalized(ClientValueKind.BINARY, value)
	}
	if (Array.isArray(value) && value.length > 0 && value.every(item => isInstance(item, "File"))) {
		return normalized(ClientValueKind.FILES, value.slice())
	}
	if (typeof value === "object") return normalized(ClientValueKind.JSON, value)
	throw new GoxClientValueError(`Arguments of type ${typeof value} are not supported`)
}

function serializeFragment(fragment) {
	let html = ""
	for (const child of Array.from(fragment.childNodes ?? [])) {
		if (typeof child.outerHTML === "string") html += child.outerHTML
		else if (typeof child.textContent === "string") html += escapeHTMLText(child.textContent)
	}
	return html
}

function escapeHTMLText(value) {
	return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
}

export function normalizeClientValues(values) {
	return Array.from(values, normalizeClientValue)
}

function normalizeInput(input) {
	const type = String(input.type || "text").toLowerCase()
	if (type === "file") {
		const files = input.files === null || input.files === undefined ? [] : Array.from(input.files)
		if (input.multiple) return normalized(ClientValueKind.FILES, files)
		if (files.length === 0) throw new GoxClientValueError("File input does not contain a selected file")
		return normalized(ClientValueKind.FILE, files[0])
	}
	if (type === "number" || type === "range") {
		if (!Number.isFinite(input.valueAsNumber)) throw new GoxClientValueError(`${type} input does not contain a valid number`)
		return normalized(ClientValueKind.NUMBER, input.valueAsNumber)
	}
	if (type === "checkbox" || type === "radio") return normalized(ClientValueKind.BOOLEAN, Boolean(input.checked))
	if (type === "date") {
		const date = input.valueAsDate
		if (!(date instanceof Date) || Number.isNaN(date.getTime())) throw new GoxClientValueError("date input does not contain a valid date")
		return normalized(ClientValueKind.DATE, new Date(date.getTime()))
	}
	if (type === "datetime-local") {
		if (typeof input.value !== "string" || input.value === "") throw new GoxClientValueError("datetime-local input does not contain a value")
		const date = new Date(input.value)
		if (Number.isNaN(date.getTime())) throw new GoxClientValueError("datetime-local input does not contain a valid date")
		return normalized(ClientValueKind.DATE, date)
	}
	if (["button", "submit", "reset", "image"].includes(type)) throw new GoxClientValueError(`${type} input cannot be used as an argument`)
	return normalized(ClientValueKind.TEXT, String(input.value ?? ""))
}

function normalizeSelect(select) {
	if (!select.multiple) return normalized(ClientValueKind.TEXT, String(select.value ?? ""))
	const options = select.selectedOptions === undefined || select.selectedOptions === null ? [] : Array.from(select.selectedOptions)
	return normalized(ClientValueKind.JSON, options.map(option => String(option.value)))
}

function normalized(kind, value) { return { kind, value } }

function isInstance(value, globalName) {
	const constructor = globalThis[globalName]
	return typeof constructor === "function" && value instanceof constructor
}
