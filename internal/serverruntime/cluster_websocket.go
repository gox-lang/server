package serverruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/gox-lang/gox"
	websocketmeta "github.com/gox-lang/server/internal/websocket"
	"github.com/gox-lang/gox/server"
)

const distributedSocketRequestTimeout = 10 * time.Second
const distributedClusterOperationTimeout = 2 * time.Second

func clusterOperationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), distributedClusterOperationTimeout)
}

func (r *Runtime) newClusterSocketProxy(socketID string) *gox.Socket {
	if r == nil || socketID == "" {
		return nil
	}
	return r.newClusterSocketProxyRecord(server.ClusterSocketRecord{ID: socketID})
}

func (r *Runtime) newClusterSocketProxyRecord(record server.ClusterSocketRecord) *gox.Socket {
	if r == nil || record.ID == "" {
		return nil
	}
	socketID := record.ID
	proxy := &gox.Socket{Session: server.NewSessionReference(record.SessionID)}
	gox.BindSocketIdentity(proxy)(socketID)
	gox.BindSocketLocation(proxy, record.InstanceID, record.InstanceID != "" && record.InstanceID == r.instanceID)
	gox.BindSocketDisconnect(proxy, func(code gox.WSCloseCode, _ string) {
		r.disconnectClusterSockets(server.ClusterTarget{Kind: server.ClusterTargetSocket, SocketID: socketID}, record.InstanceID, code)
	})
	gox.BindSocketEmit(proxy, func(event string, values []any) error {
		r.emitClusterSocketEvent(server.ClusterTarget{Kind: server.ClusterTargetSocket, SocketID: socketID}, event, values)
		return nil
	})
	gox.BindSocketRequest(proxy, func(event string, values []any, out any) error {
		return r.requestClusterSocket(proxy, event, values, out)
	})
	r.bindSocketGroups(proxy)
	return proxy
}

func (r *Runtime) requestClusterSocket(socket *gox.Socket, event string, values []any, out any) error {
	return r.requestClusterSocketWithTimeout(socket, event, values, out, distributedSocketRequestTimeout)
}

func (r *Runtime) requestClusterSocketWithTimeout(socket *gox.Socket, event string, values []any, out any, timeout time.Duration) error {
	if r == nil || socket == nil || socket.ID() == "" {
		return nil
	}
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers(values, func() uint64 {
		id := r.clusterTransferID.Add(1)
		if id == 0 {
			id = r.clusterTransferID.Add(1)
		}
		return id
	}, r.webSocketClientAssets())
	if err != nil || len(transfers) != 0 {
		if err == nil {
			err = fmt.Errorf("distributed socket request transfers are not enabled yet")
		}
		r.dispatchClusterSocketFailure(socket, "encode-request", "Distributed WebSocket request could not be encoded", event, err)
		return nil
	}
	requestID := r.clusterEventID.Add(1)
	if requestID == 0 {
		requestID = r.clusterEventID.Add(1)
	}
	payload, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketRequest,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte(event)},
			{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)},
		},
		Arguments: arguments,
	})
	if err != nil {
		r.dispatchClusterSocketFailure(socket, "encode-request", "Distributed WebSocket request could not be encoded", event, err)
		return nil
	}
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	if cluster == nil {
		r.sessionStorageMu.RUnlock()
		r.dispatchClusterSocketFailure(socket, "publish-request", "Cluster transport is not available", event, errClusterRequestClosed)
		return nil
	}
	coordinator := r.clusterRequestCoordinator()
	correlationID, pending, err := coordinator.register()
	if err != nil {
		r.sessionStorageMu.RUnlock()
		r.dispatchClusterSocketFailure(socket, "register-request", "Distributed WebSocket request could not be registered", event, err)
		return nil
	}
	publishCtx, publishCancel := clusterOperationContext()
	err = cluster.PublishSocketRequest(publishCtx, socket.ID(), correlationID, payload)
	publishCancel()
	if err != nil {
		r.sessionStorageMu.RUnlock()
		coordinator.cancel(pending, err)
		r.dispatchClusterSocketFailure(socket, "publish-request", "Distributed WebSocket request could not be published", event, err)
		return nil
	}
	r.sessionStorageMu.RUnlock()
	result := coordinator.wait(pending, timeout)
	if result.err != nil {
		if result.err == errClusterRequestTimeout {
			r.publishClusterSocketRequestCancel(correlationID)
		}
		r.dispatchClusterSocketFailure(socket, "await-response", "Distributed WebSocket request did not complete", event, result.err)
		return nil
	}
	return r.bindClusterSocketResponse(socket, event, out, result.payload)
}

func (r *Runtime) publishClusterSocketRequestCancel(correlationID []byte) {
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	if cluster == nil {
		r.sessionStorageMu.RUnlock()
		return
	}
	cancelCtx, cancelDone := clusterOperationContext()
	err := cluster.PublishSocketRequestCancel(cancelCtx, correlationID)
	cancelDone()
	r.sessionStorageMu.RUnlock()
	if err != nil {
		r.dispatchClusterError(cluster, "publish-cancel", "Distributed WebSocket cancellation could not be published", err)
	}
}

func (r *Runtime) bindClusterSocketResponse(socket *gox.Socket, event string, out any, payload []byte) error {
	packet, err := websocketmeta.DecodePacket(payload)
	if err != nil {
		r.dispatchClusterSocketFailure(socket, "decode-response", "Distributed WebSocket response is malformed", event, err)
		return nil
	}
	if packet.Type == websocketmeta.PacketError {
		var details webSocketEventErrorDetails
		if len(packet.Arguments) != 1 || packet.Arguments[0].Type != websocketmeta.ArgumentJSON || json.Unmarshal(packet.Arguments[0].Data, &details) != nil {
			r.dispatchClusterSocketFailure(socket, "decode-response", "Distributed WebSocket error response is malformed", event, fmt.Errorf("invalid ERROR payload"))
			return nil
		}
		socketErr := gox.NewGoxSocketError(gox.GoxSocketErrorCode(websocketmeta.HeaderText(packet, websocketmeta.HeaderErrorCode)), details.Message, gox.GoxSocketPhaseHandler, event, false, nil)
		socketErr.Argument = details.Argument
		socketErr.Received = details.Received
		socketErr.Expected = details.Expected
		socketErr.Remote = true
		dispatchWebSocketError(socket, socketErr)
		return nil
	}
	if packet.Type != websocketmeta.PacketResponse || len(packet.Arguments) != 1 {
		r.dispatchClusterSocketFailure(socket, "decode-response", "Distributed WebSocket response is malformed", event, fmt.Errorf("expected one RESPONSE value"))
		return nil
	}
	destination := reflect.ValueOf(out)
	values, err := websocketmeta.BindArgumentsWithTransfers(packet.Arguments, []reflect.Type{destination.Elem().Type()}, map[uint64]gox.File{})
	if err != nil {
		return err
	}
	if values[0] == nil {
		destination.Elem().Set(reflect.Zero(destination.Elem().Type()))
	} else {
		destination.Elem().Set(reflect.ValueOf(values[0]))
	}
	return nil
}

func (r *Runtime) dispatchClusterSocketFailure(socket *gox.Socket, operation, message, event string, cause error) {
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	r.sessionStorageMu.RUnlock()
	r.dispatchClusterError(cluster, operation, message, cause)
	if socket != nil {
		dispatchWebSocketError(socket, gox.NewGoxSocketError(gox.GoxSocketTransportError, message, gox.GoxSocketPhaseProtocol, event, true, cause))
	}
}

func (r *Runtime) emitClusterSocketEvent(target server.ClusterTarget, event string, values []any) {
	if r == nil || event == "" {
		return
	}
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	if cluster != nil {
		packets, err := r.encodeClusterEventPackets(event, values)
		if err != nil {
			r.sessionStorageMu.RUnlock()
			r.dispatchClusterError(cluster, "encode", "Distributed WebSocket event could not be encoded", err)
			return
		}
		for _, packet := range packets {
			publishCtx, publishCancel := clusterOperationContext()
			err := cluster.PublishSocketPacket(publishCtx, target, packet)
			publishCancel()
			if err != nil {
				r.sessionStorageMu.RUnlock()
				r.dispatchClusterError(cluster, "publish", "Distributed WebSocket packet could not be published", err)
				return
			}
		}
	}
	r.sessionStorageMu.RUnlock()
	for _, socket := range r.clusterTargetSockets(target) {
		if socket != nil && socket.ID() != target.ExcludeSocketID {
			socket.Emit(event, values...)
		}
	}
}

func (r *Runtime) emitChannelEvent(channelID, event string, values []any) error {
	if r == nil || channelID == "" || event == "" {
		return nil
	}
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	if cluster != nil {
		packets, err := r.encodeClusterChannelEventPackets(channelID, event, values)
		if err != nil {
			r.sessionStorageMu.RUnlock()
			r.dispatchClusterError(cluster, "encode-channel", "Distributed Channel event could not be encoded", err)
			return err
		}
		target := server.ClusterTarget{Kind: server.ClusterTargetChannel, ChannelID: channelID}
		for _, packet := range packets {
			publishCtx, publishCancel := clusterOperationContext()
			err = cluster.PublishSocketPacket(publishCtx, target, packet)
			publishCancel()
			if err != nil {
				r.sessionStorageMu.RUnlock()
				r.dispatchClusterError(cluster, "publish-channel", "Distributed Channel packet could not be published", err)
				return err
			}
		}
	}
	r.sessionStorageMu.RUnlock()
	var first error
	for _, socket := range r.localChannelMembers(channelID) {
		if err := gox.EmitSocketChannelEvent(socket, channelID, event, values); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (r *Runtime) disconnectClusterSockets(target server.ClusterTarget, targetInstanceID string, code gox.WSCloseCode) {
	if r == nil {
		return
	}
	if code == gox.WSCloseDefault {
		code = gox.WSCloseNormal
	}
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	if !validWebSocketClose(code) {
		r.sessionStorageMu.RUnlock()
		r.dispatchClusterError(cluster, "disconnect-code", "Distributed WebSocket disconnect code is invalid", fmt.Errorf("invalid WebSocket close code %d", code))
		return
	}
	if cluster != nil {
		publishCtx, publishCancel := clusterOperationContext()
		err := cluster.PublishSocketDisconnect(publishCtx, target, targetInstanceID, uint16(code))
		publishCancel()
		if err != nil {
			r.sessionStorageMu.RUnlock()
			r.dispatchClusterError(cluster, "publish-disconnect", "Distributed WebSocket disconnect could not be published", err)
			return
		}
	}
	r.sessionStorageMu.RUnlock()
	for _, socket := range r.clusterTargetSockets(target) {
		if socket != nil && socket.ID() != target.ExcludeSocketID {
			socket.Disconnect(code)
		}
	}
}

func (r *Runtime) encodeClusterEventPackets(event string, values []any) ([][]byte, error) {
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers(values, func() uint64 {
		id := r.clusterTransferID.Add(1)
		if id == 0 {
			id = r.clusterTransferID.Add(1)
		}
		return id
	}, r.webSocketClientAssets())
	if err != nil {
		return nil, err
	}
	packets := make([][]byte, 0, len(transfers)*3+1)
	for _, transfer := range transfers {
		encoded, err := encodeClusterTransferPackets(transfer)
		if err != nil {
			return nil, err
		}
		packets = append(packets, encoded...)
	}
	eventID := r.clusterEventID.Add(1)
	if eventID == 0 {
		eventID = r.clusterEventID.Add(1)
	}
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketEvent,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte(event)},
			{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(eventID)},
		},
		Arguments: arguments,
	})
	if err != nil {
		return nil, err
	}
	return append(packets, packet), nil
}

func (r *Runtime) encodeClusterChannelEventPackets(channelID, event string, values []any) ([][]byte, error) {
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers(values, func() uint64 {
		id := r.clusterTransferID.Add(1)
		if id == 0 {
			id = r.clusterTransferID.Add(1)
		}
		return id
	}, r.webSocketClientAssets())
	if err != nil {
		return nil, err
	}
	packets := make([][]byte, 0, len(transfers)*3+1)
	for _, transfer := range transfers {
		encoded, err := encodeClusterTransferPackets(transfer)
		if err != nil {
			return nil, err
		}
		packets = append(packets, encoded...)
	}
	eventID := r.clusterEventID.Add(1)
	if eventID == 0 {
		eventID = r.clusterEventID.Add(1)
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
		return nil, err
	}
	return append(packets, packet), nil
}

func encodeClusterTransferPackets(transfer websocketmeta.Transfer) ([][]byte, error) {
	reader, err := transfer.File.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	declaredSize := webSocketUnknownTransferSize
	if transfer.Size >= 0 {
		declaredSize = uint64(transfer.Size)
	}
	begin, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketTransferBegin,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(transfer.ID)},
			{Type: websocketmeta.HeaderTotalSize, Value: websocketmeta.EncodeCompactUint(declaredSize)},
			{Type: websocketmeta.HeaderFileName, Value: []byte(transfer.Name)},
			{Type: websocketmeta.HeaderMIMEType, Value: []byte(transfer.MIMEType)},
		},
	})
	if err != nil {
		return nil, err
	}
	packets := [][]byte{begin}
	buffer := make([]byte, webSocketTransferChunkSize)
	var offset uint64
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			chunk, err := websocketmeta.EncodePacket(websocketmeta.Packet{
				Type: websocketmeta.PacketTransferChunk,
				Headers: []websocketmeta.Header{
					{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(transfer.ID)},
					{Type: websocketmeta.HeaderSequence, Value: websocketmeta.EncodeCompactUint(offset)},
				},
				Payload: buffer[:n],
			})
			if err != nil {
				return nil, err
			}
			packets = append(packets, chunk)
			offset += uint64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if transfer.Size >= 0 && offset != uint64(transfer.Size) {
		return nil, fmt.Errorf("cluster file size changed while streaming")
	}
	finish, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketTransferEnd,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(transfer.ID)},
			{Type: websocketmeta.HeaderTotalSize, Value: websocketmeta.EncodeCompactUint(offset)},
		},
	})
	if err != nil {
		return nil, err
	}
	return append(packets, finish), nil
}

func (r *Runtime) startClusterEventSubscription(cluster server.ClusterRuntime) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err := cluster.SubscribeClusterEvents(ctx, func(event server.ClusterEvent) {
			if event.InstanceID == cluster.InstanceID() {
				return
			}
			if event.Kind == server.ClusterEventChannelClosed {
				r.channelMu.RLock()
				channel := r.channelInstances[event.Target.ChannelID]
				r.channelMu.RUnlock()
				if channel != nil {
					r.closeLocalChannelInstance(channel, gox.ChannelDisconnectClosed)
					_ = channel.Close()
				}
				return
			}
			if event.Kind == server.ClusterEventSocketResponse {
				if event.TargetInstanceID != cluster.InstanceID() {
					return
				}
				packet, err := websocketmeta.DecodePacket(event.Payload)
				if err != nil || (packet.Type != websocketmeta.PacketResponse && packet.Type != websocketmeta.PacketError) {
					if err == nil {
						err = fmt.Errorf("packet type %d cannot complete a distributed request", packet.Type)
					}
					r.dispatchClusterError(cluster, "decode-response", "Distributed WebSocket response payload is malformed", err)
					return
				}
				r.clusterRequestCoordinator().deliver(event.CorrelationID, event.Payload)
				return
			}
			if event.Kind == server.ClusterEventSocketRequest {
				sockets := r.clusterTargetSockets(event.Target)
				if len(sockets) == 0 {
					return
				}
				if err := gox.RouteSocketClusterRequest(sockets[0], event.InstanceID, event.CorrelationID, event.Payload); err != nil {
					r.dispatchClusterError(cluster, "route-request", "Distributed WebSocket request could not be routed", err)
				}
				return
			}
			if event.Kind == server.ClusterEventSocketRequestCancel {
				for _, socket := range r.webSocketRegistry().all() {
					if gox.CancelSocketClusterRequest(socket, event.CorrelationID) {
						break
					}
				}
				return
			}
			if event.Kind == server.ClusterEventSocketDisconnect {
				if event.TargetInstanceID != "" && event.TargetInstanceID != cluster.InstanceID() {
					return
				}
				code := gox.WSCloseCode(event.CloseCode)
				if !validWebSocketClose(code) {
					r.dispatchClusterError(cluster, "route-disconnect", "Distributed WebSocket disconnect code is invalid", fmt.Errorf("invalid WebSocket close code %d", code))
					return
				}
				for _, socket := range r.clusterTargetSockets(event.Target) {
					if socket != nil && socket.ID() != event.Target.ExcludeSocketID {
						socket.Disconnect(code)
					}
				}
				return
			}
			if event.Kind != server.ClusterEventSocketPacket {
				return
			}
			packet, err := websocketmeta.DecodePacket(event.Payload)
			if err != nil || !clusterOutboundPacket(packet.Type) {
				if err == nil {
					err = fmt.Errorf("packet type %d cannot be routed from cluster to client", packet.Type)
				}
				r.dispatchClusterError(cluster, "decode-websocket", "Distributed WebSocket payload is malformed", err)
				return
			}
			eventName := websocketmeta.EventName(packet)
			if eventName == "" {
				eventName = "cluster"
			}
			for _, socket := range r.clusterTargetSockets(event.Target) {
				if socket == nil || socket.ID() == event.Target.ExcludeSocketID {
					continue
				}
				if err := gox.WriteSocketPacket(socket, eventName, event.Payload); err != nil {
					r.dispatchClusterError(cluster, "route", "Distributed WebSocket packet could not be written", err)
				}
			}
		}, func(err error) {
			r.dispatchClusterError(cluster, "decode-envelope", "Distributed cluster envelope is malformed", err)
		})
		if err != nil && ctx.Err() == nil {
			r.dispatchClusterError(cluster, "subscribe", "Cluster subscription stopped unexpectedly", err)
		}
	}()
	return cancel
}

func clusterOutboundPacket(packetType websocketmeta.PacketType) bool {
	return packetType == websocketmeta.PacketEvent || packetType == websocketmeta.PacketTransferBegin ||
		packetType == websocketmeta.PacketTransferChunk || packetType == websocketmeta.PacketTransferEnd ||
		packetType == websocketmeta.PacketTransferAbort || packetType == websocketmeta.PacketChannelEvent
}

func (r *Runtime) clusterTargetSockets(target server.ClusterTarget) []*gox.Socket {
	registry := r.webSocketRegistry()
	switch target.Kind {
	case server.ClusterTargetAll:
		return registry.all()
	case server.ClusterTargetSession:
		return registry.findBySessionID(target.SessionID)
	case server.ClusterTargetSocket:
		socket, ok := registry.findByID(target.SocketID)
		if ok {
			return []*gox.Socket{socket}
		}
	case server.ClusterTargetGroup:
		return r.targetGroupSockets(target.GroupName)
	case server.ClusterTargetChannel:
		return r.targetChannelSockets(target.ChannelID)
	}
	return []*gox.Socket{}
}

func (r *Runtime) dispatchClusterError(cluster server.ClusterRuntime, operation, message string, cause error) {
	adapter := ""
	if cluster != nil {
		adapter = cluster.GetName()
	}
	gox.DispatchClusterError(gox.ClusterError{Adapter: adapter, Operation: operation, Message: message, Cause: cause})
}
