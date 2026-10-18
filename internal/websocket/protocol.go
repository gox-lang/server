package websocket

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const ProtocolVersion byte = 0x01

type PacketType byte

const (
	PacketEvent           PacketType = 0x01
	PacketRequest         PacketType = 0x02
	PacketResponse        PacketType = 0x03
	PacketError           PacketType = 0x04
	PacketTransferBegin   PacketType = 0x10
	PacketTransferChunk   PacketType = 0x11
	PacketTransferEnd     PacketType = 0x12
	PacketTransferAbort   PacketType = 0x13
	PacketChannelJoin     PacketType = 0x20
	PacketChannelAccepted PacketType = 0x21
	PacketChannelRejected PacketType = 0x22
	PacketChannelLeave    PacketType = 0x23
	PacketChannelEvent    PacketType = 0x24
	PacketChannelRequest  PacketType = 0x25
	PacketChannelResponse PacketType = 0x26
	PacketChannelError    PacketType = 0x27
)

const (
	FlagInternal byte = 1 << 0
	FlagTransfer byte = 1 << 1
	knownFlags        = FlagInternal | FlagTransfer
)

type HeaderType byte

const (
	HeaderEventName     HeaderType = 0x01
	HeaderRequestID     HeaderType = 0x02
	HeaderTransferID    HeaderType = 0x03
	HeaderSequence      HeaderType = 0x04
	HeaderTotalSize     HeaderType = 0x05
	HeaderFileName      HeaderType = 0x06
	HeaderMIMEType      HeaderType = 0x07
	HeaderErrorCode     HeaderType = 0x08
	HeaderEventID       HeaderType = 0x09
	HeaderChannelID     HeaderType = 0x0a
	HeaderChannelReason HeaderType = 0x0b
)

type ArgumentType byte

const (
	ArgumentNull    ArgumentType = 0x00
	ArgumentText    ArgumentType = 0x01
	ArgumentNumber  ArgumentType = 0x02
	ArgumentBoolean ArgumentType = 0x03
	ArgumentDate    ArgumentType = 0x04
	ArgumentJSON    ArgumentType = 0x05
	ArgumentBinary  ArgumentType = 0x06
	ArgumentFile    ArgumentType = 0x07
	ArgumentFiles   ArgumentType = 0x08
	ArgumentBigInt  ArgumentType = 0x09
	ArgumentHTML    ArgumentType = 0x0a
)

type Header struct {
	Type  HeaderType
	Value []byte
}

type Argument struct {
	Type ArgumentType
	Data []byte
}

type Packet struct {
	Type      PacketType
	Flags     byte
	Headers   []Header
	Arguments []Argument
	Payload   []byte
}

var ErrMalformedPacket = errors.New("malformed GOX WebSocket packet")

var protocolNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
var protocolBigIntPattern = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformedPacket, fmt.Sprintf(format, args...))
}

// EncodeCompactUint encodes a uint64 as a width byte followed by the minimum
// number of big-endian bytes. Zero is encoded as one zero byte.
func EncodeCompactUint(value uint64) []byte {
	if value == 0 {
		return []byte{0}
	}
	width := byte(8)
	for width > 1 && value < uint64(1)<<(8*(width-1)) {
		width--
	}
	encoded := make([]byte, int(width)+1)
	encoded[0] = width
	for i := int(width); i > 0; i-- {
		encoded[i] = byte(value)
		value >>= 8
	}
	return encoded
}

// DecodeCompactUint decodes one canonical compact uint and returns how many
// bytes were consumed.
func DecodeCompactUint(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, malformed("missing compact integer width")
	}
	width := int(data[0])
	if width > 8 {
		return 0, 0, malformed("compact integer width %d exceeds 8", width)
	}
	if len(data) < width+1 {
		return 0, 0, malformed("truncated compact integer")
	}
	if width == 0 {
		return 0, 1, nil
	}
	if data[1] == 0 {
		return 0, 0, malformed("compact integer is not minimally encoded")
	}
	var value uint64
	for _, part := range data[1 : width+1] {
		value = value<<8 | uint64(part)
	}
	return value, width + 1, nil
}

// EncodeFileReference creates the payload of one FILE argument.
func EncodeFileReference(transferID uint64) ([]byte, error) {
	if transferID == 0 {
		return nil, malformed("file transfer ID must be greater than zero")
	}
	return EncodeCompactUint(transferID), nil
}

// EncodeFilesReference creates the payload of one FILES argument while
// preserving the original file order.
func EncodeFilesReference(transferIDs []uint64) ([]byte, error) {
	if len(transferIDs) > 255 {
		return nil, malformed("file reference count %d exceeds 255", len(transferIDs))
	}
	encoded := []byte{byte(len(transferIDs))}
	for _, transferID := range transferIDs {
		file, err := EncodeFileReference(transferID)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, file...)
	}
	return encoded, nil
}

func DecodeFileReference(data []byte) (uint64, error) {
	transferID, consumed, err := DecodeCompactUint(data)
	if err != nil || consumed != len(data) || transferID == 0 {
		return 0, malformed("file argument must contain one non-zero canonical transfer ID")
	}
	return transferID, nil
}

func DecodeFilesReference(data []byte) ([]uint64, error) {
	if len(data) == 0 {
		return nil, malformed("files argument is missing its file count")
	}
	count := int(data[0])
	offset := 1
	transferIDs := make([]uint64, 0, count)
	for index := 0; index < count; index++ {
		transferID, consumed, err := DecodeCompactUint(data[offset:])
		if err != nil || transferID == 0 {
			return nil, malformed("files argument transfer ID %d is invalid", index)
		}
		offset += consumed
		transferIDs = append(transferIDs, transferID)
	}
	if offset != len(data) {
		return nil, malformed("files argument contains trailing bytes")
	}
	return transferIDs, nil
}

// EncodeBigInt creates the canonical decimal payload of one BIGINT argument.
func EncodeBigInt(value *big.Int) ([]byte, error) {
	if value == nil {
		return nil, malformed("big integer cannot be nil")
	}
	return []byte(value.String()), nil
}

// DecodeBigInt decodes one canonical BIGINT argument without limiting its
// precision to uint64.
func DecodeBigInt(data []byte) (*big.Int, error) {
	if !protocolBigIntPattern.Match(data) {
		return nil, malformed("big integer is not canonically encoded")
	}
	value, ok := new(big.Int).SetString(string(data), 10)
	if !ok {
		return nil, malformed("big integer is invalid")
	}
	return value, nil
}

func EncodePacket(packet Packet) ([]byte, error) {
	if err := validatePacket(packet); err != nil {
		return nil, err
	}
	payload := packet.Payload
	if packetCarriesArguments(packet.Type) {
		var err error
		payload, err = EncodeArguments(packet.Arguments)
		if err != nil {
			return nil, err
		}
	}
	size := 4 + len(payload)
	for _, header := range packet.Headers {
		size += 2 + len(header.Value)
	}
	encoded := make([]byte, 0, size)
	encoded = append(encoded, byte(packet.Type), ProtocolVersion, packet.Flags, byte(len(packet.Headers)))
	for _, header := range packet.Headers {
		encoded = append(encoded, byte(header.Type), byte(len(header.Value)))
		encoded = append(encoded, header.Value...)
	}
	encoded = append(encoded, payload...)
	return encoded, nil
}

func DecodePacket(data []byte) (Packet, error) {
	if len(data) < 4 {
		return Packet{}, malformed("packet header is truncated")
	}
	packet := Packet{Type: PacketType(data[0]), Flags: data[2]}
	if data[1] != ProtocolVersion {
		return Packet{}, malformed("unsupported protocol version %d", data[1])
	}
	headerCount := int(data[3])
	offset := 4
	packet.Headers = make([]Header, 0, headerCount)
	for i := 0; i < headerCount; i++ {
		if len(data)-offset < 2 {
			return Packet{}, malformed("header %d is truncated", i)
		}
		headerType := HeaderType(data[offset])
		length := int(data[offset+1])
		offset += 2
		if len(data)-offset < length {
			return Packet{}, malformed("header %d value is truncated", i)
		}
		value := append([]byte(nil), data[offset:offset+length]...)
		offset += length
		packet.Headers = append(packet.Headers, Header{Type: headerType, Value: value})
	}
	if packetCarriesArguments(packet.Type) {
		arguments, err := DecodeArguments(data[offset:])
		if err != nil {
			return Packet{}, err
		}
		packet.Arguments = arguments
	} else {
		packet.Payload = append([]byte(nil), data[offset:]...)
	}
	if err := validatePacket(packet); err != nil {
		return Packet{}, err
	}
	return packet, nil
}

func EncodeArguments(arguments []Argument) ([]byte, error) {
	if len(arguments) > 255 {
		return nil, malformed("argument count %d exceeds 255", len(arguments))
	}
	encoded := []byte{byte(len(arguments))}
	for i, argument := range arguments {
		if err := validateArgument(i, argument); err != nil {
			return nil, err
		}
		encoded = append(encoded, byte(argument.Type))
		encoded = append(encoded, EncodeCompactUint(uint64(len(argument.Data)))...)
		encoded = append(encoded, argument.Data...)
	}
	return encoded, nil
}

func DecodeArguments(data []byte) ([]Argument, error) {
	if len(data) == 0 {
		return nil, malformed("missing argument count")
	}
	count := int(data[0])
	offset := 1
	arguments := make([]Argument, 0, count)
	for i := 0; i < count; i++ {
		if offset >= len(data) {
			return nil, malformed("argument %d type is missing", i)
		}
		argumentType := ArgumentType(data[offset])
		offset++
		length, consumed, err := DecodeCompactUint(data[offset:])
		if err != nil {
			return nil, malformed("argument %d: %v", i, err)
		}
		offset += consumed
		if length > uint64(len(data)-offset) {
			return nil, malformed("argument %d data is truncated", i)
		}
		end := offset + int(length)
		argument := Argument{Type: argumentType, Data: append([]byte(nil), data[offset:end]...)}
		if err := validateArgument(i, argument); err != nil {
			return nil, err
		}
		arguments = append(arguments, argument)
		offset = end
	}
	if offset != len(data) {
		return nil, malformed("argument payload contains trailing bytes")
	}
	return arguments, nil
}

func validatePacket(packet Packet) error {
	if !knownPacketType(packet.Type) {
		return malformed("unknown packet type 0x%02x", byte(packet.Type))
	}
	if packet.Flags&^knownFlags != 0 {
		return malformed("unknown flags 0x%02x", packet.Flags&^knownFlags)
	}
	if isChannelPacket(packet.Type) && packet.Flags != 0 {
		return malformed("Channel packet 0x%02x cannot contain flags", byte(packet.Type))
	}
	if len(packet.Headers) > 255 {
		return malformed("header count %d exceeds 255", len(packet.Headers))
	}
	if packetCarriesArguments(packet.Type) {
		if len(packet.Payload) != 0 {
			return malformed("packet 0x%02x cannot contain a raw payload", byte(packet.Type))
		}
	} else if len(packet.Arguments) != 0 {
		return malformed("packet 0x%02x cannot contain arguments", byte(packet.Type))
	}
	seen := make(map[HeaderType]struct{}, len(packet.Headers))
	var previous HeaderType
	for i, header := range packet.Headers {
		if !knownHeaderType(header.Type) {
			return malformed("unknown header type 0x%02x", byte(header.Type))
		}
		if _, exists := seen[header.Type]; exists {
			return malformed("duplicate header type 0x%02x", byte(header.Type))
		}
		if i > 0 && header.Type <= previous {
			return malformed("headers are not in canonical order")
		}
		if len(header.Value) == 0 || len(header.Value) > 255 {
			return malformed("header 0x%02x length must be between 1 and 255", byte(header.Type))
		}
		if !headerAllowed(packet.Type, header.Type) {
			return malformed("header 0x%02x is forbidden for packet 0x%02x", byte(header.Type), byte(packet.Type))
		}
		if err := validateHeaderValue(header); err != nil {
			return err
		}
		seen[header.Type] = struct{}{}
		previous = header.Type
	}
	for _, required := range requiredHeaders(packet.Type) {
		if _, exists := seen[required]; !exists {
			return malformed("required header 0x%02x is missing", byte(required))
		}
	}
	if packet.Type == PacketError {
		_, hasRequest := seen[HeaderRequestID]
		_, hasEvent := seen[HeaderEventID]
		_, hasEventName := seen[HeaderEventName]
		if hasRequest == hasEvent {
			return malformed("ERROR packet must contain exactly one correlation ID")
		}
		if hasEvent != hasEventName {
			return malformed("event ERROR packet must contain its event name")
		}
	}
	if packet.Type == PacketChannelError {
		_, hasRequest := seen[HeaderRequestID]
		_, hasEvent := seen[HeaderEventID]
		_, hasEventName := seen[HeaderEventName]
		if hasRequest == hasEvent {
			return malformed("Channel ERROR packet must contain exactly one correlation ID")
		}
		if hasEvent != hasEventName {
			return malformed("Channel event ERROR packet must contain its event name")
		}
	}
	if (packet.Type == PacketChannelAccepted || packet.Type == PacketChannelRejected || packet.Type == PacketChannelLeave) && len(packet.Payload) != 0 {
		return malformed("Channel control packet 0x%02x must have an empty payload", byte(packet.Type))
	}
	if (packet.Type == PacketTransferBegin || packet.Type == PacketTransferEnd || packet.Type == PacketTransferAbort) && len(packet.Payload) != 0 {
		return malformed("transfer control packet 0x%02x must have an empty payload", byte(packet.Type))
	}
	if packet.Type == PacketTransferChunk && len(packet.Payload) == 0 {
		return malformed("transfer chunk payload cannot be empty")
	}
	return nil
}

func isChannelPacket(value PacketType) bool {
	return value >= PacketChannelJoin && value <= PacketChannelError
}

func validateHeaderValue(header Header) error {
	switch header.Type {
	case HeaderEventName, HeaderFileName, HeaderMIMEType, HeaderErrorCode, HeaderChannelID, HeaderChannelReason:
		if !utf8.Valid(header.Value) {
			return malformed("header 0x%02x is not valid UTF-8", byte(header.Type))
		}
		if header.Type == HeaderChannelID {
			value := string(header.Value)
			if strings.TrimSpace(value) != value {
				return malformed("Channel ID cannot start or end with whitespace")
			}
			for _, part := range value {
				if unicode.IsControl(part) {
					return malformed("Channel ID cannot contain control characters")
				}
			}
		}
	case HeaderRequestID, HeaderTransferID, HeaderSequence, HeaderTotalSize, HeaderEventID:
		value, consumed, err := DecodeCompactUint(header.Value)
		if err != nil || consumed != len(header.Value) {
			return malformed("header 0x%02x does not contain one canonical integer", byte(header.Type))
		}
		if (header.Type == HeaderRequestID || header.Type == HeaderTransferID || header.Type == HeaderEventID) && value == 0 {
			return malformed("header 0x%02x ID must be greater than zero", byte(header.Type))
		}
	}
	return nil
}

func validateArgument(index int, argument Argument) error {
	if !knownArgumentType(argument.Type) {
		return malformed("argument %d has unknown type 0x%02x", index, byte(argument.Type))
	}
	switch argument.Type {
	case ArgumentNull:
		if len(argument.Data) != 0 {
			return malformed("argument %d null value must be empty", index)
		}
	case ArgumentBoolean:
		if len(argument.Data) != 1 || argument.Data[0] > 1 {
			return malformed("argument %d boolean must be exactly 0 or 1", index)
		}
	case ArgumentText, ArgumentNumber, ArgumentDate, ArgumentJSON, ArgumentBigInt, ArgumentHTML:
		if !utf8.Valid(argument.Data) {
			return malformed("argument %d is not valid UTF-8", index)
		}
	}
	switch argument.Type {
	case ArgumentNumber:
		if !protocolNumberPattern.Match(argument.Data) {
			return malformed("argument %d is not a canonical decimal number", index)
		}
	case ArgumentDate:
		if _, err := time.Parse(time.RFC3339Nano, string(argument.Data)); err != nil {
			return malformed("argument %d is not an RFC3339 date", index)
		}
	case ArgumentJSON:
		if !json.Valid(argument.Data) {
			return malformed("argument %d is not valid JSON", index)
		}
	case ArgumentFile:
		if _, err := DecodeFileReference(argument.Data); err != nil {
			return malformed("argument %d has an invalid file reference", index)
		}
	case ArgumentFiles:
		if _, err := DecodeFilesReference(argument.Data); err != nil {
			return malformed("argument %d has invalid file references", index)
		}
	case ArgumentBigInt:
		if _, err := DecodeBigInt(argument.Data); err != nil {
			return malformed("argument %d is not a canonical big integer", index)
		}
	}
	return nil
}

func knownPacketType(value PacketType) bool {
	switch value {
	case PacketEvent, PacketRequest, PacketResponse, PacketError, PacketTransferBegin, PacketTransferChunk, PacketTransferEnd, PacketTransferAbort,
		PacketChannelJoin, PacketChannelAccepted, PacketChannelRejected, PacketChannelLeave, PacketChannelEvent, PacketChannelRequest, PacketChannelResponse, PacketChannelError:
		return true
	default:
		return false
	}
}

func packetCarriesArguments(value PacketType) bool {
	switch value {
	case PacketEvent, PacketRequest, PacketResponse, PacketError, PacketChannelJoin, PacketChannelEvent, PacketChannelRequest, PacketChannelResponse, PacketChannelError:
		return true
	default:
		return false
	}
}

func knownHeaderType(value HeaderType) bool {
	return value >= HeaderEventName && value <= HeaderChannelReason
}
func knownArgumentType(value ArgumentType) bool { return value <= ArgumentHTML }

func requiredHeaders(packet PacketType) []HeaderType {
	switch packet {
	case PacketEvent:
		return []HeaderType{HeaderEventName, HeaderEventID}
	case PacketRequest:
		return []HeaderType{HeaderEventName, HeaderRequestID}
	case PacketResponse:
		return []HeaderType{HeaderRequestID}
	case PacketError:
		return []HeaderType{HeaderErrorCode}
	case PacketTransferBegin:
		return []HeaderType{HeaderTransferID, HeaderTotalSize, HeaderFileName, HeaderMIMEType}
	case PacketTransferChunk:
		return []HeaderType{HeaderTransferID, HeaderSequence}
	case PacketTransferEnd:
		return []HeaderType{HeaderTransferID, HeaderTotalSize}
	case PacketTransferAbort:
		return []HeaderType{HeaderTransferID, HeaderErrorCode}
	case PacketChannelJoin, PacketChannelAccepted, PacketChannelRejected, PacketChannelLeave:
		return []HeaderType{HeaderChannelID}
	case PacketChannelEvent:
		return []HeaderType{HeaderEventName, HeaderEventID, HeaderChannelID}
	case PacketChannelRequest:
		return []HeaderType{HeaderEventName, HeaderRequestID, HeaderChannelID}
	case PacketChannelResponse:
		return []HeaderType{HeaderRequestID, HeaderChannelID}
	case PacketChannelError:
		return []HeaderType{HeaderErrorCode, HeaderChannelID}
	default:
		return nil
	}
}

func headerAllowed(packet PacketType, header HeaderType) bool {
	switch packet {
	case PacketEvent:
		return header == HeaderEventName || header == HeaderTransferID || header == HeaderEventID
	case PacketRequest:
		return header == HeaderEventName || header == HeaderRequestID || header == HeaderTransferID
	case PacketResponse:
		return header == HeaderRequestID || header == HeaderTransferID
	case PacketError:
		return header == HeaderEventName || header == HeaderRequestID || header == HeaderErrorCode || header == HeaderEventID
	case PacketTransferBegin:
		return header == HeaderTransferID || header == HeaderTotalSize || header == HeaderFileName || header == HeaderMIMEType
	case PacketTransferChunk:
		return header == HeaderTransferID || header == HeaderSequence
	case PacketTransferEnd:
		return header == HeaderTransferID || header == HeaderTotalSize
	case PacketTransferAbort:
		return header == HeaderTransferID || header == HeaderErrorCode
	case PacketChannelJoin, PacketChannelAccepted, PacketChannelRejected:
		return header == HeaderChannelID
	case PacketChannelLeave:
		return header == HeaderChannelID || header == HeaderChannelReason
	case PacketChannelEvent:
		return header == HeaderEventName || header == HeaderTransferID || header == HeaderEventID || header == HeaderChannelID
	case PacketChannelRequest:
		return header == HeaderEventName || header == HeaderRequestID || header == HeaderTransferID || header == HeaderChannelID
	case PacketChannelResponse:
		return header == HeaderRequestID || header == HeaderTransferID || header == HeaderChannelID
	case PacketChannelError:
		return header == HeaderEventName || header == HeaderRequestID || header == HeaderErrorCode || header == HeaderEventID || header == HeaderChannelID
	default:
		return false
	}
}
