import assert from "node:assert/strict"
import test from "node:test"

import {
	GoxProtocol,
	GoxProtocolError,
	decodeBigInt,
	decodeCompactUint,
	decodeFileReference,
	decodeFilesReference,
	decodeGoxPacket,
	encodeCompactUint,
	encodeBigInt,
	encodeFileReference,
	encodeFilesReference,
	encodeGoxPacket,
} from "./client.js"

const bytes = (...values) => Uint8Array.from(values)
const utf8 = value => new TextEncoder().encode(value)
const eventHeaders = (name, id = 1) => [
	{ type: GoxProtocol.HeaderType.EVENT_NAME, value: utf8(name) },
	{ type: GoxProtocol.HeaderType.EVENT_ID, value: encodeCompactUint(id) },
]

test("compact uint uses the canonical GOX vectors", () => {
	for (const [value, expected] of [
		[0n, bytes(0x00)],
		[7n, bytes(0x01, 0x07)],
		[255n, bytes(0x01, 0xff)],
		[256n, bytes(0x02, 0x01, 0x00)],
		[300n, bytes(0x02, 0x01, 0x2c)],
		[0xffffffffffffffffn, bytes(0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)],
	]) {
		const encoded = encodeCompactUint(value)
		assert.deepEqual(encoded, expected)
		assert.deepEqual(decodeCompactUint(encoded), { value, bytesRead: encoded.length })
	}
})

test("compact uint rejects every non-canonical form", () => {
	for (const encoded of [
		bytes(),
		bytes(0x09),
		bytes(0x02, 0x01),
		bytes(0x01, 0x00),
		bytes(0x02, 0x00, 0x01),
	]) {
		assert.throws(() => decodeCompactUint(encoded), GoxProtocolError)
	}
})

test("EVENT encoding matches the Go protocol vector byte for byte", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const A = GoxProtocol.ArgumentType
	const packet = {
		type: P.EVENT,
		headers: eventHeaders("sum"),
		arguments: [
			{ type: A.NUMBER, data: utf8("10") },
			{ type: A.NUMBER, data: utf8("20") },
		],
	}
	const expected = bytes(
		0x01, 0x01, 0x00, 0x02,
		0x01, 0x03, 0x73, 0x75, 0x6d,
		0x09, 0x02, 0x01, 0x01,
		0x02,
		0x02, 0x01, 0x02, 0x31, 0x30,
		0x02, 0x01, 0x02, 0x32, 0x30,
	)
	const encoded = encodeGoxPacket(packet)
	assert.deepEqual(encoded, expected)
	assert.deepEqual(encodeGoxPacket(decodeGoxPacket(encoded)), expected)
})

test("Channel packets round-trip with the same strict binary envelope as Go", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const A = GoxProtocol.ArgumentType
	const channel = { type: H.CHANNEL_ID, value: utf8("Chat#room") }
	const packets = [
		{ type: P.CHANNEL_JOIN, headers: [channel], arguments: [{ type: A.TEXT, data: utf8("token") }] },
		{ type: P.CHANNEL_ACCEPTED, headers: [channel] },
		{ type: P.CHANNEL_REJECTED, headers: [channel] },
		{ type: P.CHANNEL_LEAVE, headers: [channel, { type: H.CHANNEL_REASON, value: utf8("removed") }] },
		{ type: P.CHANNEL_EVENT, headers: [{ type: H.EVENT_NAME, value: utf8("message") }, { type: H.EVENT_ID, value: encodeCompactUint(1) }, channel], arguments: [{ type: A.TEXT, data: utf8("hello") }] },
		{ type: P.CHANNEL_REQUEST, headers: [{ type: H.EVENT_NAME, value: utf8("profile") }, { type: H.REQUEST_ID, value: encodeCompactUint(2) }, channel], arguments: [] },
		{ type: P.CHANNEL_RESPONSE, headers: [{ type: H.REQUEST_ID, value: encodeCompactUint(2) }, channel], arguments: [{ type: A.TEXT, data: utf8("ok") }] },
		{ type: P.CHANNEL_ERROR, headers: [{ type: H.REQUEST_ID, value: encodeCompactUint(2) }, { type: H.ERROR_CODE, value: utf8("HANDLER_FAILED") }, channel], arguments: [{ type: A.TEXT, data: utf8("failed") }] },
	]
	for (const packet of packets) {
		const encoded = encodeGoxPacket(packet)
		assert.deepEqual(encodeGoxPacket(decodeGoxPacket(encoded)), encoded)
	}
})

test("Channel packets reject invalid flags, identifiers and correlations", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const A = GoxProtocol.ArgumentType
	const channel = value => ({ type: H.CHANNEL_ID, value: utf8(value) })
	for (const packet of [
		{ type: P.CHANNEL_JOIN, flags: 1, headers: [channel("Chat")], arguments: [] },
		{ type: P.CHANNEL_JOIN, headers: [channel(" Chat")], arguments: [] },
		{ type: P.CHANNEL_JOIN, headers: [channel("Chat\nroom")], arguments: [] },
		{ type: P.CHANNEL_EVENT, headers: [{ type: H.EVENT_NAME, value: utf8("message") }, channel("Chat")], arguments: [] },
		{ type: P.CHANNEL_ERROR, headers: [{ type: H.ERROR_CODE, value: utf8("FAILED") }, channel("Chat")], arguments: [{ type: A.TEXT, data: utf8("failed") }] },
		{ type: P.CHANNEL_ERROR, headers: [{ type: H.EVENT_NAME, value: utf8("message") }, { type: H.REQUEST_ID, value: encodeCompactUint(1) }, { type: H.ERROR_CODE, value: utf8("FAILED") }, { type: H.EVENT_ID, value: encodeCompactUint(2) }, channel("Chat")], arguments: [{ type: A.TEXT, data: utf8("failed") }] },
	]) assert.throws(() => encodeGoxPacket(packet), GoxProtocolError)
})

test("packet decoder rejects unknown, truncated and trailing data", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const A = GoxProtocol.ArgumentType
	const valid = encodeGoxPacket({
		type: P.EVENT,
		headers: eventHeaders("event"),
		arguments: [{ type: A.NULL, data: bytes() }],
	})
	const replace = (index, value) => {
		const result = valid.slice()
		result[index] = value
		return result
	}
	for (const encoded of [
		replace(0, 0xff),
		replace(1, 0xff),
		replace(2, 0x80),
		replace(4, 0xff),
		valid.slice(0, 5),
		Uint8Array.from([...valid.slice(0, -2), 0xff, 0x00]),
		Uint8Array.from([...valid, 0x00]),
	]) {
		assert.throws(() => decodeGoxPacket(encoded), GoxProtocolError)
	}
})

test("unknown, duplicate, forbidden and unordered headers are fatal", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const requestID = encodeCompactUint(1)
	for (const headers of [
		[{ type: 0xff, value: bytes(1) }],
		[{ type: H.EVENT_NAME, value: utf8("x") }, { type: H.EVENT_NAME, value: utf8("y") }],
		[{ type: H.EVENT_NAME, value: utf8("x") }, { type: H.ERROR_CODE, value: utf8("E") }],
		[{ type: H.REQUEST_ID, value: requestID }, { type: H.EVENT_NAME, value: utf8("x") }],
	]) {
		assert.throws(() => encodeGoxPacket({ type: P.REQUEST, headers, arguments: [] }), GoxProtocolError)
	}
})

test("EVENT and ERROR correlation headers are strict", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const A = GoxProtocol.ArgumentType
	assert.throws(() => encodeGoxPacket({
		type: P.EVENT,
		headers: [{ type: H.EVENT_NAME, value: utf8("user") }],
		arguments: [],
	}), GoxProtocolError)
	const valid = {
		type: P.ERROR,
		headers: [
			{ type: H.EVENT_NAME, value: utf8("user") },
			{ type: H.ERROR_CODE, value: utf8("BIND_FAILED") },
			{ type: H.EVENT_ID, value: encodeCompactUint(7) },
		],
		arguments: [{ type: A.JSON, data: utf8('{"message":"failed"}') }],
	}
	assert.doesNotThrow(() => encodeGoxPacket(valid))
	assert.throws(() => encodeGoxPacket({
		...valid,
		headers: [
			valid.headers[0],
			{ type: H.REQUEST_ID, value: encodeCompactUint(1) },
			...valid.headers.slice(1),
		],
	}), GoxProtocolError)
})

test("every argument type round-trips without losing bytes", () => {
	const A = GoxProtocol.ArgumentType
	const args = [
		{ type: A.NULL, data: bytes() },
		{ type: A.TEXT, data: utf8("Hola, GOX") },
		{ type: A.NUMBER, data: utf8("-12.5e+2") },
		{ type: A.BOOLEAN, data: bytes(1) },
		{ type: A.DATE, data: utf8("2026-07-15T12:34:56.789Z") },
		{ type: A.JSON, data: utf8('{"name":"GOX","ready":true}') },
		{ type: A.BINARY, data: bytes(0x00, 0x7f, 0x80, 0xff) },
		{ type: A.FILE, data: encodeFileReference(7) },
		{ type: A.FILES, data: encodeFilesReference([8, 300]) },
		{ type: A.BIGINT, data: encodeBigInt(1234567890123456789012345678901234567890n) },
		{ type: A.HTML, data: utf8("<h1>Hola, GOX</h1>") },
	]
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const decoded = decodeGoxPacket(encodeGoxPacket({
		type: P.EVENT,
		headers: eventHeaders("all-types"),
		arguments: args,
	}))
	assert.deepEqual(decoded.arguments, args)
	assert.equal(decodeFileReference(decoded.arguments[7].data), 7n)
	assert.deepEqual(decodeFilesReference(decoded.arguments[8].data), [8n, 300n])
	assert.equal(decodeBigInt(decoded.arguments[9].data), 1234567890123456789012345678901234567890n)
})

test("every structured argument type rejects malformed data", () => {
	const A = GoxProtocol.ArgumentType
	for (const argument of [
		{ type: A.NULL, data: bytes(0) },
		{ type: A.TEXT, data: bytes(0xff) },
		{ type: A.HTML, data: bytes(0xff) },
		{ type: A.NUMBER, data: utf8("01") },
		{ type: A.BOOLEAN, data: bytes(2) },
		{ type: A.DATE, data: utf8("July 15") },
		{ type: A.JSON, data: utf8('{"open":') },
		{ type: A.FILE, data: bytes(0) },
		{ type: A.FILE, data: bytes(1, 7, 0) },
		{ type: A.FILES, data: bytes() },
		{ type: A.FILES, data: bytes(2, 1, 7) },
		{ type: A.BIGINT, data: bytes() },
		{ type: A.BIGINT, data: utf8("01") },
		{ type: A.BIGINT, data: utf8("+1") },
		{ type: A.BIGINT, data: utf8("-0") },
		{ type: A.BIGINT, data: utf8("1.5") },
		{ type: A.BIGINT, data: utf8("1e3") },
	]) {
		assert.throws(() => encodeGoxPacket({
			type: GoxProtocol.PacketType.EVENT,
			headers: eventHeaders("invalid"),
			arguments: [argument],
		}), GoxProtocolError)
	}
	assert.doesNotThrow(() => encodeGoxPacket({
		type: GoxProtocol.PacketType.EVENT,
		headers: eventHeaders("binary"),
		arguments: [{ type: A.BINARY, data: bytes(0xff, 0x00) }],
	}))
})

test("BIGINT supports values beyond uint64 in both directions", () => {
	for (const value of [
		0n,
		-1n,
		18446744073709551616n,
		12345678901234567890123456789012345678901234567890n,
		-12345678901234567890123456789012345678901234567890n,
	]) {
		const encoded = encodeBigInt(value)
		assert.equal(new TextDecoder().decode(encoded), value.toString())
		assert.equal(decodeBigInt(encoded), value)
	}
	assert.throws(() => encodeBigInt(42), TypeError)
})

test("BIGINT packet matches the Go protocol vector byte for byte", () => {
	const encoded = encodeGoxPacket({
		type: GoxProtocol.PacketType.EVENT,
		headers: eventHeaders("bigint"),
		arguments: [{
			type: GoxProtocol.ArgumentType.BIGINT,
			data: encodeBigInt(18446744073709551616n),
		}],
	})
	const expected = Uint8Array.from([
		0x01, 0x01, 0x00, 0x02,
		0x01, 0x06, ...utf8("bigint"),
		0x09, 0x02, 0x01, 0x01,
		0x01, 0x09, 0x01, 0x14,
		...utf8("18446744073709551616"),
	])
	assert.deepEqual(encoded, expected)
})

test("JSON arrays round-trip as standard JSON", () => {
	const data = utf8('[1,"GOX",true,null,{"id":7},[2,3]]')
	const packet = {
		type: GoxProtocol.PacketType.EVENT,
		headers: eventHeaders("json-array"),
		arguments: [{ type: GoxProtocol.ArgumentType.JSON, data }],
	}
	assert.deepEqual(decodeGoxPacket(encodeGoxPacket(packet)).arguments[0].data, data)
})

test("file transfers preserve names, sizes, MIME types and FILES order", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	for (const transfer of [
		{ id: 8, name: "avatar.png", mime: "image/png", size: 1024 },
		{ id: 300, name: "documento final.pdf", mime: "application/pdf", size: 65536 },
	]) {
		const decoded = decodeGoxPacket(encodeGoxPacket({
			type: P.TRANSFER_BEGIN,
			headers: [
				{ type: H.TRANSFER_ID, value: encodeCompactUint(transfer.id) },
				{ type: H.TOTAL_SIZE, value: encodeCompactUint(transfer.size) },
				{ type: H.FILE_NAME, value: utf8(transfer.name) },
				{ type: H.MIME_TYPE, value: utf8(transfer.mime) },
			],
			payload: bytes(),
		}))
		assert.equal(decodeCompactUint(decoded.headers[0].value).value, BigInt(transfer.id))
		assert.equal(decodeCompactUint(decoded.headers[1].value).value, BigInt(transfer.size))
		assert.equal(new TextDecoder().decode(decoded.headers[2].value), transfer.name)
		assert.equal(new TextDecoder().decode(decoded.headers[3].value), transfer.mime)
	}
	assert.deepEqual(decodeFilesReference(encodeFilesReference([8, 300])), [8n, 300n])
})

test("file transfer requires a valid name and MIME type", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const base = [
		{ type: H.TRANSFER_ID, value: encodeCompactUint(1) },
		{ type: H.TOTAL_SIZE, value: encodeCompactUint(10) },
	]
	for (const headers of [
		base,
		[...base,
			{ type: H.FILE_NAME, value: bytes(0xff) },
			{ type: H.MIME_TYPE, value: utf8("application/octet-stream") }],
	]) {
		assert.throws(() => encodeGoxPacket({ type: P.TRANSFER_BEGIN, headers, payload: bytes() }), GoxProtocolError)
	}
})

test("TRANSFER_ABORT is correlated and cannot carry bytes", () => {
	const P = GoxProtocol.PacketType
	const H = GoxProtocol.HeaderType
	const packet = {
		type: P.TRANSFER_ABORT,
		headers: [
			{ type: H.TRANSFER_ID, value: encodeCompactUint(9) },
			{ type: H.ERROR_CODE, value: utf8("CANCELLED") },
		],
		payload: bytes(),
	}
	assert.deepEqual(decodeGoxPacket(encodeGoxPacket(packet)).payload, bytes())
	assert.throws(() => encodeGoxPacket({ ...packet, payload: bytes(1) }), GoxProtocolError)
})
