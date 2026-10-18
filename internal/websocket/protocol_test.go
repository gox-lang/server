package websocket

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
)

func testEventHeaders(name string) []Header {
	return []Header{
		{Type: HeaderEventName, Value: []byte(name)},
		{Type: HeaderEventID, Value: EncodeCompactUint(1)},
	}
}

func TestChannelPacketsRoundTripWithStrictNamespaceHeaders(t *testing.T) {
	packets := []Packet{
		{Type: PacketChannelJoin, Headers: []Header{{Type: HeaderChannelID, Value: []byte("Chat#room")}}, Arguments: []Argument{{Type: ArgumentText, Data: []byte("token")}}},
		{Type: PacketChannelAccepted, Headers: []Header{{Type: HeaderChannelID, Value: []byte("Chat#room")}}},
		{Type: PacketChannelRejected, Headers: []Header{{Type: HeaderChannelID, Value: []byte("Chat#room")}}},
		{Type: PacketChannelLeave, Headers: []Header{{Type: HeaderChannelID, Value: []byte("Chat#room")}, {Type: HeaderChannelReason, Value: []byte("removed")}}},
		{Type: PacketChannelEvent, Headers: []Header{{Type: HeaderEventName, Value: []byte("message")}, {Type: HeaderEventID, Value: EncodeCompactUint(1)}, {Type: HeaderChannelID, Value: []byte("Chat#room")}}, Arguments: []Argument{{Type: ArgumentText, Data: []byte("hello")}}},
		{Type: PacketChannelRequest, Headers: []Header{{Type: HeaderEventName, Value: []byte("profile")}, {Type: HeaderRequestID, Value: EncodeCompactUint(2)}, {Type: HeaderChannelID, Value: []byte("Chat#room")}}, Arguments: []Argument{}},
		{Type: PacketChannelResponse, Headers: []Header{{Type: HeaderRequestID, Value: EncodeCompactUint(2)}, {Type: HeaderChannelID, Value: []byte("Chat#room")}}, Arguments: []Argument{{Type: ArgumentText, Data: []byte("ok")}}},
		{Type: PacketChannelError, Headers: []Header{{Type: HeaderRequestID, Value: EncodeCompactUint(2)}, {Type: HeaderErrorCode, Value: []byte("HANDLER_FAILED")}, {Type: HeaderChannelID, Value: []byte("Chat#room")}}, Arguments: []Argument{{Type: ArgumentText, Data: []byte("failed")}}},
	}
	for _, packet := range packets {
		encoded, err := EncodePacket(packet)
		if err != nil {
			t.Fatalf("encode 0x%02x: %v", packet.Type, err)
		}
		decoded, err := DecodePacket(encoded)
		if err != nil {
			t.Fatalf("decode 0x%02x: %v", packet.Type, err)
		}
		reencoded, err := EncodePacket(decoded)
		if err != nil || !bytes.Equal(encoded, reencoded) {
			t.Fatalf("packet 0x%02x is not canonical", packet.Type)
		}
	}
}

func TestChannelPacketsRejectMissingForbiddenAndTrailingData(t *testing.T) {
	invalid := []Packet{
		{Type: PacketChannelJoin, Headers: nil, Arguments: nil},
		{Type: PacketChannelAccepted, Flags: FlagInternal, Headers: []Header{{Type: HeaderChannelID, Value: []byte("Chat")}}},
		{Type: PacketChannelRejected, Headers: []Header{{Type: HeaderChannelID, Value: []byte("Chat")}}, Payload: []byte{1}},
		{Type: PacketChannelEvent, Headers: []Header{{Type: HeaderEventName, Value: []byte("message")}, {Type: HeaderChannelID, Value: []byte("Chat")}}, Arguments: nil},
		{Type: PacketChannelError, Headers: []Header{{Type: HeaderErrorCode, Value: []byte("FAILED")}, {Type: HeaderChannelID, Value: []byte("Chat")}}, Arguments: nil},
		{Type: PacketChannelError, Headers: []Header{{Type: HeaderEventName, Value: []byte("message")}, {Type: HeaderRequestID, Value: EncodeCompactUint(1)}, {Type: HeaderErrorCode, Value: []byte("FAILED")}, {Type: HeaderEventID, Value: EncodeCompactUint(2)}, {Type: HeaderChannelID, Value: []byte("Chat")}}, Arguments: nil},
	}
	for index, packet := range invalid {
		if _, err := EncodePacket(packet); !errors.Is(err, ErrMalformedPacket) {
			t.Fatalf("invalid Channel packet %d: %v", index, err)
		}
	}
}

func TestChannelPacketsRejectInvalidChannelIdentifiers(t *testing.T) {
	for _, channelID := range []string{" Chat", "Chat ", "Chat\nroom", ""} {
		packet := Packet{Type: PacketChannelJoin, Headers: []Header{{Type: HeaderChannelID, Value: []byte(channelID)}}}
		if _, err := EncodePacket(packet); !errors.Is(err, ErrMalformedPacket) {
			t.Fatalf("Channel ID %q was accepted: %v", channelID, err)
		}
	}
}

func TestCompactUintCanonicalVectors(t *testing.T) {
	vectors := []struct {
		value uint64
		bytes []byte
	}{
		{0, []byte{0x00}},
		{7, []byte{0x01, 0x07}},
		{255, []byte{0x01, 0xff}},
		{256, []byte{0x02, 0x01, 0x00}},
		{300, []byte{0x02, 0x01, 0x2c}},
		{1<<64 - 1, []byte{0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, vector := range vectors {
		encoded := EncodeCompactUint(vector.value)
		if !bytes.Equal(encoded, vector.bytes) {
			t.Fatalf("EncodeCompactUint(%d) = %x, want %x", vector.value, encoded, vector.bytes)
		}
		decoded, consumed, err := DecodeCompactUint(encoded)
		if err != nil || decoded != vector.value || consumed != len(encoded) {
			t.Fatalf("DecodeCompactUint(%x) = %d, %d, %v", encoded, decoded, consumed, err)
		}
	}
}

func TestCompactUintRejectsMalformedValues(t *testing.T) {
	for _, encoded := range [][]byte{
		nil,
		{0x09},
		{0x02, 0x01},
		{0x01, 0x00},
		{0x02, 0x00, 0x01},
	} {
		if _, _, err := DecodeCompactUint(encoded); !errors.Is(err, ErrMalformedPacket) {
			t.Fatalf("DecodeCompactUint(%x) error = %v", encoded, err)
		}
	}
}

func TestEventPacketCanonicalVector(t *testing.T) {
	packet := Packet{
		Type:    PacketEvent,
		Headers: testEventHeaders("sum"),
		Arguments: []Argument{
			{Type: ArgumentNumber, Data: []byte("10")},
			{Type: ArgumentNumber, Data: []byte("20")},
		},
	}
	want := []byte{
		0x01, 0x01, 0x00, 0x02,
		0x01, 0x03, 's', 'u', 'm',
		0x09, 0x02, 0x01, 0x01,
		0x02,
		0x02, 0x01, 0x02, '1', '0',
		0x02, 0x01, 0x02, '2', '0',
	}
	encoded, err := EncodePacket(packet)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("packet = %x, want %x", encoded, want)
	}
	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodePacket(decoded)
	if err != nil || !bytes.Equal(reencoded, want) {
		t.Fatalf("round trip = %x, %v", reencoded, err)
	}
}

func TestPacketDecoderIsStrict(t *testing.T) {
	valid, err := EncodePacket(Packet{
		Type:      PacketEvent,
		Headers:   testEventHeaders("event"),
		Arguments: []Argument{{Type: ArgumentNull}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown packet":        append([]byte{0xff}, valid[1:]...),
		"unknown version":       append([]byte{valid[0], 0xff}, valid[2:]...),
		"unknown flag":          append([]byte{valid[0], valid[1], 0x80}, valid[3:]...),
		"unknown header":        append(append([]byte(nil), valid[:4]...), append([]byte{0xff}, valid[5:]...)...),
		"truncated header":      valid[:5],
		"unknown argument":      append(append([]byte(nil), valid[:len(valid)-2]...), 0xff, 0x00),
		"trailing payload byte": append(append([]byte(nil), valid...), 0x00),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePacket(encoded); !errors.Is(err, ErrMalformedPacket) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPacketRejectsDuplicateForbiddenAndOutOfOrderHeaders(t *testing.T) {
	requestID := EncodeCompactUint(1)
	for name, headers := range map[string][]Header{
		"duplicate": {
			{Type: HeaderEventName, Value: []byte("x")},
			{Type: HeaderEventName, Value: []byte("y")},
		},
		"forbidden": {
			{Type: HeaderEventName, Value: []byte("x")},
			{Type: HeaderErrorCode, Value: []byte("E")},
		},
		"out of order": {
			{Type: HeaderRequestID, Value: requestID},
			{Type: HeaderEventName, Value: []byte("x")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := EncodePacket(Packet{Type: PacketRequest, Headers: headers})
			if !errors.Is(err, ErrMalformedPacket) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestEventAndErrorCorrelationHeadersAreStrict(t *testing.T) {
	if _, err := EncodePacket(Packet{Type: PacketEvent, Headers: []Header{{Type: HeaderEventName, Value: []byte("user")}}}); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("EVENT without event ID error = %v", err)
	}
	details := []byte(`{"message":"failed"}`)
	valid := Packet{
		Type: PacketError,
		Headers: []Header{
			{Type: HeaderEventName, Value: []byte("user")},
			{Type: HeaderErrorCode, Value: []byte("BIND_FAILED")},
			{Type: HeaderEventID, Value: EncodeCompactUint(7)},
		},
		Arguments: []Argument{{Type: ArgumentJSON, Data: details}},
	}
	if _, err := EncodePacket(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Headers = []Header{
		valid.Headers[0],
		{Type: HeaderRequestID, Value: EncodeCompactUint(1)},
		valid.Headers[1],
		valid.Headers[2],
	}
	if _, err := EncodePacket(invalid); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("ERROR with two correlation IDs error = %v", err)
	}
}

func TestEveryArgumentTypeRoundTrips(t *testing.T) {
	file, err := EncodeFileReference(7)
	if err != nil {
		t.Fatal(err)
	}
	files, err := EncodeFilesReference([]uint64{8, 300})
	if err != nil {
		t.Fatal(err)
	}
	bigValue, ok := new(big.Int).SetString("1234567890123456789012345678901234567890", 10)
	if !ok {
		t.Fatal("failed to create test big integer")
	}
	bigInteger, err := EncodeBigInt(bigValue)
	if err != nil {
		t.Fatal(err)
	}
	arguments := []Argument{
		{Type: ArgumentNull},
		{Type: ArgumentText, Data: []byte("Hola, GOX")},
		{Type: ArgumentNumber, Data: []byte("-12.5e+2")},
		{Type: ArgumentBoolean, Data: []byte{1}},
		{Type: ArgumentDate, Data: []byte("2026-07-15T12:34:56.789Z")},
		{Type: ArgumentJSON, Data: []byte(`{"name":"GOX","ready":true}`)},
		{Type: ArgumentBinary, Data: []byte{0x00, 0x7f, 0x80, 0xff}},
		{Type: ArgumentFile, Data: file},
		{Type: ArgumentFiles, Data: files},
		{Type: ArgumentBigInt, Data: bigInteger},
		{Type: ArgumentHTML, Data: []byte(`<h1>Hola, GOX</h1>`)},
	}
	encoded, err := EncodeArguments(arguments)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeArguments(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(arguments) {
		t.Fatalf("decoded %d arguments, want %d", len(decoded), len(arguments))
	}
	for index := range arguments {
		if decoded[index].Type != arguments[index].Type || !bytes.Equal(decoded[index].Data, arguments[index].Data) {
			t.Fatalf("argument %d = %#v, want %#v", index, decoded[index], arguments[index])
		}
	}
	if transferID, err := DecodeFileReference(decoded[7].Data); err != nil || transferID != 7 {
		t.Fatalf("file transfer ID = %d, %v", transferID, err)
	}
	if transferIDs, err := DecodeFilesReference(decoded[8].Data); err != nil || len(transferIDs) != 2 || transferIDs[0] != 8 || transferIDs[1] != 300 {
		t.Fatalf("files transfer IDs = %v, %v", transferIDs, err)
	}
	if decodedBigInt, err := DecodeBigInt(decoded[9].Data); err != nil || decodedBigInt.Cmp(bigValue) != 0 {
		t.Fatalf("big integer = %v, %v", decodedBigInt, err)
	}
}

func TestEveryArgumentTypeRejectsMalformedData(t *testing.T) {
	tests := map[string]Argument{
		"null with data":       {Type: ArgumentNull, Data: []byte{0}},
		"text invalid UTF-8":   {Type: ArgumentText, Data: []byte{0xff}},
		"HTML invalid UTF-8":   {Type: ArgumentHTML, Data: []byte{0xff}},
		"number invalid":       {Type: ArgumentNumber, Data: []byte("01")},
		"boolean invalid":      {Type: ArgumentBoolean, Data: []byte{2}},
		"date invalid":         {Type: ArgumentDate, Data: []byte("July 15")},
		"JSON invalid":         {Type: ArgumentJSON, Data: []byte(`{"open":`)},
		"file ID zero":         {Type: ArgumentFile, Data: []byte{0}},
		"file trailing data":   {Type: ArgumentFile, Data: []byte{1, 7, 0}},
		"files missing count":  {Type: ArgumentFiles},
		"files count mismatch": {Type: ArgumentFiles, Data: []byte{2, 1, 7}},
		"bigint empty":         {Type: ArgumentBigInt},
		"bigint leading zero":  {Type: ArgumentBigInt, Data: []byte("01")},
		"bigint plus sign":     {Type: ArgumentBigInt, Data: []byte("+1")},
		"bigint negative zero": {Type: ArgumentBigInt, Data: []byte("-0")},
		"bigint decimal":       {Type: ArgumentBigInt, Data: []byte("1.5")},
		"bigint exponent":      {Type: ArgumentBigInt, Data: []byte("1e3")},
	}
	for name, argument := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := EncodeArguments([]Argument{argument}); !errors.Is(err, ErrMalformedPacket) {
				t.Fatalf("error = %v", err)
			}
		})
	}

	// Binary accepts every byte sequence, including empty and invalid UTF-8.
	if _, err := EncodeArguments([]Argument{{Type: ArgumentBinary, Data: []byte{0xff, 0x00}}}); err != nil {
		t.Fatalf("binary argument was rejected: %v", err)
	}
}

func TestBigIntSupportsValuesBeyondUint64InBothDirections(t *testing.T) {
	for _, source := range []string{
		"0",
		"-1",
		"18446744073709551616",
		"12345678901234567890123456789012345678901234567890",
		"-12345678901234567890123456789012345678901234567890",
	} {
		value, ok := new(big.Int).SetString(source, 10)
		if !ok {
			t.Fatalf("invalid test value %q", source)
		}
		encoded, err := EncodeBigInt(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != source {
			t.Fatalf("encoded %q, want %q", encoded, source)
		}
		decoded, err := DecodeBigInt(encoded)
		if err != nil || decoded.Cmp(value) != 0 {
			t.Fatalf("decoded %v, %v", decoded, err)
		}
	}
}

func TestBigIntPacketMatchesJavaScriptVector(t *testing.T) {
	value, _ := new(big.Int).SetString("18446744073709551616", 10)
	data, err := EncodeBigInt(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePacket(Packet{
		Type:      PacketEvent,
		Headers:   testEventHeaders("bigint"),
		Arguments: []Argument{{Type: ArgumentBigInt, Data: data}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{
		0x01, 0x01, 0x00, 0x02,
		0x01, 0x06, 'b', 'i', 'g', 'i', 'n', 't',
		0x09, 0x02, 0x01, 0x01,
		0x01, 0x09, 0x01, 0x14,
	}, []byte("18446744073709551616")...)
	if !bytes.Equal(encoded, want) {
		t.Fatalf("BIGINT packet = %x, want %x", encoded, want)
	}
}

func TestJSONArrayRoundTrips(t *testing.T) {
	data := []byte(`[1,"GOX",true,null,{"id":7},[2,3]]`)
	encoded, err := EncodeArguments([]Argument{{Type: ArgumentJSON, Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeArguments(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || !bytes.Equal(decoded[0].Data, data) {
		t.Fatalf("JSON array = %s", decoded[0].Data)
	}
}

func TestFileTransfersPreserveNamesSizesMIMETypesAndOrder(t *testing.T) {
	transfers := []struct {
		id       uint64
		name     string
		mimeType string
		size     uint64
	}{
		{id: 8, name: "avatar.png", mimeType: "image/png", size: 1024},
		{id: 300, name: "documento final.pdf", mimeType: "application/pdf", size: 65536},
	}
	for _, transfer := range transfers {
		encoded, err := EncodePacket(Packet{
			Type: PacketTransferBegin,
			Headers: []Header{
				{Type: HeaderTransferID, Value: EncodeCompactUint(transfer.id)},
				{Type: HeaderTotalSize, Value: EncodeCompactUint(transfer.size)},
				{Type: HeaderFileName, Value: []byte(transfer.name)},
				{Type: HeaderMIMEType, Value: []byte(transfer.mimeType)},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodePacket(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded.Headers[2].Value) != transfer.name || string(decoded.Headers[3].Value) != transfer.mimeType {
			t.Fatalf("metadata = %q, %q", decoded.Headers[2].Value, decoded.Headers[3].Value)
		}
		id, _, _ := DecodeCompactUint(decoded.Headers[0].Value)
		size, _, _ := DecodeCompactUint(decoded.Headers[1].Value)
		if id != transfer.id || size != transfer.size {
			t.Fatalf("transfer = id %d size %d, want id %d size %d", id, size, transfer.id, transfer.size)
		}
	}

	files, err := EncodeFilesReference([]uint64{8, 300})
	if err != nil {
		t.Fatal(err)
	}
	order, err := DecodeFilesReference(files)
	if err != nil || len(order) != 2 || order[0] != 8 || order[1] != 300 {
		t.Fatalf("file order = %v, %v", order, err)
	}
}

func TestFileTransferRequiresValidNameAndMIMEType(t *testing.T) {
	base := []Header{
		{Type: HeaderTransferID, Value: EncodeCompactUint(1)},
		{Type: HeaderTotalSize, Value: EncodeCompactUint(10)},
	}
	for name, headers := range map[string][]Header{
		"missing name and MIME": base,
		"invalid UTF-8 name": append(append([]Header(nil), base...),
			Header{Type: HeaderFileName, Value: []byte{0xff}},
			Header{Type: HeaderMIMEType, Value: []byte("application/octet-stream")}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := EncodePacket(Packet{Type: PacketTransferBegin, Headers: headers}); !errors.Is(err, ErrMalformedPacket) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestTransferAbortHasStrictCorrelationAndReason(t *testing.T) {
	packet := Packet{Type: PacketTransferAbort, Headers: []Header{
		{Type: HeaderTransferID, Value: EncodeCompactUint(9)},
		{Type: HeaderErrorCode, Value: []byte("CANCELLED")},
	}}
	encoded, err := EncodePacket(packet)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != PacketTransferAbort || HeaderText(decoded, HeaderErrorCode) != "CANCELLED" {
		t.Fatalf("abort packet = %#v", decoded)
	}
	packet.Payload = []byte{1}
	if _, err := EncodePacket(packet); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("abort payload error = %v", err)
	}
}
