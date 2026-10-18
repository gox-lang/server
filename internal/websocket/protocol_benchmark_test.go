package websocket

import "testing"

var (
	benchmarkEncodedPacket []byte
	benchmarkDecodedPacket Packet
	benchmarkArguments     []Argument
)

func benchmarkEventPacket(packetType PacketType) Packet {
	headers := []Header{
		{Type: HeaderEventName, Value: []byte("message")},
		{Type: HeaderEventID, Value: EncodeCompactUint(42)},
	}
	if isChannelPacket(packetType) {
		headers = append(headers, Header{Type: HeaderChannelID, Value: []byte("Chat#benchmark")})
	}
	return Packet{
		Type:    packetType,
		Headers: headers,
		Arguments: []Argument{
			{Type: ArgumentText, Data: []byte("hello websocket")},
			{Type: ArgumentNumber, Data: []byte("42")},
			{Type: ArgumentBoolean, Data: []byte{1}},
		},
	}
}

func BenchmarkEncodePacket(b *testing.B) {
	for _, benchmark := range []struct {
		name   string
		packet Packet
	}{
		{name: "SocketEventMixed", packet: benchmarkEventPacket(PacketEvent)},
		{name: "ChannelEventMixed", packet: benchmarkEventPacket(PacketChannelEvent)},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(benchmark.packet.Arguments[0].Data) + len(benchmark.packet.Arguments[1].Data) + len(benchmark.packet.Arguments[2].Data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				encoded, err := EncodePacket(benchmark.packet)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkEncodedPacket = encoded
			}
		})
	}
}

func BenchmarkDecodePacket(b *testing.B) {
	for _, benchmark := range []struct {
		name   string
		packet Packet
	}{
		{name: "SocketEventMixed", packet: benchmarkEventPacket(PacketEvent)},
		{name: "ChannelEventMixed", packet: benchmarkEventPacket(PacketChannelEvent)},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			encoded, err := EncodePacket(benchmark.packet)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(encoded)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				decoded, err := DecodePacket(encoded)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkDecodedPacket = decoded
			}
		})
	}
}

func BenchmarkEncodeValues(b *testing.B) {
	type message struct {
		Text string `json:"text"`
		ID   int    `json:"id"`
	}
	cases := []struct {
		name   string
		values []any
	}{
		{name: "Text", values: []any{"hello websocket"}},
		{name: "Mixed", values: []any{"hello websocket", 42, true}},
		{name: "JSONStruct", values: []any{message{Text: "hello websocket", ID: 42}}},
	}
	for _, benchmark := range cases {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				arguments, err := EncodeValues(benchmark.values)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkArguments = arguments
			}
		})
	}
}
