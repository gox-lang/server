declare module "@gox/websocket-events" {
  export interface SocketClientEmitEvents {}
  export interface SocketClientOnEvents {}
  export interface ChannelClientEmitEvents {}
  export interface ChannelClientOnEvents {}
}

declare module "@gox/websocket" {
  export type GoxBytes = ArrayBuffer | ArrayBufferView
  export type GoxValue =
    | null
    | string
    | number
    | boolean
    | bigint
    | Date
    | GoxBytes
    | File
    | File[]
    | FileList
    | Element
    | DocumentFragment
    | object

  type GoxSocketClientEmitEvents = import("@gox/websocket-events").SocketClientEmitEvents
  type GoxSocketClientOnEvents = import("@gox/websocket-events").SocketClientOnEvents
  type GoxChannelClientEmitEvents = import("@gox/websocket-events").ChannelClientEmitEvents
  type GoxChannelClientOnEvents = import("@gox/websocket-events").ChannelClientOnEvents
  type GoxEventArgs<Contract> = Contract extends { readonly args: infer Args extends readonly unknown[] } ? [...Args] : GoxValue[]
  type GoxEventReply<Contract> = Contract extends { readonly reply: infer Reply } ? Reply : never
  type GoxIncomingHandler<Contract> = [GoxEventReply<Contract>] extends [never]
    ? (...args: GoxEventArgs<Contract>) => GoxValue | Promise<GoxValue> | void
    : (...args: GoxEventArgs<Contract>) => GoxEventReply<Contract> | Promise<GoxEventReply<Contract>>
  type GoxOutgoingReply<Contract> = [GoxEventReply<Contract>] extends [never] ? undefined : GoxEventReply<Contract> | undefined
  type GoxChannelName<ID extends string> = ID extends `${infer Name}#${string}` ? Name : ID
  type GoxChannelEmitEvents<Name extends string> = Name extends keyof GoxChannelClientEmitEvents ? GoxChannelClientEmitEvents[Name] : {}
  type GoxChannelOnEvents<Name extends string> = Name extends keyof GoxChannelClientOnEvents ? GoxChannelClientOnEvents[Name] : {}
  type GoxChannelEmissionReply<Contract> = [GoxEventReply<Contract>] extends [never] ? unknown : GoxEventReply<Contract>

  export interface GoxHeader {
    type: number
    value: GoxBytes
  }

  export interface GoxArgument {
    type: number
    data?: GoxBytes
  }

  export interface GoxPacket {
    type: number
    flags?: number
    headers?: GoxHeader[]
    arguments?: GoxArgument[]
    payload?: GoxBytes
  }

  export const GoxProtocol: Readonly<{
    VERSION: 1
    PacketType: Readonly<{
      EVENT: 1
      REQUEST: 2
      RESPONSE: 3
      ERROR: 4
      TRANSFER_BEGIN: 16
      TRANSFER_CHUNK: 17
      TRANSFER_END: 18
      TRANSFER_ABORT: 19
      CHANNEL_JOIN: 32
      CHANNEL_ACCEPTED: 33
      CHANNEL_REJECTED: 34
      CHANNEL_LEAVE: 35
      CHANNEL_EVENT: 36
      CHANNEL_REQUEST: 37
      CHANNEL_RESPONSE: 38
      CHANNEL_ERROR: 39
    }>
    Flag: Readonly<{ INTERNAL: 1; TRANSFER: 2 }>
    HeaderType: Readonly<{
      EVENT_NAME: 1
      REQUEST_ID: 2
      TRANSFER_ID: 3
      SEQUENCE: 4
      TOTAL_SIZE: 5
      FILE_NAME: 6
      MIME_TYPE: 7
      ERROR_CODE: 8
      EVENT_ID: 9
      CHANNEL_ID: 10
      CHANNEL_REASON: 11
    }>
    ArgumentType: Readonly<{
      NULL: 0
      TEXT: 1
      NUMBER: 2
      BOOLEAN: 3
      DATE: 4
      JSON: 5
      BINARY: 6
      FILE: 7
      FILES: 8
      BIGINT: 9
      HTML: 10
    }>
  }>

  export class GoxProtocolError extends Error {
    constructor(message: string)
  }

  export function encodeCompactUint(input: number | bigint): Uint8Array
  export function decodeCompactUint(input: GoxBytes, offset?: number): { value: bigint; bytesRead: number }
  export function encodeFileReference(transferID: number | bigint): Uint8Array
  export function encodeFilesReference(transferIDs: Array<number | bigint>): Uint8Array
  export function decodeFileReference(input: GoxBytes): bigint
  export function decodeFilesReference(input: GoxBytes): bigint[]
  export function encodeBigInt(value: bigint): Uint8Array
  export function decodeBigInt(input: GoxBytes): bigint
  export function encodeGoxPacket(packet: GoxPacket): Uint8Array
  export function decodeGoxPacket(input: GoxBytes): GoxPacket
  export function encodeGoxArguments(args: GoxArgument[]): Uint8Array
  export function decodeGoxArguments(input: GoxBytes): Array<{ type: number; data: Uint8Array }>

  export const WSCloseCode: Readonly<{
    DEFAULT: 0
    NORMAL: 1000
    GOING_AWAY: 1001
    PROTOCOL: 1002
    UNSUPPORTED: 1003
    ABNORMAL: 1006
    INVALID_DATA: 1007
    POLICY: 1008
    MESSAGE_LARGE: 1009
    INTERNAL: 1011
    RESTART: 1012
    TRY_LATER: 1013
    PERMANENT: 4000
    RECONNECT: 4001
    SESSION_EXPIRED: 4002
    SESSION_DESTROYED: 4003
  }>

  export const WSDisconnectInitiator: Readonly<{
    CLIENT: "client"
    SERVER: "server"
    UNKNOWN: "unknown"
  }>
  export type WSDisconnectInitiatorValue = typeof WSDisconnectInitiator[keyof typeof WSDisconnectInitiator]

  export const GoxSocketErrorCode: Readonly<{
    HANDSHAKE_FAILED: "HANDSHAKE_FAILED"
    CONNECTION_FAILED: "CONNECTION_FAILED"
    TRANSPORT_ERROR: "TRANSPORT_ERROR"
    HANDLER_FAILED: "HANDLER_FAILED"
    INVALID_STATE: "INVALID_STATE"
    CONNECTION_CLOSED: "CONNECTION_CLOSED"
    PROTOCOL_ERROR: "PROTOCOL_ERROR"
    ENCODE_FAILED: "ENCODE_FAILED"
    BIND_FAILED: "BIND_FAILED"
    EVENT_NOT_FOUND: "EVENT_NOT_FOUND"
    ARGUMENT_COUNT_MISMATCH: "ARGUMENT_COUNT_MISMATCH"
    ARGUMENT_TYPE_MISMATCH: "ARGUMENT_TYPE_MISMATCH"
    SESSION_SAVE_FAILED: "SESSION_SAVE_FAILED"
    SESSION_TOUCH_FAILED: "SESSION_TOUCH_FAILED"
    SESSION_EXPIRED: "SESSION_EXPIRED"
  }>

  export const GoxSocketErrorPhase: Readonly<{
    HANDSHAKE: "handshake"
    CONNECTION: "connection"
    TRANSPORT: "transport"
    HANDLER: "handler"
    PROTOCOL: "protocol"
    BINDING: "binding"
    SESSION: "session"
  }>

  export interface GoxSocketErrorOptions {
    cause?: unknown
    phase?: string
    event?: string
    retryable?: boolean
    nativeEvent?: Event
    eventID?: bigint
    argument?: number
    received?: string
    expected?: string
    missing?: string[]
    remote?: boolean
  }

  export class GoxSocketError extends Error {
    constructor(code: string, message: string, options?: GoxSocketErrorOptions)
    code: string
    phase: string
    event: string
    retryable: boolean
    nativeEvent: Event | undefined
    eventID: bigint
    argument: number
    received: string
    expected: string
    missing: string[]
    remote: boolean
  }

  export interface GoxChannelErrorOptions extends GoxSocketErrorOptions {
    channelID?: string
  }

  export class GoxChannelError extends GoxSocketError {
    constructor(code: string, message: string, options?: GoxChannelErrorOptions)
    channelID: string
  }

  export const ChannelState: Readonly<{
    CONNECTING: "connecting"
    CONNECTED: "connected"
    RECONNECTING: "reconnecting"
    DISCONNECTING: "disconnecting"
    DISCONNECTED: "disconnected"
    REJECTED: "rejected"
  }>
  export type ChannelStateValue = typeof ChannelState[keyof typeof ChannelState]

  export interface WSDisconnectReason {
    code: number
    reason: string
    clean: boolean
    initiator: WSDisconnectInitiatorValue
    willReconnect: boolean
    nativeEvent: CloseEvent | undefined
  }

  export interface ChannelEmission<T = unknown> {
    await(timeout?: number): Promise<T | undefined>
  }

  export class Channel<Name extends string = string> {
    private constructor(token: unknown, transport: unknown, id: string, validation?: GoxValue[])
    readonly id: string
    readonly name: string
    readonly key: string
    readonly connectionState: ChannelStateValue
    when(event: "connected", handler: (isReconnect: boolean) => void): this
    when(event: "rejected", handler: () => void): this
    when(event: "disconnected", handler: (reason: string) => void): this
    when(event: "error", handler: (error: GoxSocketError) => void): this
    when(event: string, handler: (...args: unknown[]) => unknown): this
    on<Event extends keyof GoxChannelOnEvents<Name> & string>(event: Event, handler: GoxIncomingHandler<GoxChannelOnEvents<Name>[Event]>): this
    on(event: string, handler: (...args: unknown[]) => unknown): this
    emit<Event extends keyof GoxChannelEmitEvents<Name> & string>(event: Event, ...values: GoxEventArgs<GoxChannelEmitEvents<Name>[Event]>): ChannelEmission<GoxChannelEmissionReply<GoxChannelEmitEvents<Name>[Event]>>
    emit<T = unknown, Event extends string = string>(event: Event & (Event extends keyof GoxChannelEmitEvents<Name> ? never : unknown), ...values: GoxValue[]): ChannelEmission<T>
    leave(): this
  }

  export interface SocketReconnectOptions {
    enabled?: boolean
    delay?: number
    maxDelay?: number
    attempts?: number
  }

  export interface SocketOptions {
    origin?: string | URL
    reconnect?: boolean | SocketReconnectOptions
  }

  export type SocketState = "connecting" | "connected" | "reconnecting" | "disconnected"
  export interface SocketChannelConstructor {
    new <ID extends string>(id: ID, ...validation: GoxValue[]): Channel<GoxChannelName<ID>>
  }

  export class Socket {
    constructor(options?: SocketOptions)
    readonly state: SocketState
    readonly Channel: SocketChannelConstructor
    when(event: "connect", handler: (reconnected: boolean) => void): this
    when(event: "disconnect", handler: (reason: WSDisconnectReason) => void): this
    when(event: "error", handler: (error: GoxSocketError) => void): this
    when(event: string, handler: (...args: unknown[]) => unknown): this
    on<Event extends keyof GoxSocketClientOnEvents & string>(event: Event, handler: GoxIncomingHandler<GoxSocketClientOnEvents[Event]>): this
    on(event: string, handler: (...args: unknown[]) => GoxValue | Promise<GoxValue> | void): this
    emit<Event extends keyof GoxSocketClientEmitEvents & string>(event: Event, ...values: GoxEventArgs<GoxSocketClientEmitEvents[Event]>): Promise<GoxOutgoingReply<GoxSocketClientEmitEvents[Event]>>
    emit<T = unknown, Event extends string = string>(event: Event & (Event extends keyof GoxSocketClientEmitEvents ? never : unknown), form: HTMLFormElement): Promise<T | undefined>
    emit<T = unknown, Event extends string = string>(event: Event & (Event extends keyof GoxSocketClientEmitEvents ? never : unknown), ...values: GoxValue[]): Promise<T | undefined>
    disconnect(code?: number): this
  }
}
