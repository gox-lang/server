import assert from "node:assert/strict"
import test from "node:test"

import {
	ChannelState,
	GoxChannelError,
	GoxProtocol,
	GoxSocketError,
	GoxSocketErrorCode,
	GoxSocketErrorPhase,
	Socket,
	WSCloseCode,
	decodeCompactUint,
	decodeGoxPacket,
	decodeFilesReference,
	encodeGoxPacket,
	encodeCompactUint,
	encodeFileReference,
	encodeFilesReference,
} from "./client.js"

test("session lifecycle close codes and errors are part of the public client API", () => {
	assert.equal(WSCloseCode.SESSION_EXPIRED, 4002)
	assert.equal(WSCloseCode.SESSION_DESTROYED, 4003)
	assert.equal(GoxSocketErrorCode.SESSION_SAVE_FAILED, "SESSION_SAVE_FAILED")
	assert.equal(GoxSocketErrorCode.SESSION_TOUCH_FAILED, "SESSION_TOUCH_FAILED")
	assert.equal(GoxSocketErrorCode.SESSION_EXPIRED, "SESSION_EXPIRED")
	assert.equal(GoxSocketErrorPhase.SESSION, "session")
})

class MockWebSocket {
	static instances = []

	constructor(url) {
		this.url = url
		this.binaryType = ""
		this.listeners = new Map()
		this.closeCalls = []
		this.sendCalls = []
		MockWebSocket.instances.push(this)
	}

	addEventListener(event, handler) {
		this.listeners.set(event, handler)
	}

	dispatch(event, value = {}) {
		this.listeners.get(event)?.(value)
	}

	close(code, reason) {
		this.closeCalls.push({ code, reason })
	}

	send(data) {
		this.sendCalls.push(data)
	}
}

class MockInput {
	constructor(values = {}) {
		Object.assign(this, { type: "text", value: "", multiple: false, files: null }, values)
	}
}

class MockSelect {
	constructor(values = {}) {
		Object.assign(this, { multiple: false, value: "", selectedOptions: [] }, values)
	}
}

class MockFile {
	constructor(partsOrName, nameOrType = "application/octet-stream", options = {}) {
		if (typeof partsOrName === "string") {
			this.name = partsOrName
			this.type = nameOrType
			this.bytes = new Uint8Array()
		} else {
			this.name = nameOrType
			this.type = options.type ?? ""
			const parts = partsOrName.map(part => part instanceof Uint8Array ? part : new Uint8Array(part))
			this.bytes = Uint8Array.from(parts.flatMap(part => Array.from(part)))
		}
		this.size = this.bytes.length
	}

	async arrayBuffer() { return this.bytes.slice().buffer }
}

class MockFileList extends Array {}

class MockForm {
	constructor(fields) {
		this.fields = fields
		this.elements = { namedItem: name => this.fields.get(name) ?? null }
	}
}

class MockNode {
	constructor(nodeType, textContent = "") {
		this.nodeType = nodeType
		this.textContent = textContent
	}
}

class MockHTMLElement extends MockNode {
	constructor(tagName, textContent = "", outerHTML = "") {
		super(1, textContent)
		this.tagName = tagName.toUpperCase()
		this.outerHTML = outerHTML || `<${tagName}>${textContent}</${tagName}>`
	}
}

class MockDocumentFragment extends MockNode {
	constructor(nodes = []) {
		super(11, "")
		this.childNodes = nodes
	}
	removeChild(node) {
		const index = this.childNodes.indexOf(node)
		if (index < 0) throw new Error("node not found")
		this.childNodes.splice(index, 1)
		return node
	}
}

class MockTemplate {
	constructor() { this.content = new MockDocumentFragment() }
	set innerHTML(source) {
		const nodes = []
		const pattern = /<([a-z][a-z0-9-]*)\b[^>]*>([\s\S]*?)<\/\1>/gi
		let cursor = 0
		let match
		while ((match = pattern.exec(source)) !== null) {
			if (match.index > cursor) nodes.push(new MockNode(3, source.slice(cursor, match.index)))
			nodes.push(new MockHTMLElement(match[1], match[2].replace(/<[^>]+>/g, ""), match[0]))
			cursor = pattern.lastIndex
		}
		if (cursor < source.length) nodes.push(new MockNode(3, source.slice(cursor)))
		this.content = new MockDocumentFragment(nodes)
	}
}

class MockElement {
	constructor(outerHTML) { this.outerHTML = outerHTML }
}

function installBrowser(origin = "http://localhost:3000") {
	MockWebSocket.instances = []
	globalThis.WebSocket = MockWebSocket
	globalThis.location = { origin }
	globalThis.HTMLInputElement = MockInput
	globalThis.HTMLSelectElement = MockSelect
	globalThis.File = MockFile
	globalThis.FileList = MockFileList
	globalThis.HTMLFormElement = MockForm
	globalThis.Element = MockElement
	globalThis.HTMLElement = MockHTMLElement
	globalThis.DocumentFragment = MockDocumentFragment
	globalThis.document = {
		createElement(name) {
			assert.equal(name, "template")
			return new MockTemplate()
		},
	}
}

async function answerSignature(native, parameters = [], hasReply = false) {
	const request = decodeGoxPacket(native.sendCalls.at(-1))
	const requestID = decodeCompactUint(request.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID).value).value
	const response = encodeGoxPacket({
		type: GoxProtocol.PacketType.RESPONSE,
		flags: GoxProtocol.Flag.INTERNAL,
		headers: [{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) }],
		arguments: [{
			type: GoxProtocol.ArgumentType.JSON,
			data: new TextEncoder().encode(JSON.stringify({ parameters, hasReply })),
		}],
	})
	native.dispatch("message", { data: response.buffer })
	await new Promise(resolve => setTimeout(resolve, 0))
}

function channelControl(type, id, reason = "") {
	const headers = [{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode(id) }]
	if (reason !== "") headers.push({ type: GoxProtocol.HeaderType.CHANNEL_REASON, value: new TextEncoder().encode(reason) })
	return encodeGoxPacket({ type, headers })
}

test("Socket connects immediately to the fixed handshake path", () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]

	assert.equal(socket.state, "connecting")
	assert.equal(native.url, "ws://localhost:3000/_gox/websocket/handshake")
	assert.equal(native.binaryType, "arraybuffer")
})

test("new socket.Channel sends one typed JOIN and accepts an independent lifecycle", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat#room", "token", 42)
	const connected = []
	channel.when("connected", isReconnect => connected.push(isReconnect))
	assert.equal(channel.id, "Chat#room")
	assert.equal(channel.name, "Chat")
	assert.equal(channel.key, "room")
	assert.equal(channel.connectionState, ChannelState.CONNECTING)
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	const join = decodeGoxPacket(native.sendCalls[0])
	assert.equal(join.type, GoxProtocol.PacketType.CHANNEL_JOIN)
	assert.equal(new TextDecoder().decode(join.headers[0].value), "Chat#room")
	assert.deepEqual(join.arguments.map(argument => argument.type), [GoxProtocol.ArgumentType.TEXT, GoxProtocol.ArgumentType.NUMBER])
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat#room").buffer })
	assert.equal(channel.connectionState, ChannelState.CONNECTED)
	assert.deepEqual(connected, [false])
})

test("Channel rejection has no reason and releases its identifier", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const first = new socket.Channel("Private")
	let rejectedArguments
	first.when("rejected", (...values) => { rejectedArguments = values })
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_REJECTED, "Private").buffer })
	assert.equal(first.connectionState, ChannelState.REJECTED)
	assert.deepEqual(rejectedArguments, [])
	assert.doesNotThrow(() => new socket.Channel("Private"))
})

test("Channel setup failures report Channel error before rejection", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat#broken")
	const lifecycle = []
	channel.when("error", error => lifecycle.push({ event: "error", error }))
	channel.when("rejected", () => lifecycle.push({ event: "rejected" }))
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	const failure = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_ERROR,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("validate") },
			{ type: GoxProtocol.HeaderType.ERROR_CODE, value: new TextEncoder().encode("HANDLER_FAILED") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(1n) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode("Chat#broken") },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.TEXT, data: new TextEncoder().encode("channel definition failed to compile") }],
	})
	native.dispatch("message", { data: failure.buffer })
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_REJECTED, "Chat#broken").buffer })
	assert.deepEqual(lifecycle.map(item => item.event), ["error", "rejected"])
	assert.equal(lifecycle[0].error instanceof GoxChannelError, true)
	assert.equal(lifecycle[0].error.channelID, "Chat#broken")
	assert.equal(lifecycle[0].error.event, "validate")
	assert.equal(lifecycle[0].error.message, "channel definition failed to compile")
	assert.equal(channel.connectionState, ChannelState.REJECTED)
})

test("Channel leave waits for the server acknowledgement and reports its reason", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat")
	let reason
	channel.when("disconnected", value => { reason = value })
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat").buffer })
	assert.equal(channel.leave(), channel)
	assert.equal(channel.connectionState, ChannelState.DISCONNECTING)
	const leave = decodeGoxPacket(native.sendCalls.at(-1))
	assert.equal(leave.type, GoxProtocol.PacketType.CHANNEL_LEAVE)
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_LEAVE, "Chat", "left").buffer })
	assert.equal(channel.connectionState, ChannelState.DISCONNECTED)
	assert.equal(reason, "left")
})

test("connected Channels revalidate after Socket reconnection", async () => {
	installBrowser()
	const socket = new Socket({ reconnect: { delay: 0, maxDelay: 0 } })
	const first = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat#room", "token")
	const connections = []
	channel.when("connected", value => connections.push(value))
	first.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	first.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat#room").buffer })
	first.dispatch("close", { code: WSCloseCode.RESTART, reason: "restart", wasClean: true })
	assert.equal(channel.connectionState, ChannelState.RECONNECTING)
	await new Promise(resolve => setTimeout(resolve, 0))
	const second = MockWebSocket.instances[1]
	second.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	const rejoin = decodeGoxPacket(second.sendCalls[0])
	assert.equal(rejoin.type, GoxProtocol.PacketType.CHANNEL_JOIN)
	second.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat#room").buffer })
	assert.deepEqual(connections, [false, true])
})

test("Channel IDs and duplicate instances are rejected locally", () => {
	installBrowser()
	const socket = new Socket()
	for (const id of ["", " Chat", "Chat ", "#room", "Chat#", "Chat#one#two", "Chat\nroom"]) {
		assert.throws(() => new socket.Channel(id), TypeError)
	}
	new socket.Channel("Chat")
	assert.throws(() => new socket.Channel("Chat"), TypeError)
})

test("Channel emit and on remain isolated from Socket events with the same name", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat#room")
	const received = []
	socket.on("message", value => received.push(`socket:${value}`))
	channel.on("message", value => received.push(`channel:${value}`))
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat#room").buffer })
	const emission = channel.emit("message", "from-client")
	assert.equal(typeof emission.await, "function")
	await new Promise(resolve => setTimeout(resolve, 0))
	const outgoing = decodeGoxPacket(native.sendCalls.at(-1))
	assert.equal(outgoing.type, GoxProtocol.PacketType.CHANNEL_EVENT)
	assert.equal(new TextDecoder().decode(outgoing.headers.at(-1).value), "Chat#room")
	assert.equal(new TextDecoder().decode(outgoing.arguments[0].data), "from-client")

	const incoming = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("message") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(9) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode("Chat#room") },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.TEXT, data: new TextEncoder().encode("from-server") }],
	})
	native.dispatch("message", { data: incoming.buffer })
	assert.deepEqual(received, ["channel:from-server"])
})

test("missing Channel handlers report only through Channel error and return correlated CHANNEL_ERROR", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat")
	const channelErrors = []
	const socketErrors = []
	channel.when("error", error => channelErrors.push(error))
	socket.when("error", error => socketErrors.push(error))
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat").buffer })
	const event = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("missing") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(17) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode("Chat") },
		],
		arguments: [],
	})
	native.dispatch("message", { data: event.buffer })
	assert.equal(channelErrors.length, 1)
	assert.ok(channelErrors[0] instanceof GoxChannelError)
	assert.equal(channelErrors[0].channelID, "Chat")
	assert.equal(socketErrors.length, 0)
	const response = decodeGoxPacket(native.sendCalls.at(-1))
	assert.equal(response.type, GoxProtocol.PacketType.CHANNEL_ERROR)
	assert.equal(decodeCompactUint(response.headers.find(header => header.type === GoxProtocol.HeaderType.EVENT_ID).value).value, 17n)
	assert.equal(response.arguments[0].type, GoxProtocol.ArgumentType.TEXT)
})

test("remote Channel event failures dispatch Channel error without touching Socket error", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat")
	let received
	let socketErrors = 0
	channel.when("error", error => { received = error })
	socket.when("error", () => { socketErrors++ })
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat").buffer })
	const packet = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_ERROR,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("message") },
			{ type: GoxProtocol.HeaderType.ERROR_CODE, value: new TextEncoder().encode("HANDLER_FAILED") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(5) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode("Chat") },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.TEXT, data: new TextEncoder().encode("failed") }],
	})
	native.dispatch("message", { data: packet.buffer })
	assert.ok(received instanceof GoxChannelError)
	assert.equal(received.message, "failed")
	assert.equal(received.remote, true)
	assert.equal(socketErrors, 0)
})

test("channel.emit(...).await() sends one CHANNEL_REQUEST and resolves its directed response", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat#room")
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat#room").buffer })
	const responsePromise = channel.emit("profile", 42).await()
	await new Promise(resolve => setTimeout(resolve, 0))
	const request = decodeGoxPacket(native.sendCalls.at(-1))
	assert.equal(request.type, GoxProtocol.PacketType.CHANNEL_REQUEST)
	const requestID = decodeCompactUint(request.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID).value).value
	const response = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_RESPONSE,
		headers: [
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode("Chat#room") },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode('{"id":42}') }],
	})
	native.dispatch("message", { data: response.buffer })
	assert.deepEqual(await responsePromise, { id: 42 })
})

test("Channel request errors resolve undefined and belong only to Channel error", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat")
	const errors = []
	let socketErrors = 0
	channel.when("error", error => errors.push(error))
	socket.when("error", () => { socketErrors++ })
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat").buffer })
	const responsePromise = channel.emit("profile").await()
	await new Promise(resolve => setTimeout(resolve, 0))
	const request = decodeGoxPacket(native.sendCalls.at(-1))
	const requestID = decodeCompactUint(request.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID).value).value
	const response = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_ERROR,
		headers: [
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) },
			{ type: GoxProtocol.HeaderType.ERROR_CODE, value: new TextEncoder().encode("HANDLER_FAILED") },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode("Chat") },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.TEXT, data: new TextEncoder().encode("failed") }],
	})
	native.dispatch("message", { data: response.buffer })
	assert.equal(await responsePromise, undefined)
	assert.equal(errors.length, 1)
	assert.equal(errors[0].message, "failed")
	assert.equal(socketErrors, 0)
})

test("Channel leave cancels pending Await and safely discards its late response", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat")
	const errors = []
	channel.when("error", error => errors.push(error))
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat").buffer })
	const responsePromise = channel.emit("slow").await()
	await new Promise(resolve => setTimeout(resolve, 0))
	const request = decodeGoxPacket(native.sendCalls.at(-1))
	const requestID = decodeCompactUint(request.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID).value).value
	channel.leave()
	assert.equal(await responsePromise, undefined)
	assert.equal(errors.at(-1).code, "REQUEST_INTERRUPTED")
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_LEAVE, "Chat", "left").buffer })
	const late = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_RESPONSE,
		headers: [
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode("Chat") },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.NULL, data: new Uint8Array() }],
	})
	native.dispatch("message", { data: late.buffer })
	assert.equal(native.closeCalls.length, 0)
})

test("Channel Await timeout cleans its request and reports one Channel error", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const channel = new socket.Channel("Chat")
	const errors = []
	channel.when("error", error => errors.push(error))
	native.dispatch("open")
	await new Promise(resolve => setTimeout(resolve, 0))
	native.dispatch("message", { data: channelControl(GoxProtocol.PacketType.CHANNEL_ACCEPTED, "Chat").buffer })
	assert.equal(await channel.emit("slow").await(0), undefined)
	assert.equal(errors.length, 1)
	assert.equal(errors[0].code, "REQUEST_TIMEOUT")
})

test("Socket origin changes only the connection origin", () => {
	installBrowser()
	new Socket({ origin: "https://api.example.com/application/path" })
	assert.equal(MockWebSocket.instances[0].url, "wss://api.example.com/_gox/websocket/handshake")
})

test("Socket state is readonly", () => {
	installBrowser()
	const socket = new Socket()

	assert.throws(() => {
		socket.state = "custom"
	}, TypeError)
	assert.equal(socket.state, "connecting")
	assert.equal(socket.connectionState, undefined)
})

test("when dispatches connect and replaces a handler with the same name", () => {
	installBrowser()
	const socket = new Socket()
	const values = []
	socket.when("connect", () => values.push("first"))
	socket.when("connect", isReconnect => values.push(isReconnect))

	MockWebSocket.instances[0].dispatch("open")
	assert.equal(socket.state, "connected")
	assert.deepEqual(values, [false])
})

test("disconnect lifecycle receives a GOX disconnect reason", () => {
	installBrowser()
	const socket = new Socket()
	const closeEvent = { code: 1000, reason: "done", wasClean: true }
	let received
	socket.when("disconnect", event => {
		received = event
	})

	MockWebSocket.instances[0].dispatch("close", closeEvent)
	assert.equal(socket.state, "disconnected")
	assert.deepEqual(received, {
		code: 1000,
		reason: "done",
		clean: true,
		initiator: "server",
		willReconnect: false,
		nativeEvent: closeEvent,
	})
})

test("a rejected native handshake becomes error then disconnected", () => {
	installBrowser()
	const socket = new Socket()
	const events = []
	socket.when("error", event => events.push(["error", event]))
	socket.when("disconnect", event => events.push(["disconnect", event]))

	const native = MockWebSocket.instances[0]
	const errorEvent = { type: "error" }
	const closeEvent = { code: 1006, reason: "", wasClean: false }
	native.dispatch("error", errorEvent)
	assert.equal(socket.state, "connecting")
	native.dispatch("close", closeEvent)

	assert.equal(socket.state, "disconnected")
	assert.equal(events[0][0], "error")
	assert.ok(events[0][1] instanceof GoxSocketError)
	assert.equal(events[0][1].code, GoxSocketErrorCode.HANDSHAKE_FAILED)
	assert.equal(events[0][1].phase, GoxSocketErrorPhase.HANDSHAKE)
	assert.equal(events[0][1].nativeEvent, errorEvent)
	assert.equal(events[1][0], "disconnect")
	assert.equal(events[1][1].code, 1006)
	assert.equal(events[1][1].initiator, "unknown")
	assert.equal(events[1][1].willReconnect, false)
})

test("Socket reconnects by default and marks the next upgrade", async () => {
	installBrowser()
	const socket = new Socket({ reconnect: { delay: 0, maxDelay: 0 } })
	const connections = []
	socket.when("connect", isReconnect => connections.push(isReconnect))
	const first = MockWebSocket.instances[0]
	first.dispatch("open")
	first.dispatch("close", { code: 1012, reason: "restart", wasClean: true })

	assert.equal(socket.state, "reconnecting")
	await new Promise(resolve => setTimeout(resolve, 0))
	const second = MockWebSocket.instances[1]
	assert.ok(second.url.endsWith("/_gox/websocket/handshake?_gox_reconnect=1"))
	second.dispatch("open")
	assert.equal(socket.state, "connected")
	assert.deepEqual(connections, [false, true])
})

test("reconnect false disables automatic reconnection", async () => {
	installBrowser()
	const socket = new Socket({ reconnect: false })
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	native.dispatch("close", { code: 1006, reason: "", wasClean: false })
	await new Promise(resolve => setTimeout(resolve, 0))
	assert.equal(socket.state, "disconnected")
	assert.equal(MockWebSocket.instances.length, 1)
})

test("disconnect is deliberate and never reconnects", async () => {
	installBrowser()
	const socket = new Socket({ reconnect: { delay: 0, maxDelay: 0 } })
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	assert.equal(socket.disconnect(0), socket)
	assert.deepEqual(native.closeCalls, [{ code: 1000, reason: "Normal closure" }])
	native.dispatch("close", { code: 1000, reason: "Normal closure", wasClean: true })
	await new Promise(resolve => setTimeout(resolve, 0))
	assert.equal(MockWebSocket.instances.length, 1)
})

test("disconnect default, zero and NORMAL use the same wire close", () => {
	for (const invoke of [
		socket => socket.disconnect(),
		socket => socket.disconnect(0),
		socket => socket.disconnect(WSCloseCode.NORMAL),
	]) {
		installBrowser()
		const socket = new Socket({ reconnect: false })
		const native = MockWebSocket.instances[0]
		invoke(socket)
		assert.deepEqual(native.closeCalls, [{ code: 1000, reason: "Normal closure" }])
	}
})

test("reconnect configuration is validated", () => {
	installBrowser()
	assert.throws(() => new Socket({ reconnect: { enabled: "yes" } }), TypeError)
	assert.throws(() => new Socket({ reconnect: { delay: 10, maxDelay: 5 } }), TypeError)
	assert.throws(() => new Socket({ reconnect: { attempts: -1 } }), TypeError)
})

test("lifecycle handler errors dispatch the error lifecycle", () => {
	installBrowser()
	const socket = new Socket()
	const expected = new Error("connect failed")
	let received
	socket.when("error", error => {
		received = error
	})
	socket.when("connect", () => {
		throw expected
	})

	MockWebSocket.instances[0].dispatch("open")
	assert.ok(received instanceof GoxSocketError)
	assert.equal(received.code, GoxSocketErrorCode.HANDLER_FAILED)
	assert.equal(received.phase, GoxSocketErrorPhase.HANDLER)
	assert.equal(received.event, "connect")
	assert.equal(received.cause, expected)
})

test("native errors after connecting become transport errors", () => {
	installBrowser()
	const socket = new Socket()
	let received
	socket.when("error", error => {
		received = error
	})
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	native.dispatch("error", { type: "error" })

	assert.ok(received instanceof GoxSocketError)
	assert.equal(received.code, GoxSocketErrorCode.TRANSPORT_ERROR)
	assert.equal(received.phase, GoxSocketErrorPhase.TRANSPORT)
	assert.equal(received.retryable, true)
})

test("when and on validate registration and return the socket", () => {
	installBrowser()
	const socket = new Socket()
	assert.equal(socket.when("connect", () => {}), socket)
	assert.equal(socket.on("message", () => {}), socket)
	assert.throws(() => socket.when("", () => {}), TypeError)
	assert.throws(() => socket.on("message", null), TypeError)
})

test("emit queues before connect and sends one binary EVENT after its signature", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	socket.emit("user", "Javier", 42, true, 18446744073709551616n)
	assert.equal(native.sendCalls.length, 0)

	native.dispatch("open")
	assert.equal(native.sendCalls.length, 1)
	await answerSignature(native)
	assert.equal(native.sendCalls.length, 2)
	assert.ok(native.sendCalls[1] instanceof Uint8Array)
	const packet = decodeGoxPacket(native.sendCalls[1])
	assert.equal(packet.type, GoxProtocol.PacketType.EVENT)
	assert.equal(new TextDecoder().decode(packet.headers[0].value), "user")
	assert.deepEqual(packet.arguments.map(argument => argument.type), [
		GoxProtocol.ArgumentType.TEXT,
		GoxProtocol.ArgumentType.NUMBER,
		GoxProtocol.ArgumentType.BOOLEAN,
		GoxProtocol.ArgumentType.BIGINT,
	])
})

test("emit accepts DOM controls and encodes their semantic values", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const date = new Date("2026-07-15T00:00:00.000Z")
	const localDateTime = "2026-07-15T10:30"
	socket.emit(
		"user",
		new MockInput({ value: "Javier" }),
		new MockInput({ type: "number", valueAsNumber: 42 }),
		new MockInput({ type: "checkbox", checked: true }),
		new MockInput({ type: "date", valueAsDate: date }),
		new MockInput({ type: "datetime-local", value: localDateTime }),
		new MockSelect({ multiple: true, selectedOptions: [{ value: "read" }, { value: "write" }] }),
	)
	native.dispatch("open")
	await answerSignature(native)
	const packet = decodeGoxPacket(native.sendCalls[1])
	assert.deepEqual(packet.arguments.map(argument => argument.type), [
		GoxProtocol.ArgumentType.TEXT,
		GoxProtocol.ArgumentType.NUMBER,
		GoxProtocol.ArgumentType.BOOLEAN,
		GoxProtocol.ArgumentType.DATE,
		GoxProtocol.ArgumentType.DATE,
		GoxProtocol.ArgumentType.JSON,
	])
	const text = new TextDecoder()
	assert.equal(text.decode(packet.arguments[0].data), "Javier")
	assert.equal(text.decode(packet.arguments[1].data), "42")
	assert.equal(text.decode(packet.arguments[3].data), date.toISOString())
	assert.equal(text.decode(packet.arguments[4].data), new Date(localDateTime).toISOString())
	assert.deepEqual(JSON.parse(text.decode(packet.arguments[5].data)), ["read", "write"])
})

test("HTMLElement arguments remain typed HTML and server HTML becomes a DOM node", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	let received
	socket.on("content", value => { received = value })
	native.dispatch("open")
	socket.emit("content", new MockElement("<article>Hola</article>"))
	await answerSignature(native)
	const outgoing = decodeGoxPacket(native.sendCalls[1])
	assert.equal(outgoing.arguments[0].type, GoxProtocol.ArgumentType.HTML)
	assert.equal(new TextDecoder().decode(outgoing.arguments[0].data), "<article>Hola</article>")

	const incoming = encodeGoxPacket({
		type: GoxProtocol.PacketType.EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("content") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(9) },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.HTML, data: new TextEncoder().encode("<h1>GOX</h1>") }],
	})
	native.dispatch("message", { data: incoming.buffer })
	assert.ok(received instanceof HTMLElement)
	assert.equal(received.tagName, "H1")
	assert.equal(received.textContent, "GOX")
})

test("server HTML with multiple roots becomes a DocumentFragment", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	let received
	socket.on("content", value => { received = value })
	native.dispatch("open")
	const incoming = encodeGoxPacket({
		type: GoxProtocol.PacketType.EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("content") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(10) },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.HTML, data: new TextEncoder().encode("<header>Top</header><main>Body</main>") }],
	})
	native.dispatch("message", { data: incoming.buffer })
	assert.ok(received instanceof DocumentFragment)
	assert.deepEqual(received.childNodes.map(node => node.tagName), ["HEADER", "MAIN"])
})

test("invalid DOM values fail locally", async () => {
	installBrowser()
	const socket = new Socket()
	const errors = []
	socket.when("error", error => errors.push(error))
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	socket.emit("invalid-number", new MockInput({ type: "number", valueAsNumber: Number.NaN }))
	await answerSignature(native)
	assert.equal(errors.length, 1)
	assert.equal(errors[0].code, GoxSocketErrorCode.ENCODE_FAILED)
	assert.match(errors[0].cause.message, /valid number/)
})

test("FILE and FILES emit transfer packets before their EVENT", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const avatar = new MockFile([Uint8Array.of(1, 2, 3)], "avatar.png", { type: "image/png" })
	const resume = new MockFile([Uint8Array.of(4, 5)], "resume.pdf", { type: "application/pdf" })
	native.dispatch("open")
	socket.emit("upload", avatar, new MockFileList(avatar, resume))
	await new Promise(resolve => setTimeout(resolve, 0))
	await answerSignature(native)
	const packets = native.sendCalls.slice(1).map(decodeGoxPacket)
	assert.deepEqual(packets.map(packet => packet.type), [
		GoxProtocol.PacketType.TRANSFER_BEGIN, GoxProtocol.PacketType.TRANSFER_CHUNK, GoxProtocol.PacketType.TRANSFER_END,
		GoxProtocol.PacketType.TRANSFER_BEGIN, GoxProtocol.PacketType.TRANSFER_CHUNK, GoxProtocol.PacketType.TRANSFER_END,
		GoxProtocol.PacketType.TRANSFER_BEGIN, GoxProtocol.PacketType.TRANSFER_CHUNK, GoxProtocol.PacketType.TRANSFER_END,
		GoxProtocol.PacketType.EVENT,
	])
	const event = packets.at(-1)
	assert.equal(event.arguments[0].type, GoxProtocol.ArgumentType.FILE)
	assert.equal(event.arguments[1].type, GoxProtocol.ArgumentType.FILES)
	assert.deepEqual(decodeFilesReference(event.arguments[1].data), [2n, 3n])
	assert.equal(new TextDecoder().decode(packets[0].headers[2].value), "avatar.png")
	assert.deepEqual(packets[1].payload, Uint8Array.of(1, 2, 3))
})

test("emit awaits the single EventReply declared by the Go signature", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	const replyPromise = socket.emit("profile", "profile-42")
	await new Promise(resolve => setTimeout(resolve, 0))
	await answerSignature(native, ["profileID"], true)
	const request = decodeGoxPacket(native.sendCalls[1])
	assert.equal(request.type, GoxProtocol.PacketType.REQUEST)
	assert.equal(new TextDecoder().decode(request.arguments[0].data), "profile-42")
	const requestID = decodeCompactUint(request.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID).value).value
	const response = encodeGoxPacket({
		type: GoxProtocol.PacketType.RESPONSE,
		headers: [{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) }],
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode('{"id":"profile-42","name":"Javier"}') }],
	})
	native.dispatch("message", { data: response.buffer })
	assert.deepEqual(await replyPromise, { id: "profile-42", name: "Javier" })
})

test("EventReply operational errors go to when error and resolve emit undefined", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const errors = []
	socket.when("error", error => errors.push(error))
	native.dispatch("open")
	const replyPromise = socket.emit("profile", "missing")
	await new Promise(resolve => setTimeout(resolve, 0))
	await answerSignature(native, ["profileID"], true)
	const request = decodeGoxPacket(native.sendCalls[1])
	const requestID = decodeCompactUint(request.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID).value).value
	const packet = encodeGoxPacket({
		type: GoxProtocol.PacketType.ERROR,
		headers: [
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) },
			{ type: GoxProtocol.HeaderType.ERROR_CODE, value: new TextEncoder().encode("HANDLER_FAILED") },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode('{"message":"profile handler failed","argument":-1}') }],
	})
	native.dispatch("message", { data: packet.buffer })
	assert.equal(await replyPromise, undefined)
	assert.equal(errors.length, 1)
	assert.equal(errors[0].code, "HANDLER_FAILED")
})

test("EventReply resolves a transferred File with an initially unknown size", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	const replyPromise = socket.emit("report", 7)
	await new Promise(resolve => setTimeout(resolve, 0))
	await answerSignature(native, ["reportID"], true)
	const request = decodeGoxPacket(native.sendCalls[1])
	const requestID = decodeCompactUint(request.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID).value).value
	const H = GoxProtocol.HeaderType
	const P = GoxProtocol.PacketType
	const encoder = new TextEncoder()
	for (const packet of [
		encodeGoxPacket({ type: P.TRANSFER_BEGIN, headers: [
			{ type: H.TRANSFER_ID, value: encodeCompactUint(21) },
			{ type: H.TOTAL_SIZE, value: encodeCompactUint(Number.MAX_SAFE_INTEGER) },
			{ type: H.FILE_NAME, value: encoder.encode("report.pdf") },
			{ type: H.MIME_TYPE, value: encoder.encode("application/pdf") },
		] }),
		encodeGoxPacket({ type: P.TRANSFER_CHUNK, headers: [
			{ type: H.TRANSFER_ID, value: encodeCompactUint(21) },
			{ type: H.SEQUENCE, value: encodeCompactUint(0) },
		], payload: Uint8Array.of(1, 2, 3) }),
		encodeGoxPacket({ type: P.TRANSFER_END, headers: [
			{ type: H.TRANSFER_ID, value: encodeCompactUint(21) },
			{ type: H.TOTAL_SIZE, value: encodeCompactUint(3) },
		] }),
	]) native.dispatch("message", { data: packet.buffer })
	const response = encodeGoxPacket({
		type: P.RESPONSE,
		headers: [{ type: H.REQUEST_ID, value: encodeCompactUint(requestID) }],
		arguments: [{ type: GoxProtocol.ArgumentType.FILE, data: encodeFileReference(21) }],
	})
	native.dispatch("message", { data: response.buffer })
	const file = await replyPromise
	assert.equal(file.name, "report.pdf")
	assert.equal(file.type, "application/pdf")
	assert.deepEqual(file.bytes, Uint8Array.of(1, 2, 3))
})

test("JavaScript on return sends one correlated RESPONSE to Go", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	socket.on("get-theme", mode => ({ name: mode, contrast: "high" }))
	native.dispatch("open")
	const request = encodeGoxPacket({
		type: GoxProtocol.PacketType.REQUEST,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("get-theme") },
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(31) },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.TEXT, data: new TextEncoder().encode("dark") }],
	})
	native.dispatch("message", { data: request.buffer })
	await new Promise(resolve => setTimeout(resolve, 0))
	const response = decodeGoxPacket(native.sendCalls[0])
	assert.equal(response.type, GoxProtocol.PacketType.RESPONSE)
	assert.equal(decodeCompactUint(response.headers[0].value).value, 31n)
	assert.deepEqual(JSON.parse(new TextDecoder().decode(response.arguments[0].data)), { name: "dark", contrast: "high" })
})

test("JavaScript async on return may reply with a File", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	socket.on("report", async () => new MockFile([Uint8Array.of(4, 5)], "report.pdf", { type: "application/pdf" }))
	native.dispatch("open")
	const request = encodeGoxPacket({
		type: GoxProtocol.PacketType.REQUEST,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("report") },
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(32) },
		],
		arguments: [],
	})
	native.dispatch("message", { data: request.buffer })
	await new Promise(resolve => setTimeout(resolve, 0))
	const packets = native.sendCalls.map(decodeGoxPacket)
	assert.deepEqual(packets.map(packet => packet.type), [
		GoxProtocol.PacketType.TRANSFER_BEGIN,
		GoxProtocol.PacketType.TRANSFER_CHUNK,
		GoxProtocol.PacketType.TRANSFER_END,
		GoxProtocol.PacketType.RESPONSE,
	])
	assert.equal(packets.at(-1).arguments[0].type, GoxProtocol.ArgumentType.FILE)
})

test("JavaScript on failure sends ERROR and dispatches when error", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const errors = []
	socket.when("error", error => errors.push(error))
	socket.on("explode", () => { throw new Error("boom") })
	native.dispatch("open")
	const request = encodeGoxPacket({
		type: GoxProtocol.PacketType.REQUEST,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("explode") },
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(33) },
		],
		arguments: [],
	})
	native.dispatch("message", { data: request.buffer })
	await new Promise(resolve => setTimeout(resolve, 0))
	assert.equal(errors.length, 1)
	assert.equal(errors[0].code, GoxSocketErrorCode.HANDLER_FAILED)
	const response = decodeGoxPacket(native.sendCalls[0])
	assert.equal(response.type, GoxProtocol.PacketType.ERROR)
	assert.equal(decodeCompactUint(response.headers[0].value).value, 33n)
})

test("server FILE and FILES transfers become real client File values in order", () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	let received
	socket.on("download", (...values) => { received = values })
	native.dispatch("open")
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const encoder = new TextEncoder()
	const sendFile = (id, name, type, data) => {
		for (const packet of [
			{ type: P.TRANSFER_BEGIN, headers: [
				{ type: H.TRANSFER_ID, value: encodeCompactUint(id) },
				{ type: H.TOTAL_SIZE, value: encodeCompactUint(data.length) },
				{ type: H.FILE_NAME, value: encoder.encode(name) },
				{ type: H.MIME_TYPE, value: encoder.encode(type) },
			], payload: new Uint8Array() },
			{ type: P.TRANSFER_CHUNK, headers: [
				{ type: H.TRANSFER_ID, value: encodeCompactUint(id) },
				{ type: H.SEQUENCE, value: encodeCompactUint(0) },
			], payload: data },
			{ type: P.TRANSFER_END, headers: [
				{ type: H.TRANSFER_ID, value: encodeCompactUint(id) },
				{ type: H.TOTAL_SIZE, value: encodeCompactUint(data.length) },
			], payload: new Uint8Array() },
		]) native.dispatch("message", { data: encodeGoxPacket(packet).buffer })
	}
	sendFile(11, "avatar.png", "image/png", Uint8Array.of(1, 2, 3))
	sendFile(12, "a.txt", "text/plain", Uint8Array.of(4))
	sendFile(13, "b.txt", "text/plain", Uint8Array.of(5))
	const event = encodeGoxPacket({ type: P.EVENT, headers: [
		{ type: H.EVENT_NAME, value: encoder.encode("download") },
		{ type: H.EVENT_ID, value: encodeCompactUint(9) },
	], arguments: [
		{ type: GoxProtocol.ArgumentType.FILE, data: encodeFileReference(11) },
		{ type: GoxProtocol.ArgumentType.FILES, data: encodeFilesReference([12, 13]) },
	] })
	native.dispatch("message", { data: event.buffer })
	assert.equal(received[0].name, "avatar.png")
	assert.equal(received[0].type, "image/png")
	assert.deepEqual(received[0].bytes, Uint8Array.of(1, 2, 3))
	assert.deepEqual(received[1].map(file => file.name), ["a.txt", "b.txt"])
})

test("HTMLFormElement requests its Go signature and emits only required named controls", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	const avatar = new MockFile([Uint8Array.of(7, 8)], "avatar.png", { type: "image/png" })
	const form = new MockForm(new Map([
		["name", new MockInput({ value: "Javier" })],
		["age", new MockInput({ type: "number", valueAsNumber: 42 })],
		["avatar", new MockInput({ type: "file", files: new MockFileList(avatar) })],
		["ignored", new MockInput({ value: "not sent" })],
	]))
	socket.emit("profile", form)
	assert.equal(native.sendCalls.length, 1)
	const request = decodeGoxPacket(native.sendCalls[0])
	assert.equal(request.type, GoxProtocol.PacketType.REQUEST)
	assert.equal(request.flags, GoxProtocol.Flag.INTERNAL)
	assert.equal(new TextDecoder().decode(request.arguments[0].data), "profile")
	const response = encodeGoxPacket({
		type: GoxProtocol.PacketType.RESPONSE,
		flags: GoxProtocol.Flag.INTERNAL,
		headers: [{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(1) }],
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode('{"parameters":["name","age","avatar"],"hasReply":false}') }],
	})
	native.dispatch("message", { data: response.buffer })
	await new Promise(resolve => setTimeout(resolve, 0))
	const packets = native.sendCalls.slice(1).map(decodeGoxPacket)
	const event = packets.at(-1)
	assert.equal(event.type, GoxProtocol.PacketType.EVENT)
	assert.deepEqual(event.arguments.map(argument => argument.type), [
		GoxProtocol.ArgumentType.TEXT,
		GoxProtocol.ArgumentType.NUMBER,
		GoxProtocol.ArgumentType.FILE,
	])
	assert.equal(new TextDecoder().decode(event.arguments[0].data), "Javier")
	assert.equal(new TextDecoder().decode(event.arguments[1].data), "42")
	assert.equal(packets.some(packet => packet.type === GoxProtocol.PacketType.TRANSFER_BEGIN), true)
})

test("HTMLFormElement missing a required Go parameter sends no EVENT", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const errors = []
	socket.when("error", error => errors.push(error))
	native.dispatch("open")
	socket.emit("profile", new MockForm(new Map([
		["name", new MockInput({ value: "Javier" })],
		["avatar", new MockInput({ type: "file", files: new MockFileList() })],
		["gallery", new MockInput({ type: "file", multiple: true, files: new MockFileList() })],
	])))
	const response = encodeGoxPacket({
		type: GoxProtocol.PacketType.RESPONSE,
		flags: GoxProtocol.Flag.INTERNAL,
		headers: [{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(1) }],
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode('{"parameters":["name","age","avatar","gallery"],"hasReply":false}') }],
	})
	native.dispatch("message", { data: response.buffer })
	await new Promise(resolve => setTimeout(resolve, 0))
	assert.equal(native.sendCalls.length, 1)
	assert.equal(errors.length, 1)
	assert.equal(errors[0].code, GoxSocketErrorCode.ARGUMENT_COUNT_MISMATCH)
	assert.equal(errors[0].phase, GoxSocketErrorPhase.BINDING)
	assert.equal(errors[0].expected, 4)
	assert.equal(errors[0].received, 1)
	assert.deepEqual(errors[0].missing, ["age", "avatar", "gallery"])
	assert.match(errors[0].message, /expected 4 form arguments, received 1; missing: "age", "avatar", and "gallery"/)
})

test("HTMLFormElement counts an empty multiple file input as missing", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	const errors = []
	socket.when("error", error => errors.push(error))
	native.dispatch("open")
	const avatar = new MockFile([Uint8Array.of(1)], "avatar.png", { type: "image/png" })
	socket.emit("profile", new MockForm(new Map([
		["name", new MockInput({ value: "Javier" })],
		["age", new MockInput({ type: "number", valueAsNumber: 42 })],
		["admin", new MockInput({ type: "checkbox", checked: true })],
		["avatar", new MockInput({ type: "file", files: new MockFileList(avatar) })],
		["gallery", new MockInput({ type: "file", multiple: true, files: new MockFileList() })],
	])))
	const response = encodeGoxPacket({
		type: GoxProtocol.PacketType.RESPONSE,
		flags: GoxProtocol.Flag.INTERNAL,
		headers: [{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(1) }],
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode('{"parameters":["name","age","admin","avatar","gallery"],"hasReply":false}') }],
	})
	native.dispatch("message", { data: response.buffer })
	await new Promise(resolve => setTimeout(resolve, 0))
	assert.equal(native.sendCalls.length, 1)
	assert.equal(errors[0].code, GoxSocketErrorCode.ARGUMENT_COUNT_MISMATCH)
	assert.equal(errors[0].expected, 5)
	assert.equal(errors[0].received, 4)
	assert.deepEqual(errors[0].missing, ["gallery"])
	assert.match(errors[0].message, /missing: "gallery"/)
})

test("binary EVENT from server dispatches on with decoded JavaScript values", () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	let received
	socket.on("server-values", (...values) => { received = values })
	native.dispatch("open")
	const encoder = new TextEncoder()
	const packet = encodeGoxPacket({
		type: GoxProtocol.PacketType.EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: encoder.encode("server-values") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: Uint8Array.of(1, 1) },
		],
		arguments: [
			{ type: GoxProtocol.ArgumentType.TEXT, data: encoder.encode("GOX") },
			{ type: GoxProtocol.ArgumentType.JSON, data: encoder.encode('[1,true,{"id":7}]') },
			{ type: GoxProtocol.ArgumentType.BIGINT, data: encoder.encode("18446744073709551616") },
		],
	})
	native.dispatch("message", { data: packet.buffer })
	assert.deepEqual(received, ["GOX", [1, true, { id: 7 }], 18446744073709551616n])
})

test("malformed server packet emits protocol error and uses a browser-valid permanent close", () => {
	installBrowser()
	const socket = new Socket({ reconnect: false })
	const native = MockWebSocket.instances[0]
	let received
	socket.when("error", error => { received = error })
	native.dispatch("open")
	native.dispatch("message", { data: Uint8Array.of(0xff).buffer })
	assert.equal(received.code, GoxSocketErrorCode.PROTOCOL_ERROR)
	assert.equal(received.phase, GoxSocketErrorPhase.PROTOCOL)
	assert.deepEqual(native.closeCalls, [{ code: WSCloseCode.PERMANENT, reason: "Permanent disconnection" }])
})

test("server binding failure returns to the exact client emission", async () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	let received
	socket.when("error", error => { received = error })
	native.dispatch("open")
	socket.emit("user", "Javier", 42, "hola mundo")
	await new Promise(resolve => setTimeout(resolve, 0))
	await answerSignature(native, ["name", "age", "admin"], false)
	const emission = decodeGoxPacket(native.sendCalls[1])
	const eventID = emission.headers.find(header => header.type === GoxProtocol.HeaderType.EVENT_ID).value
	const encoder = new TextEncoder()
	const details = encoder.encode(JSON.stringify({
		message: "argument 2 expected bool, received TEXT",
		argument: 2,
		received: "TEXT",
		expected: "bool",
	}))
	const errorPacket = encodeGoxPacket({
		type: GoxProtocol.PacketType.ERROR,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: encoder.encode("user") },
			{ type: GoxProtocol.HeaderType.ERROR_CODE, value: encoder.encode("ARGUMENT_TYPE_MISMATCH") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: eventID },
		],
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: details }],
	})
	native.dispatch("message", { data: errorPacket.buffer })
	assert.equal(received.code, GoxSocketErrorCode.ARGUMENT_TYPE_MISMATCH)
	assert.equal(received.event, "user")
	assert.equal(received.eventID, 1n)
	assert.equal(received.argument, 2)
	assert.equal(received.received, "TEXT")
	assert.equal(received.expected, "bool")
	assert.equal(received.remote, true)
	assert.equal(native.closeCalls.length, 0)
})

test("missing client handler sends a correlated ERROR without disconnecting", () => {
	installBrowser()
	const socket = new Socket()
	const native = MockWebSocket.instances[0]
	native.dispatch("open")
	const encoder = new TextEncoder()
	const eventPacket = encodeGoxPacket({
		type: GoxProtocol.PacketType.EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: encoder.encode("missing") },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: Uint8Array.of(1, 9) },
		],
		arguments: [],
	})
	native.dispatch("message", { data: eventPacket.buffer })
	assert.equal(native.sendCalls.length, 1)
	const response = decodeGoxPacket(native.sendCalls[0])
	assert.equal(response.type, GoxProtocol.PacketType.ERROR)
	assert.equal(new TextDecoder().decode(response.headers[0].value), "missing")
	assert.equal(new TextDecoder().decode(response.headers[1].value), "EVENT_NOT_FOUND")
	assert.equal(native.closeCalls.length, 0)
})
