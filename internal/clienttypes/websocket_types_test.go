package clienttypes

import (
	"strings"
	"testing"
)

func TestEmbeddedWebSocketDeclarationMatchesPublicAPI(t *testing.T) {
	data, err := staticTypes.ReadFile("static/websocket.d.ts")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	required := []string{
		`declare module "@gox/websocket"`,
		`VERSION: 1`,
		`CHANNEL_ERROR: 39`,
		`HTML: 10`,
		`export class GoxProtocolError extends Error`,
		`export function decodeCompactUint(input: GoxBytes, offset?: number): { value: bigint; bytesRead: number }`,
		`SESSION_DESTROYED: 4003`,
		`SESSION_EXPIRED: "SESSION_EXPIRED"`,
		`export class GoxSocketError extends Error`,
		`export class GoxChannelError extends GoxSocketError`,
		`REJECTED: "rejected"`,
		`readonly connectionState: ChannelStateValue`,
		`emit<T = unknown, Event extends string = string>(event: Event & (Event extends keyof GoxChannelEmitEvents<Name> ? never : unknown), ...values: GoxValue[]): ChannelEmission<T>`,
		`readonly Channel: SocketChannelConstructor`,
		`declare module "@gox/websocket-events"`,
		`export interface SocketClientEmitEvents {}`,
		`export interface SocketClientOnEvents {}`,
		`export interface ChannelClientEmitEvents {}`,
		`export interface ChannelClientOnEvents {}`,
		`type GoxSocketClientEmitEvents = import("@gox/websocket-events").SocketClientEmitEvents`,
		`type GoxIncomingHandler<Contract>`,
		"type GoxChannelName<ID extends string> = ID extends `" + "${infer Name}#${string}" + "` ? Name : ID",
		`export class Channel<Name extends string = string>`,
		`on<Event extends keyof GoxChannelOnEvents<Name> & string>(event: Event, handler: GoxIncomingHandler<GoxChannelOnEvents<Name>[Event]>): this`,
		`emit<Event extends keyof GoxChannelEmitEvents<Name> & string>(event: Event, ...values: GoxEventArgs<GoxChannelEmitEvents<Name>[Event]>): ChannelEmission<GoxChannelEmissionReply<GoxChannelEmitEvents<Name>[Event]>>`,
		`new <ID extends string>(id: ID, ...validation: GoxValue[]): Channel<GoxChannelName<ID>>`,
		`on<Event extends keyof GoxSocketClientOnEvents & string>(event: Event, handler: GoxIncomingHandler<GoxSocketClientOnEvents[Event]>): this`,
		`emit<Event extends keyof GoxSocketClientEmitEvents & string>(event: Event, ...values: GoxEventArgs<GoxSocketClientEmitEvents[Event]>): Promise<GoxOutgoingReply<GoxSocketClientEmitEvents[Event]>>`,
		`emit<T = unknown, Event extends string = string>(event: Event & (Event extends keyof GoxSocketClientEmitEvents ? never : unknown), ...values: GoxValue[]): Promise<T | undefined>`,
		`disconnect(code?: number): this`,
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Fatalf("websocket declaration is missing %q", want)
		}
	}

	for _, loose := range []string{
		`Readonly<Record<string, unknown>>`,
		`(...args: any[])`,
		`...values: any[]`,
	} {
		if strings.Contains(text, loose) {
			t.Fatalf("websocket declaration still contains loose API typing %q", loose)
		}
	}
}
