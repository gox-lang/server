package serverruntime

import (
	"fmt"
	"reflect"

	"github.com/gox-lang/server/internal/logs"
	websocketmeta "github.com/gox-lang/server/internal/websocket"

	"github.com/gox-lang/gox"
)

var channelSocketType = reflect.TypeOf((*gox.Socket)(nil))

func (connection *webSocketRuntimeConnection) emitChannel(channelID, event string, values []any) error {
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers(values, func() uint64 {
		id := connection.nextTransferID.Add(1)
		if id == 0 {
			id = connection.nextTransferID.Add(1)
		}
		return id
	}, connection.runtime.webSocketClientAssets())
	if err != nil {
		return err
	}
	eventID := connection.nextEventID.Add(1)
	if eventID == 0 {
		eventID = connection.nextEventID.Add(1)
	}
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketChannelEvent,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte(event)},
			{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(eventID)},
			{Type: websocketmeta.HeaderChannelID, Value: []byte(channelID)},
		},
		Arguments: arguments,
	})
	if err != nil {
		return err
	}
	for _, transfer := range transfers {
		if err := connection.writeTransfer(event, transfer); err != nil {
			return err
		}
	}
	return connection.writeBinary(event, packet)
}

func (connection *webSocketRuntimeConnection) sendChannelLeave(channelID string, reason gox.ChannelDisconnectReason) error {
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{Type: websocketmeta.PacketChannelLeave, Headers: []websocketmeta.Header{
		{Type: websocketmeta.HeaderChannelID, Value: []byte(channelID)},
		{Type: websocketmeta.HeaderChannelReason, Value: []byte(reason)},
	}})
	if err != nil {
		return err
	}
	return connection.writeBinary("channel:leave", packet)
}

func (connection *webSocketRuntimeConnection) sendChannelControl(packetType websocketmeta.PacketType, channelID string) error {
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{Type: packetType, Headers: []websocketmeta.Header{{Type: websocketmeta.HeaderChannelID, Value: []byte(channelID)}}})
	if err != nil {
		return err
	}
	return connection.writeBinary("channel", packet)
}

func (connection *webSocketRuntimeConnection) dispatchChannelJoin(packet websocketmeta.Packet) {
	channelID := websocketmeta.HeaderText(packet, websocketmeta.HeaderChannelID)
	channel, err := connection.runtime.ensureChannelInstance(channelID)
	if err != nil {
		_ = connection.releasePacketTransfers(packet)
		connection.reportChannelJoinFailure(nil, channelID, gox.ChannelHandlerFailed, err)
		_ = connection.sendChannelControl(websocketmeta.PacketChannelRejected, channelID)
		return
	}
	values := []any{}
	types, handled, typeErr := channel.WhenParameterTypes("validate")
	if typeErr != nil {
		err = typeErr
	} else if handled {
		if len(types) == 0 || types[0] != channelSocketType {
			err = fmt.Errorf(`channel.When("validate") must receive *gox.Socket as its first argument`)
		} else {
			values, err = connection.bindPacketArguments(packet, types[1:])
		}
	} else {
		err = connection.releasePacketTransfers(packet)
	}
	if err != nil {
		connection.reportChannelJoinFailure(channel, channelID, gox.ChannelProtocolError, err)
		connection.runtime.pruneChannelIfUnused(channel)
		_ = connection.sendChannelControl(websocketmeta.PacketChannelRejected, channelID)
		return
	}
	accepted, joinErr := connection.runtime.joinChannel(connection.socket, channelID, values)
	if joinErr != nil {
		connection.reportChannelJoinFailure(channel, channelID, gox.ChannelHandlerFailed, joinErr)
		_ = connection.sendChannelControl(websocketmeta.PacketChannelRejected, channelID)
		return
	}
	if !accepted {
		_ = connection.sendChannelControl(websocketmeta.PacketChannelRejected, channelID)
		return
	}
	if err := connection.sendChannelControl(websocketmeta.PacketChannelAccepted, channelID); err != nil {
		connection.runtime.dispatchChannelLifecycleError(channel, "connected", err)
	}
}

// reportChannelJoinFailure preserves the semantic difference between a normal
// validation rejection and an internal failure that prevented validation or
// membership from completing. The client receives Channel error first and the
// terminal rejected lifecycle notification second.
func (connection *webSocketRuntimeConnection) reportChannelJoinFailure(channel *gox.Channel, channelID string, code gox.ChannelErrorCode, cause error) {
	if connection == nil || cause == nil {
		return
	}
	message := cause.Error()
	logs.Error("Channel join failed: channel=%s error=%v", channelID, cause)
	if channel != nil {
		connection.runtime.dispatchChannelLifecycleError(channel, "validate", gox.ChannelError{
			Code: code, ChannelID: channelID, Event: "validate", Message: message, Cause: cause,
		})
	}
	eventID := connection.nextEventID.Add(1)
	if eventID == 0 {
		eventID = connection.nextEventID.Add(1)
	}
	connection.writeChannelError("validate", []websocketmeta.Header{
		{Type: websocketmeta.HeaderEventName, Value: []byte("validate")},
		{Type: websocketmeta.HeaderErrorCode, Value: []byte(code)},
		{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(eventID)},
		{Type: websocketmeta.HeaderChannelID, Value: []byte(channelID)},
	}, message)
}

func (connection *webSocketRuntimeConnection) dispatchChannelLeave(packet websocketmeta.Packet) error {
	if websocketmeta.HeaderText(packet, websocketmeta.HeaderChannelReason) != "" {
		return fmt.Errorf("client Channel leave cannot provide a reason")
	}
	channelID := websocketmeta.HeaderText(packet, websocketmeta.HeaderChannelID)
	left, err := connection.runtime.leaveChannel(connection.socket, channelID, gox.ChannelDisconnectLeft)
	if err != nil {
		return err
	}
	if !left {
		// Leaving an absent membership is idempotent, but still acknowledge it.
		return connection.sendChannelLeave(channelID, gox.ChannelDisconnectLeft)
	}
	return nil
}

func (connection *webSocketRuntimeConnection) dispatchChannelEvent(packet websocketmeta.Packet) {
	channelID := websocketmeta.HeaderText(packet, websocketmeta.HeaderChannelID)
	event := websocketmeta.EventName(packet)
	eventID, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderEventID)
	channel := connection.activeSocketChannel(channelID)
	if channel == nil {
		_ = connection.releasePacketTransfers(packet)
		connection.sendChannelEventError(channelID, event, eventID, gox.ChannelInvalidState, "Socket is not connected to this Channel")
		return
	}
	types, handled, err := channel.OnParameterTypes(event)
	if err != nil || !handled {
		_ = connection.releasePacketTransfers(packet)
		connection.sendChannelEventError(channelID, event, eventID, gox.ChannelHandlerFailed, "Channel event handler is unavailable")
		return
	}
	if len(types) == 0 || types[0] != channelSocketType {
		_ = connection.releasePacketTransfers(packet)
		connection.sendChannelEventError(channelID, event, eventID, gox.ChannelProtocolError, "Channel handler must receive *gox.Socket first")
		return
	}
	values, err := connection.bindPacketArguments(packet, types[1:])
	if err != nil {
		connection.sendChannelEventError(channelID, event, eventID, gox.ChannelProtocolError, err.Error())
		return
	}
	args := append([]any{connection.socket}, values...)
	go func() {
		if _, _, err := channel.DispatchOn(event, args...); err != nil {
			connection.sendChannelEventError(channelID, event, eventID, gox.ChannelHandlerFailed, err.Error())
			return
		}
		if sessionErr := connection.persistSessionActivity(event); sessionErr != nil {
			connection.sendChannelEventError(channelID, event, eventID, gox.ChannelHandlerFailed, sessionErr.Message)
		}
	}()
}

func (connection *webSocketRuntimeConnection) dispatchChannelRequest(packet websocketmeta.Packet) {
	channelID := websocketmeta.HeaderText(packet, websocketmeta.HeaderChannelID)
	event := websocketmeta.EventName(packet)
	requestID, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderRequestID)
	channel := connection.activeSocketChannel(channelID)
	if channel == nil {
		_ = connection.releasePacketTransfers(packet)
		connection.sendChannelRequestError(channelID, requestID, gox.ChannelInvalidState, "Socket is not connected to this Channel")
		return
	}
	signature, handled := channel.OnSignature(event)
	if !handled || !signature.HasReply {
		_ = connection.releasePacketTransfers(packet)
		connection.sendChannelRequestError(channelID, requestID, gox.ChannelInvalidState, "Channel event does not declare gox.EventReply")
		return
	}
	types, handled, err := channel.OnParameterTypes(event)
	if err != nil || !handled || len(types) == 0 || types[0] != channelSocketType {
		_ = connection.releasePacketTransfers(packet)
		connection.sendChannelRequestError(channelID, requestID, gox.ChannelProtocolError, "Channel handler signature is invalid")
		return
	}
	values, err := connection.bindPacketArguments(packet, types[1:])
	if err != nil {
		connection.sendChannelRequestError(channelID, requestID, gox.ChannelProtocolError, err.Error())
		return
	}
	args := append([]any{connection.socket}, values...)
	go func() {
		value, _, dispatchErr := channel.DispatchOn(event, args...)
		if dispatchErr != nil {
			connection.sendChannelRequestError(channelID, requestID, gox.ChannelHandlerFailed, dispatchErr.Error())
			return
		}
		if replyErr, ok := value.(error); ok && replyErr != nil {
			connection.sendChannelRequestError(channelID, requestID, gox.ChannelHandlerFailed, replyErr.Error())
			return
		}
		if sessionErr := connection.persistSessionActivity(event); sessionErr != nil {
			connection.sendChannelRequestError(channelID, requestID, gox.ChannelHandlerFailed, sessionErr.Message)
			return
		}
		if err := connection.sendChannelResponse(channelID, requestID, event, value); err != nil {
			connection.runtime.dispatchChannelLifecycleError(channel, event, err)
		}
	}()
}

func (connection *webSocketRuntimeConnection) activeSocketChannel(channelID string) *gox.Channel {
	connection.runtime.channelMu.RLock()
	channel := connection.runtime.channelInstances[channelID]
	member := connection.runtime.channelMembers[channelID][connection.socket.ID()]
	connection.runtime.channelMu.RUnlock()
	if channel == nil || member != connection.socket || channel.IsClosed() {
		return nil
	}
	return channel
}

func (connection *webSocketRuntimeConnection) sendChannelResponse(channelID string, requestID uint64, event string, value any) error {
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers([]any{value}, func() uint64 {
		id := connection.nextTransferID.Add(1)
		if id == 0 {
			id = connection.nextTransferID.Add(1)
		}
		return id
	}, connection.runtime.webSocketClientAssets())
	if err != nil {
		return err
	}
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{Type: websocketmeta.PacketChannelResponse, Headers: []websocketmeta.Header{
		{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)},
		{Type: websocketmeta.HeaderChannelID, Value: []byte(channelID)},
	}, Arguments: arguments})
	if err != nil {
		return err
	}
	for _, transfer := range transfers {
		if err := connection.writeTransfer(event, transfer); err != nil {
			return err
		}
	}
	return connection.writeBinary(event, packet)
}

func (connection *webSocketRuntimeConnection) sendChannelEventError(channelID, event string, eventID uint64, code gox.ChannelErrorCode, message string) {
	connection.dispatchAndWriteChannelError(channelID, event, code, message, []websocketmeta.Header{
		{Type: websocketmeta.HeaderEventName, Value: []byte(event)},
		{Type: websocketmeta.HeaderErrorCode, Value: []byte(code)},
		{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(eventID)},
		{Type: websocketmeta.HeaderChannelID, Value: []byte(channelID)},
	})
}

func (connection *webSocketRuntimeConnection) sendChannelRequestError(channelID string, requestID uint64, code gox.ChannelErrorCode, message string) {
	connection.dispatchAndWriteChannelError(channelID, "", code, message, []websocketmeta.Header{
		{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)},
		{Type: websocketmeta.HeaderErrorCode, Value: []byte(code)},
		{Type: websocketmeta.HeaderChannelID, Value: []byte(channelID)},
	})
}

func (connection *webSocketRuntimeConnection) dispatchAndWriteChannelError(channelID, event string, code gox.ChannelErrorCode, message string, headers []websocketmeta.Header) {
	connection.runtime.channelMu.RLock()
	channel := connection.runtime.channelInstances[channelID]
	connection.runtime.channelMu.RUnlock()
	if channel != nil {
		connection.runtime.dispatchChannelLifecycleError(channel, event, gox.ChannelError{Code: code, ChannelID: channelID, Event: event, Message: message})
	}
	connection.writeChannelError(event, headers, message)
}

func (connection *webSocketRuntimeConnection) writeChannelError(event string, headers []websocketmeta.Header, message string) {
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{Type: websocketmeta.PacketChannelError, Headers: headers, Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte(message)}}})
	if err == nil {
		_ = connection.writeBinary(event, packet)
	}
}

func (connection *webSocketRuntimeConnection) dispatchRemoteChannelError(packet websocketmeta.Packet) error {
	if _, hasRequest := websocketmeta.HeaderUint(packet, websocketmeta.HeaderRequestID); hasRequest {
		return fmt.Errorf("client Channel ERROR cannot reference a request")
	}
	if len(packet.Arguments) != 1 || packet.Arguments[0].Type != websocketmeta.ArgumentText {
		return fmt.Errorf("Channel ERROR requires one TEXT argument")
	}
	channelID := websocketmeta.HeaderText(packet, websocketmeta.HeaderChannelID)
	channel := connection.activeSocketChannel(channelID)
	if channel == nil {
		return nil
	}
	event := websocketmeta.EventName(packet)
	code := gox.ChannelErrorCode(websocketmeta.HeaderText(packet, websocketmeta.HeaderErrorCode))
	connection.runtime.dispatchChannelLifecycleError(channel, event, gox.ChannelError{Code: code, ChannelID: channelID, Event: event, Message: string(packet.Arguments[0].Data)})
	return nil
}

func (connection *webSocketRuntimeConnection) releasePacketTransfers(packet websocketmeta.Packet) error {
	all := make(map[uint64]struct{})
	for _, argument := range packet.Arguments {
		ids := []uint64{}
		if argument.Type == websocketmeta.ArgumentFile {
			id, err := websocketmeta.DecodeFileReference(argument.Data)
			if err != nil {
				return err
			}
			ids = []uint64{id}
		} else if argument.Type == websocketmeta.ArgumentFiles {
			decoded, err := websocketmeta.DecodeFilesReference(argument.Data)
			if err != nil {
				return err
			}
			ids = decoded
		}
		for _, id := range ids {
			all[id] = struct{}{}
		}
	}
	for id := range all {
		transfer, exists := connection.incoming[id]
		if !exists || !transfer.complete || uint64(len(transfer.data)) != transfer.total {
			return fmt.Errorf("file transfer %d is incomplete", id)
		}
	}
	for id := range all {
		delete(connection.incoming, id)
	}
	return nil
}
