import { ClientValueKind, htmlToNode, normalizeClientValue } from "../client/values.js"


const HANDSHAKE_PATH = "/_gox/websocket/handshake"
const RECONNECT_QUERY = "_gox_reconnect"
const DEFAULT_RECONNECT = Object.freeze({
	enabled: true,
	delay: 1000,
	maxDelay: 30_000,
	attempts: 0,
})

export const GoxProtocol = Object.freeze({
	VERSION: 0x01,
	PacketType: Object.freeze({
		EVENT: 0x01,
		REQUEST: 0x02,
		RESPONSE: 0x03,
		ERROR: 0x04,
		TRANSFER_BEGIN: 0x10,
		TRANSFER_CHUNK: 0x11,
		TRANSFER_END: 0x12,
		TRANSFER_ABORT: 0x13,
		CHANNEL_JOIN: 0x20,
		CHANNEL_ACCEPTED: 0x21,
		CHANNEL_REJECTED: 0x22,
		CHANNEL_LEAVE: 0x23,
		CHANNEL_EVENT: 0x24,
		CHANNEL_REQUEST: 0x25,
		CHANNEL_RESPONSE: 0x26,
		CHANNEL_ERROR: 0x27,
	}),
	Flag: Object.freeze({ INTERNAL: 0x01, TRANSFER: 0x02 }),
	HeaderType: Object.freeze({
		EVENT_NAME: 0x01,
		REQUEST_ID: 0x02,
		TRANSFER_ID: 0x03,
		SEQUENCE: 0x04,
		TOTAL_SIZE: 0x05,
		FILE_NAME: 0x06,
		MIME_TYPE: 0x07,
		ERROR_CODE: 0x08,
		EVENT_ID: 0x09,
		CHANNEL_ID: 0x0a,
		CHANNEL_REASON: 0x0b,
	}),
	ArgumentType: Object.freeze({
		NULL: 0x00,
		TEXT: 0x01,
		NUMBER: 0x02,
		BOOLEAN: 0x03,
		DATE: 0x04,
		JSON: 0x05,
		BINARY: 0x06,
		FILE: 0x07,
		FILES: 0x08,
		BIGINT: 0x09,
		HTML: 0x0a,
	}),
})

const KNOWN_FLAGS = GoxProtocol.Flag.INTERNAL | GoxProtocol.Flag.TRANSFER
const textDecoder = new TextDecoder("utf-8", { fatal: true })

export class GoxProtocolError extends Error {
	constructor(message) {
		super(`Malformed GOX WebSocket packet: ${message}`)
		this.name = "GoxProtocolError"
	}
}

export function encodeCompactUint(input) {
	if (typeof input === "number" && (!Number.isSafeInteger(input) || input < 0)) {
		throw new RangeError("Compact integer numbers must be non-negative safe integers")
	}
	let value = typeof input === "bigint" ? input : BigInt(input)
	if (value < 0n || value > 0xffffffffffffffffn) {
		throw new RangeError("Compact integer must fit in uint64")
	}
	if (value === 0n) return Uint8Array.of(0)
	let width = 1
	while (value >= (1n << BigInt(width * 8))) width++
	const encoded = new Uint8Array(width + 1)
	encoded[0] = width
	for (let index = width; index > 0; index--) {
		encoded[index] = Number(value & 0xffn)
		value >>= 8n
	}
	return encoded
}

export function decodeCompactUint(input, offset = 0) {
	const data = asBytes(input)
	if (offset < 0 || offset >= data.length) malformed("missing compact integer width")
	const width = data[offset]
	if (width > 8) malformed(`compact integer width ${width} exceeds 8`)
	if (data.length - offset < width + 1) malformed("truncated compact integer")
	if (width === 0) return { value: 0n, bytesRead: 1 }
	if (data[offset + 1] === 0) malformed("compact integer is not minimally encoded")
	let value = 0n
	for (let index = 1; index <= width; index++) {
		value = (value << 8n) | BigInt(data[offset + index])
	}
	return { value, bytesRead: width + 1 }
}

export function encodeFileReference(transferID) {
	const encoded = encodeCompactUint(transferID)
	if (decodeCompactUint(encoded).value === 0n) malformed("file transfer ID must be greater than zero")
	return encoded
}

export function encodeFilesReference(transferIDs) {
	if (!Array.isArray(transferIDs)) throw new TypeError("File transfer IDs must be an array")
	if (transferIDs.length > 255) malformed(`file reference count ${transferIDs.length} exceeds 255`)
	return concatBytes([Uint8Array.of(transferIDs.length), ...transferIDs.map(encodeFileReference)])
}

export function decodeFileReference(input) {
	const data = asBytes(input)
	const decoded = decodeCompactUint(data)
	if (decoded.bytesRead !== data.length || decoded.value === 0n) malformed("file argument must contain one non-zero canonical transfer ID")
	return decoded.value
}

export function decodeFilesReference(input) {
	const data = asBytes(input)
	if (data.length === 0) malformed("files argument is missing its file count")
	const count = data[0]
	const transferIDs = []
	let offset = 1
	for (let index = 0; index < count; index++) {
		const decoded = decodeCompactUint(data, offset)
		if (decoded.value === 0n) malformed(`files argument transfer ID ${index} is invalid`)
		offset += decoded.bytesRead
		transferIDs.push(decoded.value)
	}
	if (offset !== data.length) malformed("files argument contains trailing bytes")
	return transferIDs
}

export function encodeBigInt(value) {
	if (typeof value !== "bigint") throw new TypeError("GOX BIGINT value must be a BigInt")
	return new TextEncoder().encode(value.toString(10))
}

export function decodeBigInt(input) {
	const data = asBytes(input)
	validateUTF8(data, "big integer")
	const value = textDecoder.decode(data)
	if (!/^(0|-?[1-9][0-9]*)$/.test(value)) malformed("big integer is not canonically encoded")
	return BigInt(value)
}

export function encodeGoxPacket(packet) {
	validatePacket(packet)
	const headers = packet.headers ?? []
	const payload = packetCarriesArguments(packet.type)
		? encodeGoxArguments(packet.arguments ?? [])
		: asBytes(packet.payload ?? new Uint8Array())
	const parts = [Uint8Array.of(packet.type, GoxProtocol.VERSION, packet.flags ?? 0, headers.length)]
	for (const header of headers) {
		const value = asBytes(header.value)
		parts.push(Uint8Array.of(header.type, value.length), value)
	}
	parts.push(payload)
	return concatBytes(parts)
}

export function decodeGoxPacket(input) {
	const data = asBytes(input)
	if (data.length < 4) malformed("packet header is truncated")
	if (data[1] !== GoxProtocol.VERSION) malformed(`unsupported protocol version ${data[1]}`)
	const packet = { type: data[0], flags: data[2], headers: [] }
	const headerCount = data[3]
	let offset = 4
	for (let index = 0; index < headerCount; index++) {
		if (data.length - offset < 2) malformed(`header ${index} is truncated`)
		const type = data[offset]
		const length = data[offset + 1]
		offset += 2
		if (data.length - offset < length) malformed(`header ${index} value is truncated`)
		packet.headers.push({ type, value: data.slice(offset, offset + length) })
		offset += length
	}
	if (packetCarriesArguments(packet.type)) {
		packet.arguments = decodeGoxArguments(data.subarray(offset))
	} else {
		packet.payload = data.slice(offset)
	}
	validatePacket(packet)
	return packet
}

export function encodeGoxArguments(args) {
	if (!Array.isArray(args)) throw new TypeError("GOX arguments must be an array")
	if (args.length > 255) malformed(`argument count ${args.length} exceeds 255`)
	const parts = [Uint8Array.of(args.length)]
	for (let index = 0; index < args.length; index++) {
		const argument = args[index]
		const data = asBytes(argument.data ?? new Uint8Array())
		validateArgument(index, { type: argument.type, data })
		parts.push(Uint8Array.of(argument.type), encodeCompactUint(data.length), data)
	}
	return concatBytes(parts)
}

export function decodeGoxArguments(input) {
	const data = asBytes(input)
	if (data.length === 0) malformed("missing argument count")
	const count = data[0]
	const args = []
	let offset = 1
	for (let index = 0; index < count; index++) {
		if (offset >= data.length) malformed(`argument ${index} type is missing`)
		const type = data[offset++]
		const length = decodeCompactUint(data, offset)
		offset += length.bytesRead
		if (length.value > BigInt(data.length - offset)) malformed(`argument ${index} data is truncated`)
		const end = offset + Number(length.value)
		const argument = { type, data: data.slice(offset, end) }
		validateArgument(index, argument)
		args.push(argument)
		offset = end
	}
	if (offset !== data.length) malformed("argument payload contains trailing bytes")
	return args
}

function validatePacket(packet) {
	if (packet === null || typeof packet !== "object") throw new TypeError("GOX packet must be an object")
	if (!knownPacketType(packet.type)) malformed(`unknown packet type ${hex(packet.type)}`)
	const flags = packet.flags ?? 0
	if (!Number.isInteger(flags) || flags < 0 || flags > 255) malformed("flags must fit in one byte")
	if ((flags & ~KNOWN_FLAGS) !== 0) malformed(`unknown flags ${hex(flags & ~KNOWN_FLAGS)}`)
	if (isChannelPacket(packet.type) && flags !== 0) malformed(`Channel packet ${hex(packet.type)} cannot contain flags`)
	const headers = packet.headers ?? []
	if (!Array.isArray(headers)) throw new TypeError("GOX packet headers must be an array")
	if (headers.length > 255) malformed(`header count ${headers.length} exceeds 255`)
	if (packetCarriesArguments(packet.type)) {
		if (packet.payload !== undefined && asBytes(packet.payload).length !== 0) malformed(`packet ${hex(packet.type)} cannot contain a raw payload`)
	} else if (packet.arguments !== undefined && packet.arguments.length !== 0) {
		malformed(`packet ${hex(packet.type)} cannot contain arguments`)
	}
	const seen = new Set()
	let previous = -1
	for (const header of headers) {
		if (!knownHeaderType(header.type)) malformed(`unknown header type ${hex(header.type)}`)
		if (seen.has(header.type)) malformed(`duplicate header type ${hex(header.type)}`)
		if (header.type <= previous) malformed("headers are not in canonical order")
		const value = asBytes(header.value)
		if (value.length === 0 || value.length > 255) malformed(`header ${hex(header.type)} length must be between 1 and 255`)
		if (!headerAllowed(packet.type, header.type)) malformed(`header ${hex(header.type)} is forbidden for packet ${hex(packet.type)}`)
		validateHeaderValue(header.type, value)
		seen.add(header.type)
		previous = header.type
	}
	for (const required of requiredHeaders(packet.type)) {
		if (!seen.has(required)) malformed(`required header ${hex(required)} is missing`)
	}
	if (packet.type === GoxProtocol.PacketType.ERROR) {
		const hasRequest = seen.has(GoxProtocol.HeaderType.REQUEST_ID)
		const hasEvent = seen.has(GoxProtocol.HeaderType.EVENT_ID)
		const hasEventName = seen.has(GoxProtocol.HeaderType.EVENT_NAME)
		if (hasRequest === hasEvent) malformed("ERROR packet must contain exactly one correlation ID")
		if (hasEvent !== hasEventName) malformed("event ERROR packet must contain its event name")
	}
	if (packet.type === GoxProtocol.PacketType.CHANNEL_ERROR) {
		const hasRequest = seen.has(GoxProtocol.HeaderType.REQUEST_ID)
		const hasEvent = seen.has(GoxProtocol.HeaderType.EVENT_ID)
		const hasEventName = seen.has(GoxProtocol.HeaderType.EVENT_NAME)
		if (hasRequest === hasEvent) malformed("Channel ERROR packet must contain exactly one correlation ID")
		if (hasEvent !== hasEventName) malformed("Channel event ERROR packet must contain its event name")
	}
	const P = GoxProtocol.PacketType
	if ([P.CHANNEL_ACCEPTED, P.CHANNEL_REJECTED, P.CHANNEL_LEAVE].includes(packet.type) && asBytes(packet.payload ?? new Uint8Array()).length !== 0) malformed("Channel control packet payload must be empty")
	if ([P.TRANSFER_BEGIN, P.TRANSFER_END, P.TRANSFER_ABORT].includes(packet.type) && asBytes(packet.payload ?? new Uint8Array()).length !== 0) malformed("transfer control packet payload must be empty")
	if (packet.type === P.TRANSFER_CHUNK && asBytes(packet.payload ?? new Uint8Array()).length === 0) malformed("transfer chunk payload cannot be empty")
}

function validateHeaderValue(type, value) {
	const H = GoxProtocol.HeaderType
	if ([H.EVENT_NAME, H.FILE_NAME, H.MIME_TYPE, H.ERROR_CODE, H.CHANNEL_ID, H.CHANNEL_REASON].includes(type)) validateUTF8(value, `header ${hex(type)}`)
	if (type === H.CHANNEL_ID) {
		const channelID = textDecoder.decode(value)
		if (channelID.trim() !== channelID) malformed("Channel ID cannot start or end with whitespace")
		if (/\p{Cc}/u.test(channelID)) malformed("Channel ID cannot contain control characters")
	}
	if ([H.REQUEST_ID, H.TRANSFER_ID, H.SEQUENCE, H.TOTAL_SIZE, H.EVENT_ID].includes(type)) {
		const decoded = decodeCompactUint(value)
		if (decoded.bytesRead !== value.length) malformed(`header ${hex(type)} does not contain one canonical integer`)
		if ([H.REQUEST_ID, H.TRANSFER_ID, H.EVENT_ID].includes(type) && decoded.value === 0n) malformed(`header ${hex(type)} ID must be greater than zero`)
	}
}

function validateArgument(index, argument) {
	if (!knownArgumentType(argument.type)) malformed(`argument ${index} has unknown type ${hex(argument.type)}`)
	const A = GoxProtocol.ArgumentType
	if (argument.type === A.NULL && argument.data.length !== 0) malformed(`argument ${index} null value must be empty`)
	if (argument.type === A.BOOLEAN && (argument.data.length !== 1 || argument.data[0] > 1)) malformed(`argument ${index} boolean must be exactly 0 or 1`)
	if ([A.TEXT, A.NUMBER, A.DATE, A.JSON, A.BIGINT, A.HTML].includes(argument.type)) validateUTF8(argument.data, `argument ${index}`)
	if (argument.type === A.NUMBER && !/^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/.test(textDecoder.decode(argument.data))) malformed(`argument ${index} is not a canonical decimal number`)
	if (argument.type === A.DATE) {
		const value = textDecoder.decode(argument.data)
		if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value) || Number.isNaN(Date.parse(value))) malformed(`argument ${index} is not an RFC3339 date`)
	}
	if (argument.type === A.JSON) {
		try { JSON.parse(textDecoder.decode(argument.data)) } catch { malformed(`argument ${index} is not valid JSON`) }
	}
	if (argument.type === A.FILE) decodeFileReference(argument.data)
	if (argument.type === A.FILES) decodeFilesReference(argument.data)
	if (argument.type === A.BIGINT) decodeBigInt(argument.data)
}

function requiredHeaders(type) {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	switch (type) {
	case P.EVENT: return [H.EVENT_NAME, H.EVENT_ID]
	case P.REQUEST: return [H.EVENT_NAME, H.REQUEST_ID]
	case P.RESPONSE: return [H.REQUEST_ID]
	case P.ERROR: return [H.ERROR_CODE]
	case P.TRANSFER_BEGIN: return [H.TRANSFER_ID, H.TOTAL_SIZE, H.FILE_NAME, H.MIME_TYPE]
	case P.TRANSFER_CHUNK: return [H.TRANSFER_ID, H.SEQUENCE]
	case P.TRANSFER_END: return [H.TRANSFER_ID, H.TOTAL_SIZE]
	case P.TRANSFER_ABORT: return [H.TRANSFER_ID, H.ERROR_CODE]
	case P.CHANNEL_JOIN:
	case P.CHANNEL_ACCEPTED:
	case P.CHANNEL_REJECTED:
	case P.CHANNEL_LEAVE: return [H.CHANNEL_ID]
	case P.CHANNEL_EVENT: return [H.EVENT_NAME, H.EVENT_ID, H.CHANNEL_ID]
	case P.CHANNEL_REQUEST: return [H.EVENT_NAME, H.REQUEST_ID, H.CHANNEL_ID]
	case P.CHANNEL_RESPONSE: return [H.REQUEST_ID, H.CHANNEL_ID]
	case P.CHANNEL_ERROR: return [H.ERROR_CODE, H.CHANNEL_ID]
	default: return []
	}
}

function headerAllowed(packet, header) {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	switch (packet) {
	case P.EVENT: return [H.EVENT_NAME, H.TRANSFER_ID, H.EVENT_ID].includes(header)
	case P.REQUEST: return [H.EVENT_NAME, H.REQUEST_ID, H.TRANSFER_ID].includes(header)
	case P.RESPONSE: return [H.REQUEST_ID, H.TRANSFER_ID].includes(header)
	case P.ERROR: return [H.EVENT_NAME, H.REQUEST_ID, H.ERROR_CODE, H.EVENT_ID].includes(header)
	case P.TRANSFER_BEGIN: return [H.TRANSFER_ID, H.TOTAL_SIZE, H.FILE_NAME, H.MIME_TYPE].includes(header)
	case P.TRANSFER_CHUNK: return [H.TRANSFER_ID, H.SEQUENCE].includes(header)
	case P.TRANSFER_END: return [H.TRANSFER_ID, H.TOTAL_SIZE].includes(header)
	case P.TRANSFER_ABORT: return [H.TRANSFER_ID, H.ERROR_CODE].includes(header)
	case P.CHANNEL_JOIN:
	case P.CHANNEL_ACCEPTED:
	case P.CHANNEL_REJECTED: return header === H.CHANNEL_ID
	case P.CHANNEL_LEAVE: return [H.CHANNEL_ID, H.CHANNEL_REASON].includes(header)
	case P.CHANNEL_EVENT: return [H.EVENT_NAME, H.TRANSFER_ID, H.EVENT_ID, H.CHANNEL_ID].includes(header)
	case P.CHANNEL_REQUEST: return [H.EVENT_NAME, H.REQUEST_ID, H.TRANSFER_ID, H.CHANNEL_ID].includes(header)
	case P.CHANNEL_RESPONSE: return [H.REQUEST_ID, H.TRANSFER_ID, H.CHANNEL_ID].includes(header)
	case P.CHANNEL_ERROR: return [H.EVENT_NAME, H.REQUEST_ID, H.ERROR_CODE, H.EVENT_ID, H.CHANNEL_ID].includes(header)
	default: return false
	}
}

function knownPacketType(type) { return [0x01, 0x02, 0x03, 0x04, 0x10, 0x11, 0x12, 0x13, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27].includes(type) }
function packetCarriesArguments(type) { return (type >= 0x01 && type <= 0x04) || [0x20, 0x24, 0x25, 0x26, 0x27].includes(type) }
function isChannelPacket(type) { return type >= GoxProtocol.PacketType.CHANNEL_JOIN && type <= GoxProtocol.PacketType.CHANNEL_ERROR }
function knownHeaderType(type) { return Number.isInteger(type) && type >= 0x01 && type <= 0x0b }
function knownArgumentType(type) { return Number.isInteger(type) && type >= 0x00 && type <= 0x0a }

function asBytes(value) {
	if (value instanceof Uint8Array) return value
	if (value instanceof ArrayBuffer) return new Uint8Array(value)
	if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength)
	throw new TypeError("GOX protocol values must be binary")
}

function cloneEventSignature(signature) {
	return {
		parameters: signature.parameters.slice(),
		hasReply: signature.hasReply,
	}
}

function concatBytes(parts) {
	const size = parts.reduce((total, part) => total + part.length, 0)
	const result = new Uint8Array(size)
	let offset = 0
	for (const part of parts) {
		result.set(part, offset)
		offset += part.length
	}
	return result
}

function validateUTF8(value, field) {
	try { textDecoder.decode(value) } catch { malformed(`${field} is not valid UTF-8`) }
}

function malformed(message) { throw new GoxProtocolError(message) }
function hex(value) { return `0x${Number(value).toString(16).padStart(2, "0")}` }

export const WSCloseCode = Object.freeze({
	DEFAULT: 0,
	NORMAL: 1000,
	GOING_AWAY: 1001,
	PROTOCOL: 1002,
	UNSUPPORTED: 1003,
	ABNORMAL: 1006,
	INVALID_DATA: 1007,
	POLICY: 1008,
	MESSAGE_LARGE: 1009,
	INTERNAL: 1011,
	RESTART: 1012,
	TRY_LATER: 1013,
	PERMANENT: 4000,
	RECONNECT: 4001,
	SESSION_EXPIRED: 4002,
	SESSION_DESTROYED: 4003,
})

export const WSDisconnectInitiator = Object.freeze({
	CLIENT: "client",
	SERVER: "server",
	UNKNOWN: "unknown",
})

export const GoxSocketErrorCode = Object.freeze({
	HANDSHAKE_FAILED: "HANDSHAKE_FAILED",
	CONNECTION_FAILED: "CONNECTION_FAILED",
	TRANSPORT_ERROR: "TRANSPORT_ERROR",
	HANDLER_FAILED: "HANDLER_FAILED",
	INVALID_STATE: "INVALID_STATE",
	CONNECTION_CLOSED: "CONNECTION_CLOSED",
	PROTOCOL_ERROR: "PROTOCOL_ERROR",
	ENCODE_FAILED: "ENCODE_FAILED",
	BIND_FAILED: "BIND_FAILED",
	EVENT_NOT_FOUND: "EVENT_NOT_FOUND",
	ARGUMENT_COUNT_MISMATCH: "ARGUMENT_COUNT_MISMATCH",
	ARGUMENT_TYPE_MISMATCH: "ARGUMENT_TYPE_MISMATCH",
	SESSION_SAVE_FAILED: "SESSION_SAVE_FAILED",
	SESSION_TOUCH_FAILED: "SESSION_TOUCH_FAILED",
	SESSION_EXPIRED: "SESSION_EXPIRED",
})

export const GoxSocketErrorPhase = Object.freeze({
	HANDSHAKE: "handshake",
	CONNECTION: "connection",
	TRANSPORT: "transport",
	HANDLER: "handler",
	PROTOCOL: "protocol",
	BINDING: "binding",
	SESSION: "session",
})

export class GoxSocketError extends Error {
	constructor(code, message, options = {}) {
		super(message, { cause: options.cause })
		this.name = "GoxSocketError"
		this.code = code
		this.phase = options.phase ?? ""
		this.event = options.event ?? ""
		this.retryable = options.retryable ?? false
		this.nativeEvent = options.nativeEvent
		this.eventID = options.eventID ?? 0n
		this.argument = options.argument ?? -1
		this.received = options.received ?? ""
		this.expected = options.expected ?? ""
		this.missing = Array.isArray(options.missing) ? options.missing.slice() : []
		this.remote = options.remote ?? false
	}
}

export class GoxChannelError extends GoxSocketError {
	constructor(code, message, options = {}) {
		super(code, message, options)
		this.name = "GoxChannelError"
		this.channelID = options.channelID ?? ""
	}
}

export const ChannelState = Object.freeze({
	CONNECTING: "connecting",
	CONNECTED: "connected",
	RECONNECTING: "reconnecting",
	DISCONNECTING: "disconnecting",
	DISCONNECTED: "disconnected",
	REJECTED: "rejected",
})

const channelConstructorToken = Symbol("GOX Channel constructor")

class ChannelEmission {
	#channel
	#event
	#values
	#mode = "pending"

	constructor(channel, event, values) {
		this.#channel = channel
		this.#event = event
		this.#values = values
		queueMicrotask(() => {
			if (this.#mode !== "pending") return
			this.#mode = "event"
			this.#channel._emit(this.#event, this.#values)
		})
	}

	await(timeout = 10_000) {
		if (this.#mode !== "pending") throw new TypeError("Channel emission was already dispatched")
		if (!Number.isFinite(timeout) || timeout < 0) throw new TypeError("Channel Await timeout must be a non-negative number")
		this.#mode = "request"
		return this.#channel._request(this.#event, this.#values, timeout)
	}
}

export class Channel {
	#when = new Map()
	#on = new Map()
	#transport
	#validation
	#state = ChannelState.CONNECTING
	#hasConnected = false
	#pendingRequests = new Map()

	constructor(token, transport, id, validation = []) {
		if (token !== channelConstructorToken) {
			throw new TypeError("Channels must be created with new socket.Channel(...)")
		}
		validateChannelID(id)
		this.id = id
		const separator = id.indexOf("#")
		this.name = separator === -1 ? id : id.slice(0, separator)
		this.key = separator === -1 ? "" : id.slice(separator + 1)
		this.#transport = transport
		this.#validation = validation.slice()
	}

	get connectionState() { return this.#state }

	when(event, handler) {
		registerChannelHandler(this.#when, "when", event, handler)
		return this
	}

	on(event, handler) {
		registerChannelHandler(this.#on, "on", event, handler)
		return this
	}

	emit(event, ...values) {
		if (typeof event !== "string" || event.length === 0) throw new TypeError("Channel.emit event must be a non-empty string")
		return new ChannelEmission(this, event, values)
	}

	leave() {
		if (this.#state === ChannelState.DISCONNECTED || this.#state === ChannelState.REJECTED || this.#state === ChannelState.DISCONNECTING) return this
		this.#state = ChannelState.DISCONNECTING
		this.#cancelPending("Channel request was interrupted by leave", false)
		this.#transport.leave(this)
		return this
	}

	_join(isReconnect) {
		if (this.#state === ChannelState.DISCONNECTING || this.#state === ChannelState.REJECTED) return
		this.#state = isReconnect ? ChannelState.RECONNECTING : ChannelState.CONNECTING
		this.#transport.join(this, this.#validation)
	}

	_accept() {
		if (this.#state !== ChannelState.CONNECTING && this.#state !== ChannelState.RECONNECTING) {
			throw new GoxProtocolError(`Channel ${JSON.stringify(this.id)} was accepted from state ${JSON.stringify(this.#state)}`)
		}
		const isReconnect = this.#hasConnected
		this.#hasConnected = true
		this.#state = ChannelState.CONNECTED
		this.#dispatchWhen("connected", isReconnect)
	}

	_reject() {
		if (this.#state !== ChannelState.CONNECTING && this.#state !== ChannelState.RECONNECTING) {
			throw new GoxProtocolError(`Channel ${JSON.stringify(this.id)} was rejected from state ${JSON.stringify(this.#state)}`)
		}
		this.#state = ChannelState.REJECTED
		this.#cancelPending("Channel validation was rejected", false)
		this.#dispatchWhen("rejected")
		this.#transport.release(this)
	}

	_remoteLeave(reason) {
		this.#state = ChannelState.DISCONNECTED
		this.#cancelPending(`Channel disconnected: ${reason}`, false)
		this.#dispatchWhen("disconnected", reason)
		this.#transport.release(this)
	}

	_socketDisconnected(reason, willReconnect) {
		if (this.#state === ChannelState.REJECTED || this.#state === ChannelState.DISCONNECTED) return
		this.#state = willReconnect ? ChannelState.RECONNECTING : ChannelState.DISCONNECTED
		this.#cancelPending("Channel request was interrupted by the Socket disconnection", willReconnect)
		this.#dispatchWhen("disconnected", "socket-disconnected")
		if (!willReconnect) this.#transport.release(this)
	}

	_remoteError(error) { this.#dispatchWhen("error", error) }

	_emit(event, values) {
		if (this.#state !== ChannelState.CONNECTED) {
			this.#dispatchWhen("error", new GoxChannelError(
				GoxSocketErrorCode.INVALID_STATE,
				`Channel ${JSON.stringify(this.id)} is not connected`,
				{ channelID: this.id, phase: GoxSocketErrorPhase.CONNECTION, event, retryable: this.#state === ChannelState.RECONNECTING },
			))
			return
		}
		this.#transport.emit(this, event, values)
	}

	_request(event, values, timeout) {
		if (this.#state !== ChannelState.CONNECTED) {
			const error = new GoxChannelError(
				GoxSocketErrorCode.INVALID_STATE,
				`Channel ${JSON.stringify(this.id)} is not connected`,
				{ channelID: this.id, phase: GoxSocketErrorPhase.CONNECTION, event, retryable: this.#state === ChannelState.RECONNECTING },
			)
			this.#dispatchWhen("error", error)
			return Promise.resolve(undefined)
		}
		const requestID = this.#transport.allocateRequestID()
		const response = new Promise(resolve => {
			const timer = setTimeout(() => {
				this.#pendingRequests.delete(requestID)
				this.#transport.ignoreRequest(this.id, requestID)
				const error = new GoxChannelError(
					"REQUEST_TIMEOUT",
					`Channel ${JSON.stringify(this.id)} event ${JSON.stringify(event)} timed out`,
					{ channelID: this.id, phase: GoxSocketErrorPhase.PROTOCOL, event, retryable: true },
				)
				this.#dispatchWhen("error", error)
				resolve(undefined)
			}, timeout)
			this.#pendingRequests.set(requestID, { event, resolve, timer })
		})
		this.#transport.request(this, event, values, requestID)
		return response
	}

	_receiveResponse(requestID, value) {
		const pending = this.#pendingRequests.get(requestID)
		if (pending === undefined) throw new GoxProtocolError(`Channel response references unknown request ${requestID}`)
		clearTimeout(pending.timer)
		this.#pendingRequests.delete(requestID)
		pending.resolve(value)
	}

	_receiveRequestError(requestID, code, message) {
		const pending = this.#pendingRequests.get(requestID)
		if (pending === undefined) throw new GoxProtocolError(`Channel error references unknown request ${requestID}`)
		clearTimeout(pending.timer)
		this.#pendingRequests.delete(requestID)
		const error = new GoxChannelError(code, message, {
			channelID: this.id,
			phase: GoxSocketErrorPhase.HANDLER,
			event: pending.event,
			remote: true,
			retryable: false,
		})
		this.#dispatchWhen("error", error)
		pending.resolve(undefined)
	}

	_requestSendFailed(requestID, cause) {
		const pending = this.#pendingRequests.get(requestID)
		if (pending === undefined) return
		clearTimeout(pending.timer)
		this.#pendingRequests.delete(requestID)
		const error = cause instanceof GoxChannelError ? cause : new GoxChannelError(
			GoxSocketErrorCode.ENCODE_FAILED,
			`Channel ${JSON.stringify(this.id)} request ${JSON.stringify(pending.event)} could not be sent`,
			{ channelID: this.id, phase: GoxSocketErrorPhase.PROTOCOL, event: pending.event, retryable: false, cause },
		)
		this.#dispatchWhen("error", error)
		pending.resolve(undefined)
	}

	#cancelPending(message, retryable) {
		for (const [requestID, pending] of this.#pendingRequests) {
			clearTimeout(pending.timer)
			this.#transport.ignoreRequest(this.id, requestID)
			const error = new GoxChannelError(
				"REQUEST_INTERRUPTED",
				message,
				{ channelID: this.id, phase: GoxSocketErrorPhase.TRANSPORT, event: pending.event, retryable },
			)
			this.#dispatchWhen("error", error)
			pending.resolve(undefined)
		}
		this.#pendingRequests.clear()
	}

	_receiveEvent(event, values, eventID) {
		if (this.#state !== ChannelState.CONNECTED) {
			throw new GoxProtocolError(`Channel ${JSON.stringify(this.id)} received an event from state ${JSON.stringify(this.#state)}`)
		}
		const handler = this.#on.get(event)
		if (handler === undefined) {
			const error = new GoxChannelError(
				GoxSocketErrorCode.EVENT_NOT_FOUND,
				`Channel ${JSON.stringify(this.id)} event ${JSON.stringify(event)} has no registered handler`,
				{ channelID: this.id, phase: GoxSocketErrorPhase.HANDLER, event, eventID, retryable: false },
			)
			this.#dispatchWhen("error", error)
			this.#transport.eventError(this, event, eventID, error)
			return
		}
		try {
			handler(...values)
		} catch (cause) {
			const error = cause instanceof GoxChannelError ? cause : new GoxChannelError(
				GoxSocketErrorCode.HANDLER_FAILED,
				`Channel ${JSON.stringify(this.id)} handler ${JSON.stringify(event)} failed`,
				{ channelID: this.id, phase: GoxSocketErrorPhase.HANDLER, event, eventID, retryable: false, cause },
			)
			if (error.eventID === 0n) error.eventID = eventID
			this.#dispatchWhen("error", error)
			this.#transport.eventError(this, event, eventID, error)
		}
	}

	#dispatchWhen(event, ...args) {
		const handler = this.#when.get(event)
		if (handler === undefined) return
		try {
			handler(...args)
		} catch (cause) {
			if (event === "error") return
			this.#dispatchWhen("error", new GoxSocketError(
				GoxSocketErrorCode.HANDLER_FAILED,
				`Channel ${JSON.stringify(this.id)} lifecycle handler ${JSON.stringify(event)} failed`,
				{ phase: GoxSocketErrorPhase.HANDLER, event, retryable: false, cause },
			))
		}
	}
}

export class Socket {
	#when = new Map()
	#on = new Map()
	#webSocket = null
	#state = "connecting"
	#url
	#reconnect
	#reconnectTimer = null
	#reconnectAttempt = 0
	#hasConnected = false
	#isReconnectAttempt = false
	#disconnectRequested = false
	#lastError = null
	#sendQueue = []
	#nextEventID = 1n
	#nextTransferID = 1n
	#nextRequestID = 1n
	#incomingTransfers = new Map()
	#pendingRequests = new Map()
	#signatureCache = new Map()
	#emissionChain = Promise.resolve()
	#pendingEmissions = 0
	#channels = new Map()
	#ignoredChannelRequests = new Map()

	constructor(options = {}) {
		if (options === null || typeof options !== "object" || Array.isArray(options)) {
			throw new TypeError("Socket options must be an object")
		}

		this.#url = resolveWebSocketURL(options.origin)
		this.#reconnect = normalizeReconnect(options.reconnect)
		const owner = this
		this.Channel = class SocketChannel extends Channel {
			constructor(id, ...validation) {
				const transport = owner.#channelTransport()
				super(channelConstructorToken, transport, id, validation)
				owner.#registerChannel(this)
			}
		}
		this.#open(false)
	}

	get state() {
		return this.#state
	}

	when(event, handler) {
		registerHandler(this.#when, "when", event, handler)
		return this
	}

	on(event, handler) {
		registerHandler(this.#on, "on", event, handler)
		return this
	}

	emit(event, ...values) {
		if (typeof event !== "string" || event.length === 0) {
			throw new TypeError("Socket.emit event must be a non-empty string")
		}
		const form = values.length === 1 && isHTMLForm(values[0]) ? values[0] : null
		return this.#requestSignature(event).then(signature => {
			const outgoing = form === null ? values : prepareFormArguments(form, signature.parameters, event)
			return this.#emitWithSignature(event, outgoing, signature)
		}).catch(cause => {
			const error = cause instanceof GoxSocketError ? cause : new GoxSocketError(
				GoxSocketErrorCode.ENCODE_FAILED,
				`Socket event "${event}" could not be encoded`,
				{ phase: GoxSocketErrorPhase.PROTOCOL, event, retryable: false, cause },
			)
			this.#dispatchWhen("error", error)
			return undefined
		})
	}

	disconnect(code = WSCloseCode.DEFAULT) {
		code = normalizeDisconnectCode(code)
		const reason = closeReason(code)
		this.#disconnectRequested = true
		this.#clearReconnectTimer()
		const webSocket = this.#webSocket
		if (webSocket !== null && typeof webSocket.close === "function") {
			webSocket.close(code, reason)
		} else {
			this.#state = "disconnected"
		}
		return this
	}

	#open(isReconnect) {
		this.#isReconnectAttempt = isReconnect
		this.#state = isReconnect ? "reconnecting" : "connecting"
		const url = new URL(this.#url)
		if (isReconnect) url.searchParams.set(RECONNECT_QUERY, "1")
		try {
			const webSocket = new WebSocket(url.href)
			this.#webSocket = webSocket
			webSocket.binaryType = "arraybuffer"

			webSocket.addEventListener("open", () => {
				if (this.#webSocket !== webSocket) return
				const reconnected = this.#isReconnectAttempt
				this.#state = "connected"
				this.#hasConnected = true
				this.#reconnectAttempt = 0
				this.#lastError = null
				this.#flushSendQueue()
				if (reconnected) {
					for (const channel of this.#channels.values()) channel._join(true)
				}
				this.#dispatchWhen("connect", reconnected)
			})

			webSocket.addEventListener("message", event => {
				if (this.#webSocket !== webSocket) return
				if (!(event.data instanceof ArrayBuffer) && !ArrayBuffer.isView(event.data)) {
					this.#protocolFailure("GOX WebSocket received a non-binary message")
					return
				}
				try {
					const packet = decodeGoxPacket(event.data)
					if (packet.type === GoxProtocol.PacketType.EVENT) this.#receiveEvent(packet)
					else if (packet.type === GoxProtocol.PacketType.REQUEST) this.#receiveRequest(packet)
					else if (packet.type === GoxProtocol.PacketType.ERROR) this.#receiveRemoteError(packet)
					else if (packet.type === GoxProtocol.PacketType.RESPONSE) this.#receiveResponse(packet)
					else if (packet.type === GoxProtocol.PacketType.CHANNEL_ACCEPTED) this.#receiveChannelAccepted(packet)
					else if (packet.type === GoxProtocol.PacketType.CHANNEL_REJECTED) this.#receiveChannelRejected(packet)
					else if (packet.type === GoxProtocol.PacketType.CHANNEL_LEAVE) this.#receiveChannelLeave(packet)
					else if (packet.type === GoxProtocol.PacketType.CHANNEL_EVENT) this.#receiveChannelEvent(packet)
					else if (packet.type === GoxProtocol.PacketType.CHANNEL_RESPONSE) this.#receiveChannelResponse(packet)
					else if (packet.type === GoxProtocol.PacketType.CHANNEL_ERROR) this.#receiveChannelError(packet)
					else if ([
						GoxProtocol.PacketType.TRANSFER_BEGIN,
						GoxProtocol.PacketType.TRANSFER_CHUNK,
						GoxProtocol.PacketType.TRANSFER_END,
						GoxProtocol.PacketType.TRANSFER_ABORT,
					].includes(packet.type)) this.#receiveTransfer(packet)
					else this.#protocolFailure("GOX WebSocket received an unexpected packet type")
				} catch (cause) {
					this.#protocolFailure("GOX WebSocket received a malformed packet", cause)
				}
			})

			webSocket.addEventListener("close", event => {
				if (this.#webSocket !== webSocket) return
				this.#webSocket = null
				this.#incomingTransfers.clear()
				this.#signatureCache.clear()
				for (const pending of this.#pendingRequests.values()) {
					clearTimeout(pending.timer)
					const error = new GoxSocketError("REQUEST_INTERRUPTED", `Socket request for "${pending.event}" was interrupted`, { phase: GoxSocketErrorPhase.TRANSPORT, event: pending.event, retryable: true })
					if (pending.kind === "signature") pending.reject(error)
					else {
						this.#dispatchWhen("error", error)
						pending.resolve(undefined)
					}
				}
				this.#pendingRequests.clear()
				const willReconnect = this.#shouldReconnect(event)
				this.#state = "disconnected"
				const reason = this.#disconnectReason(event, willReconnect)
				for (const channel of [...this.#channels.values()]) channel._socketDisconnected(reason, willReconnect)
				for (const timer of this.#ignoredChannelRequests.values()) clearTimeout(timer)
				this.#ignoredChannelRequests.clear()
				this.#dispatchWhen("disconnect", reason)
				if (willReconnect) this.#scheduleReconnect()
			})

			webSocket.addEventListener("error", event => {
				if (this.#webSocket !== webSocket) return
				const connecting = this.#state === "connecting"
				const socketError = new GoxSocketError(
					connecting ? GoxSocketErrorCode.HANDSHAKE_FAILED : GoxSocketErrorCode.TRANSPORT_ERROR,
					connecting
						? "The WebSocket handshake was rejected or could not be completed"
						: "The WebSocket transport failed",
					{
						phase: connecting ? GoxSocketErrorPhase.HANDSHAKE : GoxSocketErrorPhase.TRANSPORT,
						event: connecting ? "handshake" : "transport",
						retryable: !connecting || this.#hasConnected || this.#isReconnectAttempt,
						nativeEvent: event,
					},
				)
				this.#lastError = socketError
				this.#dispatchWhen("error", socketError)
			})
		} catch (error) {
			this.#state = "disconnected"
			queueMicrotask(() => this.#dispatchWhen("error", new GoxSocketError(
				GoxSocketErrorCode.CONNECTION_FAILED,
				"The WebSocket connection could not be created",
				{ phase: GoxSocketErrorPhase.CONNECTION, event: "connect", retryable: false, cause: error },
			)))
		}
	}

	#allocateEventID() {
		const eventID = this.#nextEventID
		this.#nextEventID = eventID === 0xffffffffffffffffn ? 1n : eventID + 1n
		return eventID
	}

	#allocateTransferID() {
		const transferID = this.#nextTransferID
		this.#nextTransferID = transferID === 0xffffffffffffffffn ? 1n : transferID + 1n
		return transferID
	}

	#allocateRequestID() {
		const requestID = this.#nextRequestID
		this.#nextRequestID = requestID === 0xffffffffffffffffn ? 1n : requestID + 1n
		return requestID
	}

	#channelTransport() {
		return Object.freeze({
			join: (channel, validation) => this.#sendChannelJoin(channel, validation),
			leave: channel => this.#sendChannelLeave(channel),
			emit: (channel, event, values) => this.#sendChannelEvent(channel, event, values),
			eventError: (channel, event, eventID, error) => this.#sendChannelEventError(channel, event, eventID, error),
			allocateRequestID: () => this.#allocateRequestID(),
			request: (channel, event, values, requestID) => this.#sendChannelRequest(channel, event, values, requestID),
			ignoreRequest: (channelID, requestID) => this.#ignoreChannelRequest(channelID, requestID),
			release: channel => {
				if (this.#channels.get(channel.id) === channel) this.#channels.delete(channel.id)
			},
		})
	}

	#registerChannel(channel) {
		if (this.#channels.has(channel.id)) throw new TypeError(`Channel ${JSON.stringify(channel.id)} already exists on this Socket`)
		this.#channels.set(channel.id, channel)
		channel._join(false)
	}

	#sendChannelJoin(channel, validation) {
		const emission = prepareChannelJoin(channel.id, validation, () => this.#allocateTransferID())
		this.#queueEmission(emission, `channel:${channel.id}:join`).catch(cause => this.#channelTransportError(channel, "join", cause))
	}

	#sendChannelLeave(channel) {
		try {
			const packet = encodeGoxPacket({
				type: GoxProtocol.PacketType.CHANNEL_LEAVE,
				headers: [{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode(channel.id) }],
			})
			this.#enqueuePackets([{ packet, event: `channel:${channel.id}:leave` }])
		} catch (cause) {
			this.#channelTransportError(channel, "leave", cause)
		}
	}

	#sendChannelEvent(channel, event, values) {
		try {
			const emission = prepareChannelEvent(channel.id, event, this.#allocateEventID(), values, () => this.#allocateTransferID())
			this.#queueEmission(emission, `channel:${channel.id}:${event}`).catch(cause => this.#channelTransportError(channel, event, cause))
		} catch (cause) {
			this.#channelTransportError(channel, event, cause)
		}
	}

	#sendChannelRequest(channel, event, values, requestID) {
		try {
			const emission = prepareChannelRequest(channel.id, event, requestID, values, () => this.#allocateTransferID())
			this.#queueEmission(emission, `channel:${channel.id}:${event}`).catch(cause => channel._requestSendFailed(requestID, cause))
		} catch (cause) {
			channel._requestSendFailed(requestID, cause)
		}
	}

	#sendChannelEventError(channel, event, eventID, error) {
		try {
			const packet = encodeGoxPacket({
				type: GoxProtocol.PacketType.CHANNEL_ERROR,
				headers: [
					{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode(event) },
					{ type: GoxProtocol.HeaderType.ERROR_CODE, value: new TextEncoder().encode(error.code) },
					{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(eventID) },
					{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode(channel.id) },
				],
				arguments: [{ type: GoxProtocol.ArgumentType.TEXT, data: new TextEncoder().encode(error.message) }],
			})
			this.#enqueuePackets([{ packet, event: `channel:${channel.id}:${event}:error` }])
		} catch (cause) {
			this.#channelTransportError(channel, event, cause)
		}
	}

	#channelTransportError(channel, event, cause) {
		const error = cause instanceof GoxChannelError ? cause : new GoxChannelError(
			GoxSocketErrorCode.ENCODE_FAILED,
			`Channel ${JSON.stringify(channel.id)} ${event} could not be encoded`,
			{ channelID: channel.id, phase: GoxSocketErrorPhase.PROTOCOL, event, retryable: false, cause },
		)
		channel._remoteError(error)
	}

	#channelForPacket(packet) {
		const id = decodeHeaderText(packet, GoxProtocol.HeaderType.CHANNEL_ID)
		const channel = this.#channels.get(id)
		if (channel === undefined) malformed(`Channel packet references unknown Channel ${JSON.stringify(id)}`)
		return channel
	}

	#channelRequestKey(channelID, requestID) { return `${channelID}\u0000${requestID}` }
	#ignoreChannelRequest(channelID, requestID) {
		const key = this.#channelRequestKey(channelID, requestID)
		const previous = this.#ignoredChannelRequests.get(key)
		if (previous !== undefined) clearTimeout(previous)
		const timer = setTimeout(() => this.#ignoredChannelRequests.delete(key), 30_000)
		timer?.unref?.()
		this.#ignoredChannelRequests.set(key, timer)
	}
	#consumeIgnoredChannelRequest(channelID, requestID) {
		const key = this.#channelRequestKey(channelID, requestID)
		const timer = this.#ignoredChannelRequests.get(key)
		if (timer === undefined) return false
		clearTimeout(timer)
		this.#ignoredChannelRequests.delete(key)
		return true
	}

	#receiveChannelAccepted(packet) { this.#channelForPacket(packet)._accept() }
	#receiveChannelRejected(packet) { this.#channelForPacket(packet)._reject() }
	#receiveChannelLeave(packet) {
		const reasonHeader = packet.headers.find(header => header.type === GoxProtocol.HeaderType.CHANNEL_REASON)
		const reason = reasonHeader === undefined ? "left" : textDecoder.decode(reasonHeader.value)
		this.#channelForPacket(packet)._remoteLeave(reason)
	}
	#receiveChannelEvent(packet) {
		const channel = this.#channelForPacket(packet)
		const event = decodeEventName(packet)
		const eventID = decodeHeaderUint(packet, GoxProtocol.HeaderType.EVENT_ID)
		const referenced = []
		const values = packet.arguments.map(argument => decodeEventArgument(argument, this.#incomingTransfers, referenced))
		for (const transferID of referenced) this.#incomingTransfers.delete(transferID)
		channel._receiveEvent(event, values, eventID)
	}
	#receiveChannelError(packet) {
		if (packet.arguments.length !== 1 || packet.arguments[0].type !== GoxProtocol.ArgumentType.TEXT) malformed("Channel ERROR requires one TEXT argument")
		const requestHeader = packet.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID)
		if (requestHeader !== undefined) {
			const channelID = decodeHeaderText(packet, GoxProtocol.HeaderType.CHANNEL_ID)
			const requestID = decodeCompactUint(requestHeader.value).value
			if (this.#consumeIgnoredChannelRequest(channelID, requestID)) return
			const channel = this.#channelForPacket(packet)
			channel._receiveRequestError(
				requestID,
				decodeHeaderText(packet, GoxProtocol.HeaderType.ERROR_CODE),
				textDecoder.decode(packet.arguments[0].data),
			)
			return
		}
		const channel = this.#channelForPacket(packet)
		const event = decodeEventName(packet)
		channel._remoteError(new GoxChannelError(
			decodeHeaderText(packet, GoxProtocol.HeaderType.ERROR_CODE),
			textDecoder.decode(packet.arguments[0].data),
			{
				channelID: channel.id,
				phase: GoxSocketErrorPhase.HANDLER,
				event,
				eventID: decodeHeaderUint(packet, GoxProtocol.HeaderType.EVENT_ID),
				remote: true,
				retryable: false,
			},
		))
	}
	#receiveChannelResponse(packet) {
		if (packet.arguments.length !== 1) malformed("Channel RESPONSE must contain exactly one reply value")
		const channelID = decodeHeaderText(packet, GoxProtocol.HeaderType.CHANNEL_ID)
		const requestID = decodeHeaderUint(packet, GoxProtocol.HeaderType.REQUEST_ID)
		if (this.#consumeIgnoredChannelRequest(channelID, requestID)) return
		const channel = this.#channelForPacket(packet)
		const referenced = []
		const value = decodeEventArgument(packet.arguments[0], this.#incomingTransfers, referenced)
		for (const transferID of referenced) this.#incomingTransfers.delete(transferID)
		channel._receiveResponse(requestID, value)
	}

	#emitWithSignature(event, values, signature) {
		const requestID = signature.hasReply ? this.#allocateRequestID() : 0n
		const eventID = signature.hasReply ? 0n : this.#allocateEventID()
		const emission = prepareEmission(event, requestID, eventID, values, () => this.#allocateTransferID())
		let response = Promise.resolve(undefined)
		if (signature.hasReply) {
			response = new Promise(resolve => {
				const timer = setTimeout(() => {
					this.#pendingRequests.delete(requestID)
					const error = new GoxSocketError("REQUEST_TIMEOUT", `Socket event "${event}" timed out`, { phase: GoxSocketErrorPhase.PROTOCOL, event, retryable: true })
					this.#dispatchWhen("error", error)
					resolve(undefined)
				}, 10_000)
				this.#pendingRequests.set(requestID, { kind: "event", event, resolve, timer })
			})
		}
		const queued = this.#queueEmission(emission, event)
		return queued.then(() => response).catch(cause => {
			if (signature.hasReply) {
				const pending = this.#pendingRequests.get(requestID)
				if (pending !== undefined) {
					clearTimeout(pending.timer)
					this.#pendingRequests.delete(requestID)
					pending.resolve(undefined)
				}
			}
			throw cause
		})
	}

	#queueEmission(emission, event) {
		if (emission.transfers.length > 0) this.#pendingEmissions++
		const queued = this.#emissionChain.then(async () => {
			const packets = emission.transfers.length === 0
				? [{ packet: emission.packet, event }]
				: await encodeEventTransfers(emission, event)
			this.#enqueuePackets(packets)
		})
		this.#emissionChain = queued.catch(() => undefined).finally(() => {
			if (emission.transfers.length > 0) this.#pendingEmissions--
		})
		return queued
	}

	#requestSignature(event) {
		if (this.#signatureCache.has(event)) return Promise.resolve(cloneEventSignature(this.#signatureCache.get(event)))
		const requestID = this.#allocateRequestID()
		const packet = encodeGoxPacket({
			type: GoxProtocol.PacketType.REQUEST,
			flags: GoxProtocol.Flag.INTERNAL,
			headers: [
				{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode("gox:get-signature") },
				{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) },
			],
			arguments: [{ type: GoxProtocol.ArgumentType.TEXT, data: new TextEncoder().encode(event) }],
		})
		return new Promise((resolve, reject) => {
			const timer = setTimeout(() => {
				this.#pendingRequests.delete(requestID)
				reject(new GoxSocketError("REQUEST_TIMEOUT", `Socket signature request for "${event}" timed out`, { phase: GoxSocketErrorPhase.PROTOCOL, event, retryable: true }))
			}, 10_000)
			this.#pendingRequests.set(requestID, { kind: "signature", event, resolve, reject, timer })
			this.#enqueuePackets([{ packet, event: "gox:get-signature" }])
		})
	}

	#receiveEvent(packet) {
		const eventName = decodeEventName(packet)
		const eventID = decodeHeaderUint(packet, GoxProtocol.HeaderType.EVENT_ID)
		const referenced = []
		const values = packet.arguments.map(argument => decodeEventArgument(argument, this.#incomingTransfers, referenced))
		for (const transferID of referenced) this.#incomingTransfers.delete(transferID)
		const dispatched = this.#dispatch(this.#on, eventName, values, false, eventID)
		if (!dispatched.handled) {
			const error = new GoxSocketError(
				GoxSocketErrorCode.EVENT_NOT_FOUND,
				`Socket event "${eventName}" has no registered handler`,
				{ phase: GoxSocketErrorPhase.HANDLER, event: eventName, eventID, retryable: false },
			)
			this.#dispatchWhen("error", error)
			this.#sendEventError(error)
		} else if (dispatched.error !== null) {
			dispatched.error.eventID = eventID
			this.#sendEventError(dispatched.error)
		}
	}

	#receiveRequest(packet) {
		if (packet.flags !== 0) malformed("public REQUEST packet contains flags")
		const eventName = decodeEventName(packet)
		const requestID = decodeHeaderUint(packet, GoxProtocol.HeaderType.REQUEST_ID)
		const referenced = []
		const values = packet.arguments.map(argument => decodeEventArgument(argument, this.#incomingTransfers, referenced))
		for (const transferID of referenced) this.#incomingTransfers.delete(transferID)
		const handler = this.#on.get(eventName)
		if (handler === undefined) {
			const error = new GoxSocketError(
				GoxSocketErrorCode.EVENT_NOT_FOUND,
				`Socket event "${eventName}" has no registered handler`,
				{ phase: GoxSocketErrorPhase.HANDLER, event: eventName, remote: false, retryable: false },
			)
			this.#dispatchWhen("error", error)
			this.#sendRequestError(requestID, eventName, error)
			return
		}
		Promise.resolve().then(() => handler(...values)).then(value => {
			const emission = prepareReply(requestID, value === undefined ? null : value, () => this.#allocateTransferID())
			return this.#queueEmission(emission, eventName)
		}).catch(cause => {
			const error = cause instanceof GoxSocketError ? cause : new GoxSocketError(
				GoxSocketErrorCode.HANDLER_FAILED,
				`Socket handler "${eventName}" failed`,
				{ phase: GoxSocketErrorPhase.HANDLER, event: eventName, retryable: false, cause },
			)
			this.#dispatchWhen("error", error)
			this.#sendRequestError(requestID, eventName, error)
		})
	}

	#sendRequestError(requestID, event, error) {
		const details = {
			message: error.message,
			argument: error.argument,
			received: error.received,
			expected: error.expected,
		}
		const packet = encodeGoxPacket({
			type: GoxProtocol.PacketType.ERROR,
			headers: [
				{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) },
				{ type: GoxProtocol.HeaderType.ERROR_CODE, value: new TextEncoder().encode(error.code) },
			],
			arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode(JSON.stringify(details)) }],
		})
		this.#enqueuePackets([{ packet, event }])
	}

	#receiveTransfer(packet) {
		const P = GoxProtocol.PacketType
		const H = GoxProtocol.HeaderType
		const id = decodeHeaderUint(packet, H.TRANSFER_ID)
		if (packet.type === P.TRANSFER_BEGIN) {
			if (this.#incomingTransfers.has(id)) malformed(`transfer ${id} already exists`)
			const total = decodeHeaderUint(packet, H.TOTAL_SIZE)
			if (total > BigInt(Number.MAX_SAFE_INTEGER)) malformed(`transfer ${id} is too large`)
			const unknown = total === BigInt(Number.MAX_SAFE_INTEGER)
			this.#incomingTransfers.set(id, {
				name: decodeHeaderText(packet, H.FILE_NAME),
				type: decodeHeaderText(packet, H.MIME_TYPE),
				total: unknown ? null : Number(total),
				data: unknown ? [] : new Uint8Array(Number(total)),
				received: 0,
				complete: false,
			})
			return
		}
		const transfer = this.#incomingTransfers.get(id)
		if (transfer === undefined) malformed(`transfer ${id} is unknown`)
		if (packet.type === P.TRANSFER_CHUNK) {
			const position = decodeHeaderUint(packet, H.SEQUENCE)
			if (position !== BigInt(transfer.received)) malformed(`transfer ${id} chunk is out of order`)
			if (transfer.total !== null && transfer.received + packet.payload.length > transfer.total) malformed(`transfer ${id} exceeds its declared size`)
			if (transfer.total === null) transfer.data.push(packet.payload.slice())
			else transfer.data.set(packet.payload, transfer.received)
			transfer.received += packet.payload.length
			return
		}
		if (packet.type === P.TRANSFER_END) {
			const total = decodeHeaderUint(packet, H.TOTAL_SIZE)
			if (transfer.total !== null && (total !== BigInt(transfer.total) || transfer.received !== transfer.total)) malformed(`transfer ${id} ended with an invalid size`)
			if (transfer.total === null) {
				if (total !== BigInt(transfer.received)) malformed(`transfer ${id} ended with an invalid size`)
				const data = new Uint8Array(transfer.received)
				let offset = 0
				for (const chunk of transfer.data) { data.set(chunk, offset); offset += chunk.length }
				transfer.data = data
				transfer.total = transfer.received
			}
			transfer.complete = true
			return
		}
		this.#incomingTransfers.delete(id)
	}

	#sendEventError(error) {
		const details = {
			message: error.message,
			argument: error.argument,
			received: error.received,
			expected: error.expected,
		}
		const packet = encodeGoxPacket({
			type: GoxProtocol.PacketType.ERROR,
			headers: [
				{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode(error.event) },
				{ type: GoxProtocol.HeaderType.ERROR_CODE, value: new TextEncoder().encode(error.code) },
				{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(error.eventID) },
			],
			arguments: [{ type: GoxProtocol.ArgumentType.JSON, data: new TextEncoder().encode(JSON.stringify(details)) }],
		})
		this.#writePacket(packet, error.event)
	}

	#receiveRemoteError(packet) {
		if (packet.arguments.length !== 1 || packet.arguments[0].type !== GoxProtocol.ArgumentType.JSON) malformed("event ERROR payload must contain one JSON argument")
		const details = JSON.parse(textDecoder.decode(packet.arguments[0].data))
		const requestHeader = packet.headers.find(header => header.type === GoxProtocol.HeaderType.REQUEST_ID)
		if (requestHeader !== undefined) {
			const requestID = decodeCompactUint(requestHeader.value).value
			const pending = this.#pendingRequests.get(requestID)
			if (pending === undefined) malformed(`ERROR references unknown request ${requestID}`)
			clearTimeout(pending.timer)
			this.#pendingRequests.delete(requestID)
			const error = new GoxSocketError(
				decodeHeaderText(packet, GoxProtocol.HeaderType.ERROR_CODE),
				typeof details.message === "string" ? details.message : "WebSocket request failed",
				{
					phase: pending.kind === "signature" ? GoxSocketErrorPhase.PROTOCOL : GoxSocketErrorPhase.HANDLER,
					event: pending.event,
					argument: Number.isInteger(details.argument) ? details.argument : -1,
					received: typeof details.received === "string" ? details.received : "",
					expected: typeof details.expected === "string" ? details.expected : "",
					remote: true,
					retryable: false,
				},
			)
			if (pending.kind === "signature") pending.reject(error)
			else {
				this.#dispatchWhen("error", error)
				pending.resolve(undefined)
			}
			return
		}
		const error = new GoxSocketError(
			decodeHeaderText(packet, GoxProtocol.HeaderType.ERROR_CODE),
			typeof details.message === "string" ? details.message : "Remote WebSocket event failed",
			{
				phase: GoxSocketErrorPhase.HANDLER,
				event: decodeEventName(packet),
				eventID: decodeHeaderUint(packet, GoxProtocol.HeaderType.EVENT_ID),
				argument: Number.isInteger(details.argument) ? details.argument : -1,
				received: typeof details.received === "string" ? details.received : "",
				expected: typeof details.expected === "string" ? details.expected : "",
				remote: true,
				retryable: false,
			},
		)
		this.#dispatchWhen("error", error)
	}

	#receiveResponse(packet) {
		if (packet.flags === GoxProtocol.Flag.INTERNAL) {
			this.#receiveInternalResponse(packet)
			return
		}
		if (packet.flags !== 0) malformed("public RESPONSE packet contains flags")
		const requestID = decodeHeaderUint(packet, GoxProtocol.HeaderType.REQUEST_ID)
		const pending = this.#pendingRequests.get(requestID)
		if (pending === undefined || pending.kind !== "event") malformed(`RESPONSE references unknown request ${requestID}`)
		if (packet.arguments.length !== 1) malformed("event RESPONSE must contain exactly one reply value")
		const referenced = []
		const value = decodeEventArgument(packet.arguments[0], this.#incomingTransfers, referenced)
		for (const transferID of referenced) this.#incomingTransfers.delete(transferID)
		clearTimeout(pending.timer)
		this.#pendingRequests.delete(requestID)
		pending.resolve(value)
	}

	#receiveInternalResponse(packet) {
		if (packet.arguments.length !== 1 || packet.arguments[0].type !== GoxProtocol.ArgumentType.JSON) malformed("signature RESPONSE must contain one JSON argument")
		const requestID = decodeHeaderUint(packet, GoxProtocol.HeaderType.REQUEST_ID)
		const pending = this.#pendingRequests.get(requestID)
		if (pending === undefined || pending.kind !== "signature") malformed(`signature RESPONSE references unknown request ${requestID}`)
		const signature = JSON.parse(textDecoder.decode(packet.arguments[0].data))
		if (signature === null || typeof signature !== "object" || Array.isArray(signature)) malformed("signature RESPONSE must contain an object")
		if (!Array.isArray(signature.parameters) || signature.parameters.some(name => typeof name !== "string" || name.length === 0)) malformed("signature RESPONSE contains invalid parameter names")
		if (typeof signature.hasReply !== "boolean") malformed("signature RESPONSE contains an invalid reply contract")
		clearTimeout(pending.timer)
		this.#pendingRequests.delete(requestID)
		this.#signatureCache.set(pending.event, cloneEventSignature(signature))
		pending.resolve(cloneEventSignature(signature))
	}

	#flushSendQueue() {
		const queue = this.#sendQueue
		this.#sendQueue = []
		for (const item of queue) this.#writePacket(item.packet, item.event)
	}

	#enqueuePackets(items) {
		if (this.#state === "connected" && this.#webSocket !== null) {
			for (const item of items) this.#writePacket(item.packet, item.event)
		} else {
			this.#sendQueue.push(...items)
		}
	}

	#writePacket(packet, event) {
		try {
			this.#webSocket.send(packet)
		} catch (cause) {
			const error = new GoxSocketError(
				GoxSocketErrorCode.TRANSPORT_ERROR,
				`Socket event "${event}" could not be sent`,
				{ phase: GoxSocketErrorPhase.TRANSPORT, event, retryable: true, cause },
			)
			this.#dispatchWhen("error", error)
		}
	}

	#protocolFailure(message, cause) {
		this.#dispatchWhen("error", new GoxSocketError(
			GoxSocketErrorCode.PROTOCOL_ERROR,
			message,
			{ phase: GoxSocketErrorPhase.PROTOCOL, event: "protocol", retryable: false, cause },
		))
		// Browsers forbid application JavaScript from passing reserved close
		// codes such as 1002 to WebSocket.close(). Use GOX's permanent custom
		// close code; the dispatched error retains the precise protocol cause.
		this.disconnect(WSCloseCode.PERMANENT)
	}

	#shouldReconnect(event) {
		if (this.#disconnectRequested || !this.#reconnect.enabled) return false
		if (this.#reconnect.attempts !== 0 && this.#reconnectAttempt >= this.#reconnect.attempts) return false
		if (this.#lastError?.code === GoxSocketErrorCode.HANDSHAKE_FAILED && !this.#lastError.retryable) return false
		return closeCodeShouldReconnect(event?.code ?? 1006)
	}

	#scheduleReconnect() {
		const exponent = Math.min(this.#reconnectAttempt, 30)
		const delay = Math.min(this.#reconnect.delay * (2 ** exponent), this.#reconnect.maxDelay)
		this.#reconnectAttempt += 1
		this.#state = "reconnecting"
		this.#reconnectTimer = setTimeout(() => {
			this.#reconnectTimer = null
			this.#open(true)
		}, delay)
	}

	#clearReconnectTimer() {
		if (this.#reconnectTimer !== null) {
			clearTimeout(this.#reconnectTimer)
			this.#reconnectTimer = null
		}
	}

	#disconnectReason(event, willReconnect) {
		const code = event?.code ?? WSCloseCode.ABNORMAL
		return {
			code,
			reason: event?.reason ?? "",
			clean: event?.wasClean ?? false,
			initiator: this.#disconnectRequested
				? WSDisconnectInitiator.CLIENT
				: code === WSCloseCode.ABNORMAL
					? WSDisconnectInitiator.UNKNOWN
					: WSDisconnectInitiator.SERVER,
			willReconnect,
			nativeEvent: event,
		}
	}

	#dispatchWhen(event, ...args) {
		return this.#dispatch(this.#when, event, args, true)
	}

	#dispatchOn(event, ...args) {
		return this.#dispatch(this.#on, event, args, false)
	}

	#dispatch(handlers, event, args, lifecycle, eventID = 0n) {
		const handler = handlers.get(event)
		if (handler === undefined) {
			return { value: undefined, handled: false, error: null }
		}

		try {
			return { value: handler(...args), handled: true, error: null }
		} catch (error) {
			const socketError = error instanceof GoxSocketError
				? error
				: new GoxSocketError(
					GoxSocketErrorCode.HANDLER_FAILED,
					`Socket handler "${event}" failed`,
					{ phase: GoxSocketErrorPhase.HANDLER, event, retryable: false, cause: error },
				)
			if (socketError.eventID === 0n) socketError.eventID = eventID
			if (!lifecycle || event !== "error") {
				this.#dispatchWhen("error", socketError)
			}
			return { value: undefined, handled: true, error: socketError }
		}
	}
}

function prepareEmission(event, requestID, eventID, values, allocateTransferID) {
	const transfers = []
	const expectsReply = requestID !== 0n
	const packet = encodeGoxPacket({
		type: expectsReply ? GoxProtocol.PacketType.REQUEST : GoxProtocol.PacketType.EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode(event) },
			{ type: expectsReply ? GoxProtocol.HeaderType.REQUEST_ID : GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(expectsReply ? requestID : eventID) },
		],
		arguments: values.map(value => encodeEventArgument(value, allocateTransferID, transfers)),
	})
	return { packet, transfers }
}

function prepareChannelJoin(channelID, values, allocateTransferID) {
	const transfers = []
	const packet = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_JOIN,
		headers: [{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode(channelID) }],
		arguments: values.map(value => encodeEventArgument(value, allocateTransferID, transfers)),
	})
	return { packet, transfers }
}

function prepareChannelEvent(channelID, event, eventID, values, allocateTransferID) {
	const transfers = []
	const packet = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_EVENT,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode(event) },
			{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(eventID) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode(channelID) },
		],
		arguments: values.map(value => encodeEventArgument(value, allocateTransferID, transfers)),
	})
	return { packet, transfers }
}

function prepareChannelRequest(channelID, event, requestID, values, allocateTransferID) {
	const transfers = []
	const packet = encodeGoxPacket({
		type: GoxProtocol.PacketType.CHANNEL_REQUEST,
		headers: [
			{ type: GoxProtocol.HeaderType.EVENT_NAME, value: new TextEncoder().encode(event) },
			{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) },
			{ type: GoxProtocol.HeaderType.CHANNEL_ID, value: new TextEncoder().encode(channelID) },
		],
		arguments: values.map(value => encodeEventArgument(value, allocateTransferID, transfers)),
	})
	return { packet, transfers }
}

function prepareReply(requestID, value, allocateTransferID) {
	const transfers = []
	const packet = encodeGoxPacket({
		type: GoxProtocol.PacketType.RESPONSE,
		headers: [{ type: GoxProtocol.HeaderType.REQUEST_ID, value: encodeCompactUint(requestID) }],
		arguments: [encodeEventArgument(value, allocateTransferID, transfers)],
	})
	return { packet, transfers }
}

function encodeEventArgument(value, allocateTransferID, transfers) {
	const A = GoxProtocol.ArgumentType
	const normalized = normalizeClientValue(value)
	switch (normalized.kind) {
	case ClientValueKind.NULL: return { type: A.NULL, data: new Uint8Array() }
	case ClientValueKind.TEXT: return { type: A.TEXT, data: new TextEncoder().encode(normalized.value) }
	case ClientValueKind.BOOLEAN: return { type: A.BOOLEAN, data: Uint8Array.of(normalized.value ? 1 : 0) }
	case ClientValueKind.NUMBER: return { type: A.NUMBER, data: new TextEncoder().encode(String(normalized.value)) }
	case ClientValueKind.BIGINT: return { type: A.BIGINT, data: encodeBigInt(normalized.value) }
	case ClientValueKind.HTML: return { type: A.HTML, data: new TextEncoder().encode(normalized.value) }
	case ClientValueKind.DATE: return { type: A.DATE, data: new TextEncoder().encode(normalized.value.toISOString()) }
	case ClientValueKind.BINARY:
		if (normalized.value instanceof ArrayBuffer || ArrayBuffer.isView(normalized.value)) return { type: A.BINARY, data: asBytes(normalized.value).slice() }
		throw new TypeError("Socket.emit Blob support requires the transfer integration")
	case ClientValueKind.FILE:
		return encodeFileArgument(normalized.value, allocateTransferID, transfers)
	case ClientValueKind.FILES: {
		const ids = normalized.value.map(file => {
			const id = allocateTransferID()
			transfers.push({ id, file })
			return id
		})
		return { type: A.FILES, data: encodeFilesReference(ids) }
	}
	case ClientValueKind.JSON: {
		const json = JSON.stringify(normalized.value)
		if (json === undefined) throw new TypeError("Socket.emit value cannot be represented as JSON")
		return { type: A.JSON, data: new TextEncoder().encode(json) }
	}
	default: throw new TypeError(`Socket.emit does not support normalized kind ${normalized.kind}`)
	}
}

function encodeFileArgument(file, allocateTransferID, transfers) {
	const id = allocateTransferID()
	transfers.push({ id, file })
	return { type: GoxProtocol.ArgumentType.FILE, data: encodeFileReference(id) }
}

async function encodeEventTransfers(emission, event) {
	const packets = []
	for (const transfer of emission.transfers) {
		if (typeof transfer.file.arrayBuffer !== "function") throw new TypeError(`Socket.emit file ${JSON.stringify(transfer.file.name)} cannot be read`)
		const data = new Uint8Array(await transfer.file.arrayBuffer())
		const name = String(transfer.file.name || "")
		if (name === "") throw new TypeError("Socket.emit files must have a name")
		const mime = String(transfer.file.type || "application/octet-stream")
		packets.push({ packet: encodeTransferBegin(transfer.id, name, mime, data.length), event })
		for (let position = 0; position < data.length; position += 64 << 10) {
			packets.push({ packet: encodeTransferChunk(transfer.id, position, data.slice(position, position + (64 << 10))), event })
		}
		packets.push({ packet: encodeTransferEnd(transfer.id, data.length), event })
	}
	packets.push({ packet: emission.packet, event })
	return packets
}

function encodeTransferBegin(id, name, mime, size) {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	return encodeGoxPacket({ type: P.TRANSFER_BEGIN, headers: [
		{ type: H.TRANSFER_ID, value: encodeCompactUint(id) },
		{ type: H.TOTAL_SIZE, value: encodeCompactUint(size) },
		{ type: H.FILE_NAME, value: new TextEncoder().encode(name) },
		{ type: H.MIME_TYPE, value: new TextEncoder().encode(mime) },
	], payload: new Uint8Array() })
}

function encodeTransferChunk(id, position, data) {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	return encodeGoxPacket({ type: P.TRANSFER_CHUNK, headers: [
		{ type: H.TRANSFER_ID, value: encodeCompactUint(id) },
		{ type: H.SEQUENCE, value: encodeCompactUint(position) },
	], payload: data })
}

function encodeTransferEnd(id, size) {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	return encodeGoxPacket({ type: P.TRANSFER_END, headers: [
		{ type: H.TRANSFER_ID, value: encodeCompactUint(id) },
		{ type: H.TOTAL_SIZE, value: encodeCompactUint(size) },
	], payload: new Uint8Array() })
}

function decodeEventName(packet) {
	const header = packet.headers.find(item => item.type === GoxProtocol.HeaderType.EVENT_NAME)
	return textDecoder.decode(header.value)
}

function decodeHeaderText(packet, type) {
	const header = packet.headers.find(item => item.type === type)
	return textDecoder.decode(header.value)
}

function decodeHeaderUint(packet, type) {
	const header = packet.headers.find(item => item.type === type)
	return decodeCompactUint(header.value).value
}

function decodeEventArgument(argument, transfers = new Map(), referenced = []) {
	const A = GoxProtocol.ArgumentType
	switch (argument.type) {
	case A.NULL: return null
	case A.TEXT: return textDecoder.decode(argument.data)
	case A.NUMBER: {
		const value = Number(textDecoder.decode(argument.data))
		if (!Number.isFinite(value)) malformed("NUMBER argument is outside the JavaScript numeric range")
		return value
	}
	case A.BOOLEAN: return argument.data[0] === 1
	case A.DATE: return new Date(textDecoder.decode(argument.data))
	case A.JSON: return JSON.parse(textDecoder.decode(argument.data))
	case A.BINARY: return argument.data.slice()
	case A.BIGINT: return decodeBigInt(argument.data)
	case A.HTML: return htmlToNode(textDecoder.decode(argument.data))
	case A.FILE: return resolveTransferredFile(decodeFileReference(argument.data), transfers, referenced)
	case A.FILES: return decodeFilesReference(argument.data).map(id => resolveTransferredFile(id, transfers, referenced))
	default: malformed(`event argument type ${hex(argument.type)} is not supported yet`)
	}
}

function resolveTransferredFile(id, transfers, referenced) {
	const transfer = transfers.get(id)
	if (transfer === undefined || !transfer.complete) malformed(`file transfer ${id} is incomplete`)
	referenced.push(id)
	return new File([transfer.data], transfer.name, { type: transfer.type })
}

function registerHandler(registry, method, event, handler) {
	if (typeof event !== "string" || event.length === 0) {
		throw new TypeError(`Socket.${method} event must be a non-empty string`)
	}
	if (typeof handler !== "function") {
		throw new TypeError(`Socket.${method}("${event}") handler must be a function`)
	}
	registry.set(event, handler)
}

function registerChannelHandler(registry, method, event, handler) {
	if (typeof event !== "string" || event.length === 0) {
		throw new TypeError(`Channel.${method} event must be a non-empty string`)
	}
	if (typeof handler !== "function") {
		throw new TypeError(`Channel.${method}(${JSON.stringify(event)}) handler must be a function`)
	}
	registry.set(event, handler)
}

function validateChannelID(id) {
	if (typeof id !== "string" || id.length === 0) throw new TypeError("Channel ID must be a non-empty string")
	if (new TextEncoder().encode(id).length > 255) throw new TypeError("Channel ID cannot exceed 255 UTF-8 bytes")
	if (id.trim() !== id) throw new TypeError("Channel ID cannot start or end with whitespace")
	if (/\p{Cc}/u.test(id)) throw new TypeError("Channel ID cannot contain control characters")
	if ((id.match(/#/g) ?? []).length > 1) throw new TypeError("Channel ID cannot contain more than one instance separator")
	const [name, key] = id.split("#")
	if (name.length === 0) throw new TypeError("Channel name cannot be empty")
	if (id.includes("#") && key.length === 0) throw new TypeError("Channel instance key cannot be empty")
}

function isHTMLForm(value) {
	return typeof globalThis.HTMLFormElement === "function" && value instanceof globalThis.HTMLFormElement
}

function prepareFormArguments(form, names, event) {
	const values = []
	const missing = []
	for (const name of names) {
		const field = findFormControl(form, name)
		if (!field.found || !formControlHasValue(field.value)) {
			missing.push(name)
			continue
		}
		values.push(field.value)
	}
	if (missing.length > 0) {
		const expected = names.length
		const received = expected - missing.length
		const quoted = formatMissingNames(missing)
		throw new GoxSocketError(
			GoxSocketErrorCode.ARGUMENT_COUNT_MISMATCH,
			`Socket event "${event}" expected ${expected} form arguments, received ${received}; missing: ${quoted}`,
			{ phase: GoxSocketErrorPhase.BINDING, event, retryable: false, expected, received, missing },
		)
	}
	return values
}

function findFormControl(form, name) {
	const elements = form.elements
	const field = elements && typeof elements.namedItem === "function" ? elements.namedItem(name) : null
	if (field === null || field === undefined) return { found: false, value: undefined }
	if (typeof globalThis.RadioNodeList === "function" && field instanceof globalThis.RadioNodeList) {
		return field.value === "" ? { found: false, value: undefined } : { found: true, value: field.value }
	}
	if (field.disabled === true) return { found: false, value: undefined }
	return { found: true, value: field }
}

function formControlHasValue(value) {
	if (typeof globalThis.HTMLInputElement !== "function" || !(value instanceof globalThis.HTMLInputElement)) return true
	const type = String(value.type || "text").toLowerCase()
	if (type === "file") {
		return value.files !== null && value.files !== undefined && value.files.length > 0
	}
	if (type === "number" || type === "range") return Number.isFinite(value.valueAsNumber)
	if (type === "date") return value.valueAsDate instanceof Date && !Number.isNaN(value.valueAsDate.getTime())
	if (type === "datetime-local") return typeof value.value === "string" && value.value !== ""
	return true
}

function formatMissingNames(names) {
	const quoted = names.map(name => JSON.stringify(name))
	if (quoted.length <= 1) return quoted[0] ?? ""
	if (quoted.length === 2) return `${quoted[0]} and ${quoted[1]}`
	return `${quoted.slice(0, -1).join(", ")}, and ${quoted.at(-1)}`
}

function normalizeReconnect(value) {
	if (value === undefined || value === true) return { ...DEFAULT_RECONNECT }
	if (value === false) return { ...DEFAULT_RECONNECT, enabled: false }
	if (value === null || typeof value !== "object" || Array.isArray(value)) {
		throw new TypeError("Socket reconnect must be a boolean or an object")
	}
	const config = { ...DEFAULT_RECONNECT, ...value }
	if (typeof config.enabled !== "boolean") {
		throw new TypeError("Socket reconnect.enabled must be a boolean")
	}
	if (!Number.isFinite(config.delay) || config.delay < 0) {
		throw new TypeError("Socket reconnect.delay must be a non-negative number")
	}
	if (!Number.isFinite(config.maxDelay) || config.maxDelay < config.delay) {
		throw new TypeError("Socket reconnect.maxDelay must be greater than or equal to delay")
	}
	if (!Number.isInteger(config.attempts) || config.attempts < 0) {
		throw new TypeError("Socket reconnect.attempts must be a non-negative integer")
	}
	return config
}

function normalizeDisconnectCode(code) {
	if (code === WSCloseCode.DEFAULT) code = WSCloseCode.NORMAL
	if (!Number.isInteger(code) || !validCloseCode(code)) {
		throw new TypeError("Socket disconnect code is not a valid WebSocket close code")
	}
	return code
}

function validCloseCode(code) {
	if (code >= 3000 && code <= 4999) return true
	return [1000, 1001, 1002, 1003, 1007, 1008, 1009, 1011, 1012, 1013].includes(code)
}

function closeCodeShouldReconnect(code) {
	return [1001, 1006, 1011, 1012, 1013, 4001].includes(code)
}

function closeReason(code) {
	switch (code) {
	case WSCloseCode.NORMAL: return "Normal closure"
	case WSCloseCode.GOING_AWAY: return "Going away"
	case WSCloseCode.PROTOCOL: return "Protocol error"
	case WSCloseCode.UNSUPPORTED: return "Unsupported data"
	case WSCloseCode.INVALID_DATA: return "Invalid payload data"
	case WSCloseCode.POLICY: return "Policy violation"
	case WSCloseCode.MESSAGE_LARGE: return "Message too large"
	case WSCloseCode.INTERNAL: return "Internal server error"
	case WSCloseCode.RESTART: return "Server restarting"
	case WSCloseCode.TRY_LATER: return "Try again later"
	case WSCloseCode.PERMANENT: return "Permanent disconnection"
	case WSCloseCode.RECONNECT: return "Reconnect requested"
	case WSCloseCode.SESSION_EXPIRED: return "Session expired"
	case WSCloseCode.SESSION_DESTROYED: return "Session destroyed"
	default: return "Connection closed"
	}
}

function resolveWebSocketURL(origin) {
	const source = origin === undefined ? globalThis.location?.origin : origin
	if (typeof source !== "string" || source.length === 0) {
		throw new TypeError("Socket origin must be a non-empty string")
	}

	const url = new URL(HANDSHAKE_PATH, source)
	switch (url.protocol) {
	case "http:":
		url.protocol = "ws:"
		break
	case "https:":
		url.protocol = "wss:"
		break
	case "ws:":
	case "wss:":
		break
	default:
		throw new TypeError(`Socket origin protocol "${url.protocol}" is not supported`)
	}
	return url.href
}
