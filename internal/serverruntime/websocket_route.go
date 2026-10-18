package serverruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	websocketmeta "github.com/gox-lang/server/internal/websocket"

	"github.com/gorilla/websocket"
	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const webSocketModuleRoutePath = "/_gox/websocket/module"

func (r *Runtime) webSocketClientRoute(ctx *gox.Context, req *http.Request) bool {
	if !r.webSocketRouteEnabled(ctx, req, websocketmeta.ClientPath) {
		return false
	}

	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		ctx.Response.Headers.Set("Allow", "GET, HEAD")
		r.renderHTTPStatusError(ctx, req, nil, http.StatusMethodNotAllowed, "method not allowed")
		return true
	}

	ctx.Response.Headers.Set("Content-Type", "text/javascript; charset=utf-8")
	ctx.Response.Headers.Set("Cache-Control", "no-store")
	if req.Method == http.MethodGet {
		ctx.Writer.WriteRawBytes(websocketmeta.ClientSource)
	}
	return true
}

func (r *Runtime) webSocketHandshakeRoute(ctx *gox.Context, req *http.Request) bool {
	if !r.webSocketRouteEnabled(ctx, req, websocketmeta.HandshakePath) {
		return false
	}

	if req.Method != http.MethodGet {
		ctx.Response.Headers.Set("Allow", "GET")
		r.renderHTTPStatusError(ctx, req, nil, http.StatusMethodNotAllowed, "method not allowed")
		return true
	}

	ctx.Response.Headers.Set("Cache-Control", "no-store")

	route, handler, buildVersion, goxErr := r.ensureWebSocketReady(ctx)
	if goxErr != nil {
		r.renderGoxError(ctx, req, nil, goxErr)
		return true
	}

	socket := &gox.Socket{
		Session:   ctx.Session,
		Handshake: gox.NewSocketHandshake(ctx.Request),
	}
	assignSocketID := gox.BindSocketIdentity(socket)
	if assignSocketID == nil {
		r.renderHTTPStatusError(ctx, req, nil, http.StatusInternalServerError, "WebSocket identity is unavailable")
		return true
	}
	if err := executeWebSocketIndex(route, ctx, socket, handler); err != nil {
		r.renderGoxError(ctx, req, nil, err)
		return true
	}

	allowed, goxErr := authorizeWebSocketHandshake(route, ctx, socket, req)
	if goxErr != nil {
		dispatchWebSocketError(socket, newWebSocketHandlerError("handshake", goxErr))
		r.renderGoxError(ctx, req, nil, goxErr)
		return true
	}
	if !allowed {
		r.renderHTTPStatusError(ctx, req, nil, http.StatusForbidden, "WebSocket handshake rejected")
		return true
	}

	state, ok := runtimeResponse(ctx)
	if !ok || state.writer == nil {
		r.renderHTTPStatusError(ctx, req, nil, http.StatusInternalServerError, "WebSocket response is unavailable")
		return true
	}
	config := r.stateFor(ctx).Config.WebSocket
	upgrader := websocket.Upgrader{
		HandshakeTimeout:  config.HandshakeTimeout,
		ReadBufferSize:    config.Buffers.ReadSize,
		WriteBufferSize:   config.Buffers.WriteSize,
		EnableCompression: config.Compression.Enabled,
		CheckOrigin:       func(*http.Request) bool { return true },
	}
	state.applyHeaders()
	conn, err := upgrader.Upgrade(state.writer, req, nil)
	if err != nil {
		return true // Gorilla already wrote the HTTP handshake error.
	}
	state.MarkUpgraded()
	ctx.Response.Status.SetCode(http.StatusSwitchingProtocols)
	if config.MaxMessageSize > 0 {
		conn.SetReadLimit(config.MaxMessageSize)
	}
	if config.Compression.Enabled {
		conn.EnableWriteCompression(true)
		if config.Compression.Level != 0 {
			if err := conn.SetCompressionLevel(config.Compression.Level); err != nil {
				_ = conn.Close()
				dispatchWebSocketError(socket, gox.NewGoxSocketError(
					gox.GoxSocketConnectionFailed,
					"WebSocket compression could not be configured",
					gox.GoxSocketPhaseConnection,
					"connect",
					false,
					err,
				))
				return true
			}
		}
	}

	registry := r.webSocketRegistry()
	connection := &webSocketRuntimeConnection{
		conn:            conn,
		socket:          socket,
		registry:        registry,
		runtime:         r,
		writeTimeout:    config.WriteTimeout,
		maxTransferSize: config.MaxMessageSize,
	}
	gox.BindSocketSiblingsEmit(socket, func() []*gox.Socket {
		sockets := r.findClusterSocketsBySessionID(socket.Session.ID())
		result := make([]*gox.Socket, 0, len(sockets))
		for _, sibling := range sockets {
			if sibling != nil && sibling.ID() != socket.ID() {
				result = append(result, sibling)
			}
		}
		return result
	}, func(event string, values []any) {
		connection.runtime.emitClusterSocketEvent(server.ClusterTarget{
			Kind: server.ClusterTargetSession, SessionID: socket.Session.ID(), ExcludeSocketID: socket.ID(),
		}, event, values)
	})
	gox.BindSocketSiblingsDisconnect(socket, func(code gox.WSCloseCode) {
		connection.runtime.disconnectClusterSockets(server.ClusterTarget{
			Kind: server.ClusterTargetSession, SessionID: socket.Session.ID(), ExcludeSocketID: socket.ID(),
		}, "", code)
	})
	gox.BindSocketDisconnect(socket, connection.disconnect)
	gox.BindSocketEmit(socket, connection.emit)
	gox.BindSocketPacket(socket, connection.writeBinary)
	gox.BindSocketRequest(socket, connection.request)
	gox.BindSocketClusterRequest(socket, connection.routeClusterRequest)
	gox.BindSocketClusterCancel(socket, connection.cancelClusterRequest)
	gox.BindSocketChannelTransport(socket, gox.SocketChannelTransport{Emit: connection.emitChannel, Leave: connection.sendChannelLeave})
	gox.BindSocketLocation(socket, r.instanceID, true)
	r.bindSocketGroups(socket)
	if err := registry.register(socket, server.DetachSession(ctx.Session), assignSocketID); err != nil {
		_ = conn.Close()
		dispatchWebSocketError(socket, gox.NewGoxSocketError(
			gox.GoxSocketConnectionFailed,
			"WebSocket connection could not be registered",
			gox.GoxSocketPhaseConnection,
			"connect",
			false,
			err,
		))
		return true
	}
	if err := r.registerSocketDirectory(socket); err != nil {
		registry.unregister(socket.ID(), socket)
		_ = conn.Close()
		_, cluster := r.socketDirectory()
		r.dispatchSocketDirectoryError(cluster, "register", err)
		dispatchWebSocketError(socket, gox.NewGoxSocketError(
			gox.GoxSocketConnectionFailed,
			"WebSocket connection could not be registered in the distributed directory",
			gox.GoxSocketPhaseConnection,
			"connect",
			false,
			err,
		))
		return true
	}
	go connection.runReadLoop()
	route.mu.Lock()
	stale := route.BuildVersion != buildVersion
	route.mu.Unlock()
	if stale {
		socket.Disconnect(gox.WSCloseReconnect)
		return true
	}
	dispatchWebSocketConnect(socket, req.URL.Query().Get("_gox_reconnect") == "1")
	return true
}

func authorizeWebSocketHandshake(route *GoxRouteRuntime, ctx *gox.Context, socket *gox.Socket, req *http.Request) (bool, *GoxError) {
	value, handled, err := socket.DispatchWhen("handshake", ctx)
	if err != nil {
		return false, newGoxError(gox.GoxErrorDispatch, route, ctx, "WebSocket handshake handler failed", err)
	}
	if handled {
		allowed, ok := value.(bool)
		if !ok {
			err := fmt.Errorf("socket.When(%q) must return bool, received %T", "handshake", value)
			return false, newGoxError(gox.GoxErrorDispatch, route, ctx, "invalid WebSocket handshake result", err)
		}
		return allowed, nil
	}
	return websocket.IsWebSocketUpgrade(req) && sameWebSocketOrigin(req, ctx.Request.URL.Scheme()), nil
}

func sameWebSocketOrigin(req *http.Request, protocols ...string) bool {
	origins := req.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(origins[0])
	protocol := scheme(req)
	if len(protocols) > 0 {
		protocol = protocols[0]
	}
	return err == nil && origin.Scheme == protocol && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == "" && origin.Host != "" && strings.EqualFold(origin.Host, req.Host)
}

func dispatchWebSocketConnect(socket *gox.Socket, isReconnect bool) {
	if _, _, err := socket.DispatchWhen("connect", isReconnect); err != nil {
		dispatchWebSocketError(socket, newWebSocketHandlerError("connect", err))
	}
}

func dispatchWebSocketError(socket *gox.Socket, err gox.GoxSocketError) {
	if err.Code == "" {
		return
	}
	_, _, _ = socket.DispatchWhen("error", err)
}

func newWebSocketHandlerError(event string, cause error) gox.GoxSocketError {
	return gox.NewGoxSocketError(
		gox.GoxSocketHandlerFailed,
		"WebSocket handler failed",
		gox.GoxSocketPhaseHandler,
		event,
		false,
		cause,
	)
}

type webSocketRuntimeConnection struct {
	conn            *websocket.Conn
	socket          *gox.Socket
	registry        *webSocketRegistry
	runtime         *Runtime
	writeTimeout    time.Duration
	maxTransferSize int64

	closeOnce               sync.Once
	mu                      sync.Mutex
	writeMu                 sync.Mutex
	nextEventID             atomic.Uint64
	nextTransferID          atomic.Uint64
	nextRequestID           atomic.Uint64
	local                   *gox.WSDisconnectReason
	incoming                map[uint64]*webSocketIncomingTransfer
	requestMu               sync.Mutex
	pendingRequests         map[uint64]*webSocketPendingRequest
	clusterRequests         map[uint64]*webSocketClusterRequest
	clusterRequestIDs       map[[clusterCorrelationSize]byte]uint64
	ignoredClusterResponses map[uint64]time.Time
}

const webSocketTransferChunkSize = 64 << 10
const webSocketUnknownTransferSize uint64 = 1<<53 - 1

var webSocketTransferBuffers = sync.Pool{New: func() any {
	buffer := make([]byte, webSocketTransferChunkSize)
	return &buffer
}}

type webSocketIncomingTransfer struct {
	name     string
	mimeType string
	total    uint64
	data     []byte
	complete bool
	unknown  bool
}

type webSocketPendingRequest struct {
	event   string
	out     any
	done    chan struct{}
	castErr error
}

type webSocketClusterRequest struct {
	originInstanceID string
	correlationID    []byte
	correlationKey   [clusterCorrelationSize]byte
	event            string
}

func (connection *webSocketRuntimeConnection) routeClusterRequest(originInstanceID string, correlationID []byte, packetData []byte) error {
	correlationKey, ok := clusterCorrelationKey(correlationID)
	if !ok || originInstanceID == "" {
		return errClusterCorrelation
	}
	packet, err := websocketmeta.DecodePacket(packetData)
	if err != nil || packet.Type != websocketmeta.PacketRequest || packet.Flags != 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("distributed socket payload must contain one public REQUEST packet")
	}
	for _, argument := range packet.Arguments {
		if argument.Type == websocketmeta.ArgumentFile || argument.Type == websocketmeta.ArgumentFiles {
			return fmt.Errorf("distributed socket request transfers are not enabled yet")
		}
	}
	requestID := connection.nextRequestID.Add(1)
	if requestID == 0 {
		requestID = connection.nextRequestID.Add(1)
	}
	packet.Headers = []websocketmeta.Header{
		{Type: websocketmeta.HeaderEventName, Value: []byte(websocketmeta.EventName(packet))},
		{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)},
	}
	encoded, err := websocketmeta.EncodePacket(packet)
	if err != nil {
		return err
	}
	route := &webSocketClusterRequest{
		originInstanceID: originInstanceID,
		correlationID:    append([]byte(nil), correlationID...),
		correlationKey:   correlationKey,
		event:            websocketmeta.EventName(packet),
	}
	connection.requestMu.Lock()
	if connection.clusterRequests == nil {
		connection.clusterRequests = make(map[uint64]*webSocketClusterRequest)
		connection.clusterRequestIDs = make(map[[clusterCorrelationSize]byte]uint64)
	}
	if _, exists := connection.clusterRequestIDs[correlationKey]; exists {
		connection.requestMu.Unlock()
		return fmt.Errorf("distributed socket correlation is already active")
	}
	connection.clusterRequests[requestID] = route
	connection.clusterRequestIDs[correlationKey] = requestID
	connection.requestMu.Unlock()
	if err := connection.writeBinary(route.event, encoded); err != nil {
		connection.removeClusterRequest(requestID, route)
		return err
	}
	return nil
}

func (connection *webSocketRuntimeConnection) removeClusterRequest(requestID uint64, expected *webSocketClusterRequest) *webSocketClusterRequest {
	connection.requestMu.Lock()
	defer connection.requestMu.Unlock()
	route := connection.clusterRequests[requestID]
	if route == nil || (expected != nil && route != expected) {
		return nil
	}
	delete(connection.clusterRequests, requestID)
	delete(connection.clusterRequestIDs, route.correlationKey)
	return route
}

func (connection *webSocketRuntimeConnection) cancelClusterRequest(correlationID []byte) bool {
	key, ok := clusterCorrelationKey(correlationID)
	if !ok {
		return false
	}
	connection.requestMu.Lock()
	defer connection.requestMu.Unlock()
	requestID, ok := connection.clusterRequestIDs[key]
	if !ok {
		return false
	}
	route := connection.clusterRequests[requestID]
	if route == nil {
		return false
	}
	delete(connection.clusterRequests, requestID)
	delete(connection.clusterRequestIDs, key)
	if connection.ignoredClusterResponses == nil {
		connection.ignoredClusterResponses = make(map[uint64]time.Time)
	}
	now := time.Now()
	for id, expires := range connection.ignoredClusterResponses {
		if !expires.After(now) {
			delete(connection.ignoredClusterResponses, id)
		}
	}
	connection.ignoredClusterResponses[requestID] = now.Add(time.Minute)
	return true
}

func (connection *webSocketRuntimeConnection) emit(event string, values []any) error {
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers(values, func() uint64 {
		id := connection.nextTransferID.Add(1)
		if id == 0 {
			id = connection.nextTransferID.Add(1)
		}
		return id
	}, connection.runtime.webSocketClientAssets())
	if err != nil {
		return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket event arguments could not be encoded", gox.GoxSocketPhaseProtocol, event, false, err)
	}
	eventID := connection.nextEventID.Add(1)
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketEvent,
		Headers: []websocketmeta.Header{{
			Type:  websocketmeta.HeaderEventName,
			Value: []byte(event),
		}, {
			Type:  websocketmeta.HeaderEventID,
			Value: websocketmeta.EncodeCompactUint(eventID),
		}},
		Arguments: arguments,
	})
	if err != nil {
		return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket event packet could not be encoded", gox.GoxSocketPhaseProtocol, event, false, err)
	}
	for _, transfer := range transfers {
		if err := connection.writeTransfer(event, transfer); err != nil {
			return err
		}
	}
	return connection.writeBinary(event, packet)
}

func (connection *webSocketRuntimeConnection) request(event string, values []any, out any) error {
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers(values, func() uint64 {
		id := connection.nextTransferID.Add(1)
		if id == 0 {
			id = connection.nextTransferID.Add(1)
		}
		return id
	}, connection.runtime.webSocketClientAssets())
	if err != nil {
		dispatchWebSocketError(connection.socket, gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket request arguments could not be encoded", gox.GoxSocketPhaseProtocol, event, false, err))
		return nil
	}
	requestID := connection.nextRequestID.Add(1)
	if requestID == 0 {
		requestID = connection.nextRequestID.Add(1)
	}
	pending := &webSocketPendingRequest{event: event, out: out, done: make(chan struct{})}
	connection.requestMu.Lock()
	if connection.pendingRequests == nil {
		connection.pendingRequests = make(map[uint64]*webSocketPendingRequest)
	}
	connection.pendingRequests[requestID] = pending
	connection.requestMu.Unlock()
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketRequest,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte(event)},
			{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)},
		},
		Arguments: arguments,
	})
	if err == nil {
		for _, transfer := range transfers {
			if err = connection.writeTransfer(event, transfer); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = connection.writeBinary(event, packet)
	}
	if err != nil {
		connection.removePendingRequest(requestID, pending)
		socketErr, ok := err.(gox.GoxSocketError)
		if !ok {
			socketErr = gox.NewGoxSocketError(gox.GoxSocketTransportError, "WebSocket request could not be sent", gox.GoxSocketPhaseTransport, event, true, err)
		}
		dispatchWebSocketError(connection.socket, socketErr)
		return nil
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-pending.done:
		return pending.castErr
	case <-timer.C:
		if connection.removePendingRequest(requestID, pending) {
			dispatchWebSocketError(connection.socket, gox.NewGoxSocketError(gox.GoxSocketTransportError, "WebSocket request timed out", gox.GoxSocketPhaseProtocol, event, true, nil))
			return nil
		}
		<-pending.done
		return pending.castErr
	}
}

func (connection *webSocketRuntimeConnection) removePendingRequest(requestID uint64, expected *webSocketPendingRequest) bool {
	connection.requestMu.Lock()
	defer connection.requestMu.Unlock()
	pending, ok := connection.pendingRequests[requestID]
	if !ok || pending != expected {
		return false
	}
	delete(connection.pendingRequests, requestID)
	return true
}

func (connection *webSocketRuntimeConnection) writeTransfer(event string, transfer websocketmeta.Transfer) error {
	if connection.maxTransferSize > 0 && transfer.Size >= 0 && transfer.Size > connection.maxTransferSize {
		return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket file exceeds MaxMessageSize", gox.GoxSocketPhaseProtocol, event, false, nil)
	}
	reader, err := transfer.File.Open()
	if err != nil {
		return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket file could not be opened", gox.GoxSocketPhaseProtocol, event, false, err)
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
		return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket file transfer could not be encoded", gox.GoxSocketPhaseProtocol, event, false, err)
	}
	if err := connection.writeBinary(event, begin); err != nil {
		return err
	}
	bufferPtr := webSocketTransferBuffers.Get().(*[]byte)
	buffer := *bufferPtr
	defer webSocketTransferBuffers.Put(bufferPtr)
	var offset uint64
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if connection.maxTransferSize > 0 && offset+uint64(n) > uint64(connection.maxTransferSize) {
				_ = connection.writeTransferAbort(event, transfer.ID, "FILE_TOO_LARGE")
				return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket file exceeds MaxMessageSize", gox.GoxSocketPhaseProtocol, event, false, nil)
			}
			chunk, err := websocketmeta.EncodePacket(websocketmeta.Packet{
				Type: websocketmeta.PacketTransferChunk,
				Headers: []websocketmeta.Header{
					{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(transfer.ID)},
					{Type: websocketmeta.HeaderSequence, Value: websocketmeta.EncodeCompactUint(offset)},
				},
				Payload: buffer[:n],
			})
			if err != nil {
				return err
			}
			if err := connection.writeBinary(event, chunk); err != nil {
				return err
			}
			offset += uint64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = connection.writeTransferAbort(event, transfer.ID, "READ_FAILED")
			return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket file could not be read", gox.GoxSocketPhaseProtocol, event, false, readErr)
		}
	}
	if transfer.Size >= 0 && offset != uint64(transfer.Size) {
		_ = connection.writeTransferAbort(event, transfer.ID, "SIZE_MISMATCH")
		return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket file size changed while streaming", gox.GoxSocketPhaseProtocol, event, false, nil)
	}
	finish, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketTransferEnd,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(transfer.ID)},
			{Type: websocketmeta.HeaderTotalSize, Value: websocketmeta.EncodeCompactUint(offset)},
		},
	})
	if err != nil {
		return err
	}
	return connection.writeBinary(event, finish)
}

func (connection *webSocketRuntimeConnection) writeTransferAbort(event string, id uint64, code string) error {
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{Type: websocketmeta.PacketTransferAbort, Headers: []websocketmeta.Header{
		{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(id)},
		{Type: websocketmeta.HeaderErrorCode, Value: []byte(code)},
	}})
	if err != nil {
		return err
	}
	return connection.writeBinary(event, packet)
}

func (connection *webSocketRuntimeConnection) writeBinary(event string, packet []byte) error {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if connection.writeTimeout > 0 {
		_ = connection.conn.SetWriteDeadline(time.Now().Add(connection.writeTimeout))
	}
	if err := connection.conn.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		return gox.NewGoxSocketError(gox.GoxSocketTransportError, "WebSocket event could not be written", gox.GoxSocketPhaseTransport, event, true, err)
	}
	return nil
}

func (connection *webSocketRuntimeConnection) disconnect(code gox.WSCloseCode, reason string) {
	if !validWebSocketClose(code) || !utf8.ValidString(reason) || len([]byte(reason)) > 123 {
		dispatchWebSocketError(connection.socket, gox.NewGoxSocketError(
			gox.GoxSocketInvalidState,
			"Invalid WebSocket close code or reason",
			gox.GoxSocketPhaseConnection,
			"disconnect",
			false,
			nil,
		))
		return
	}
	connection.closeOnce.Do(func() {
		info := gox.WSDisconnectReason{
			Code:          code,
			Reason:        reason,
			Clean:         true,
			Initiator:     gox.WSDisconnectInitiatorServer,
			WillReconnect: webSocketCloseShouldReconnect(code),
		}
		connection.mu.Lock()
		connection.local = &info
		connection.mu.Unlock()

		deadline := time.Time{}
		if connection.writeTimeout > 0 {
			deadline = time.Now().Add(connection.writeTimeout)
		}
		if err := connection.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(int(code), reason), deadline); err != nil {
			dispatchWebSocketError(connection.socket, gox.NewGoxSocketError(
				gox.GoxSocketTransportError,
				"WebSocket close frame could not be written",
				gox.GoxSocketPhaseTransport,
				"disconnect",
				true,
				err,
			))
		}
		_ = connection.conn.Close()
	})
}

func (connection *webSocketRuntimeConnection) runReadLoop() {
	var readErr error
	defer func() {
		_ = connection.conn.Close()
		reason := connection.disconnectReason(readErr)
		if connection.runtime != nil {
			connection.runtime.leaveAllSocketGroups(connection.socket)
		}
		if connection.runtime != nil {
			connection.runtime.leaveAllChannels(connection.socket, gox.ChannelDisconnectSocketDisconnected)
		}
		if connection.registry != nil {
			if connection.registry.unregister(connection.socket.ID(), connection.socket) && connection.runtime != nil {
				connection.runtime.unregisterSocketDirectory(connection.socket.ID())
				if len(connection.registry.findBySessionID(connection.socket.Session.ID())) == 0 {
					connection.runtime.clearSessionTouch(connection.socket.Session.ID())
				}
			}
		}
		connection.incoming = nil
		connection.cancelPendingRequests()
		if _, _, err := connection.socket.DispatchWhen("disconnect", reason); err != nil {
			dispatchWebSocketError(connection.socket, newWebSocketHandlerError("disconnect", err))
		}
	}()
	for {
		messageType, reader, err := connection.conn.NextReader()
		if err != nil {
			readErr = err
			connection.mu.Lock()
			local := connection.local != nil
			connection.mu.Unlock()
			if !local {
				if _, ok := err.(*websocket.CloseError); !ok {
					dispatchWebSocketError(connection.socket, gox.NewGoxSocketError(
						gox.GoxSocketTransportError,
						"WebSocket transport failed",
						gox.GoxSocketPhaseTransport,
						"read",
						true,
						err,
					))
				}
			}
			return
		}
		if messageType != websocket.BinaryMessage {
			connection.failProtocol("WebSocket text frames are not supported", nil)
			return
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			connection.failProtocol("WebSocket binary frame could not be read", err)
			return
		}
		packet, err := websocketmeta.DecodePacket(data)
		if err != nil {
			connection.failProtocol("Malformed GOX WebSocket packet", err)
			return
		}
		if packet.Type != websocketmeta.PacketEvent && packet.Type != websocketmeta.PacketError && packet.Type != websocketmeta.PacketRequest && packet.Type != websocketmeta.PacketResponse &&
			packet.Type != websocketmeta.PacketTransferBegin && packet.Type != websocketmeta.PacketTransferChunk &&
			packet.Type != websocketmeta.PacketTransferEnd && packet.Type != websocketmeta.PacketTransferAbort &&
			packet.Type != websocketmeta.PacketChannelJoin && packet.Type != websocketmeta.PacketChannelLeave &&
			packet.Type != websocketmeta.PacketChannelEvent && packet.Type != websocketmeta.PacketChannelRequest && packet.Type != websocketmeta.PacketChannelError {
			connection.failProtocol("GOX WebSocket packet type is not valid in this protocol stage", nil)
			return
		}
		switch packet.Type {
		case websocketmeta.PacketEvent:
			connection.dispatchEvent(packet)
		case websocketmeta.PacketError:
			if !connection.dispatchRemoteError(packet) {
				return
			}
		case websocketmeta.PacketResponse:
			if err := connection.dispatchResponse(packet); err != nil {
				connection.failProtocol("Invalid GOX WebSocket response", err)
				return
			}
		case websocketmeta.PacketRequest:
			var requestErr error
			if packet.Flags == websocketmeta.FlagInternal {
				requestErr = connection.dispatchInternalRequest(packet)
			} else {
				requestErr = connection.dispatchRequest(packet)
			}
			if requestErr != nil {
				connection.failProtocol("Invalid GOX WebSocket request", requestErr)
				return
			}
		case websocketmeta.PacketChannelJoin:
			connection.dispatchChannelJoin(packet)
		case websocketmeta.PacketChannelLeave:
			if err := connection.dispatchChannelLeave(packet); err != nil {
				connection.failProtocol("Invalid GOX Channel leave", err)
				return
			}
		case websocketmeta.PacketChannelEvent:
			connection.dispatchChannelEvent(packet)
		case websocketmeta.PacketChannelRequest:
			connection.dispatchChannelRequest(packet)
		case websocketmeta.PacketChannelError:
			if err := connection.dispatchRemoteChannelError(packet); err != nil {
				connection.failProtocol("Invalid GOX Channel error", err)
				return
			}
		default:
			if err := connection.receiveTransfer(packet); err != nil {
				connection.failProtocol("Invalid GOX WebSocket file transfer", err)
				return
			}
		}
	}
}

func (connection *webSocketRuntimeConnection) cancelPendingRequests() {
	connection.requestMu.Lock()
	pending := connection.pendingRequests
	connection.pendingRequests = nil
	connection.clusterRequests = nil
	connection.clusterRequestIDs = nil
	connection.ignoredClusterResponses = nil
	connection.requestMu.Unlock()
	for _, request := range pending {
		dispatchWebSocketError(connection.socket, gox.NewGoxSocketError(gox.GoxSocketTransportError, "WebSocket request was interrupted", gox.GoxSocketPhaseTransport, request.event, true, nil))
		close(request.done)
	}
}

func (connection *webSocketRuntimeConnection) dispatchResponse(packet websocketmeta.Packet) error {
	if packet.Flags != 0 {
		return fmt.Errorf("public RESPONSE packet contains flags")
	}
	requestID, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderRequestID)
	connection.requestMu.Lock()
	pending, ok := connection.pendingRequests[requestID]
	if ok {
		delete(connection.pendingRequests, requestID)
	}
	route := connection.clusterRequests[requestID]
	if route != nil {
		delete(connection.clusterRequests, requestID)
		delete(connection.clusterRequestIDs, route.correlationKey)
	}
	_, ignored := connection.ignoredClusterResponses[requestID]
	if ignored {
		delete(connection.ignoredClusterResponses, requestID)
	}
	connection.requestMu.Unlock()
	if ignored {
		return nil
	}
	if route != nil {
		encoded, err := websocketmeta.EncodePacket(packet)
		if err != nil {
			return err
		}
		connection.publishClusterResponse(route, encoded)
		return nil
	}
	if !ok {
		return fmt.Errorf("response references unknown request %d", requestID)
	}
	if len(packet.Arguments) != 1 {
		pending.castErr = fmt.Errorf("socket Await expected one reply value, received %d", len(packet.Arguments))
		close(pending.done)
		return nil
	}
	destination := reflect.ValueOf(pending.out)
	values, err := connection.bindPacketArguments(packet, []reflect.Type{destination.Elem().Type()})
	if err != nil {
		pending.castErr = err
		close(pending.done)
		return nil
	}
	if values[0] == nil {
		destination.Elem().Set(reflect.Zero(destination.Elem().Type()))
	} else {
		destination.Elem().Set(reflect.ValueOf(values[0]))
	}
	close(pending.done)
	return nil
}

func (connection *webSocketRuntimeConnection) publishClusterResponse(route *webSocketClusterRequest, payload []byte) {
	if route == nil || connection.runtime == nil {
		return
	}
	connection.runtime.sessionStorageMu.RLock()
	cluster := connection.runtime.clusterRuntime
	if cluster == nil {
		connection.runtime.sessionStorageMu.RUnlock()
		return
	}
	publishCtx, publishCancel := clusterOperationContext()
	err := cluster.PublishSocketResponse(publishCtx, route.originInstanceID, route.correlationID, payload)
	publishCancel()
	connection.runtime.sessionStorageMu.RUnlock()
	if err != nil {
		connection.runtime.dispatchClusterError(cluster, "publish-response", "Distributed WebSocket response could not be published", err)
	}
}

func (connection *webSocketRuntimeConnection) dispatchRequest(packet websocketmeta.Packet) error {
	if packet.Flags != 0 {
		return fmt.Errorf("public REQUEST packet contains flags")
	}
	requestID, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderRequestID)
	event := websocketmeta.EventName(packet)
	signature, handled := connection.socket.OnSignature(event)
	if !handled {
		socketErr := gox.NewGoxSocketError(gox.GoxSocketEventNotFound, "WebSocket event has no registered handler", gox.GoxSocketPhaseHandler, event, false, nil)
		connection.dispatchRequestFailure(requestID, event, socketErr)
		return nil
	}
	if !signature.HasReply {
		socketErr := gox.NewGoxSocketError(gox.GoxSocketInvalidState, "WebSocket event does not declare gox.EventReply", gox.GoxSocketPhaseHandler, event, false, nil)
		connection.dispatchRequestFailure(requestID, event, socketErr)
		return nil
	}
	types, handled, err := connection.socket.OnParameterTypes(event)
	if err != nil || !handled {
		if err == nil {
			err = fmt.Errorf("handler is unavailable")
		}
		connection.dispatchRequestFailure(requestID, event, newWebSocketHandlerError(event, err))
		return nil
	}
	values, err := connection.bindPacketArguments(packet, types)
	if err != nil {
		socketErr := gox.NewGoxSocketError(gox.GoxSocketBindFailed, "WebSocket request arguments do not match the Go handler", gox.GoxSocketPhaseProtocol, event, false, err)
		if binding, ok := err.(*websocketmeta.BindingError); ok {
			socketErr.Code = gox.GoxSocketErrorCode(binding.Code)
			socketErr.Argument = binding.Argument
			socketErr.Received = binding.Received
			socketErr.Expected = binding.Expected
			socketErr.Message = binding.Error()
		}
		connection.dispatchRequestFailure(requestID, event, socketErr)
		return nil
	}
	go func() {
		value, _, dispatchErr := connection.socket.DispatchOn(event, values...)
		sessionErr := connection.persistSessionActivity(event)
		if dispatchErr != nil {
			if sessionErr != nil {
				dispatchWebSocketError(connection.socket, *sessionErr)
			}
			connection.dispatchRequestFailure(requestID, event, newWebSocketHandlerError(event, dispatchErr))
			return
		}
		if replyErr, ok := value.(error); ok && replyErr != nil {
			socketErr := newWebSocketHandlerError(event, replyErr)
			socketErr.Message = replyErr.Error()
			connection.dispatchRequestFailure(requestID, event, socketErr)
			return
		}
		if sessionErr != nil {
			connection.dispatchRequestFailure(requestID, event, *sessionErr)
			return
		}
		if err := connection.sendResponse(requestID, event, value); err != nil {
			socketErr, ok := err.(gox.GoxSocketError)
			if !ok {
				socketErr = newWebSocketHandlerError(event, err)
			}
			connection.dispatchRequestFailure(requestID, event, socketErr)
		}
	}()
	return nil
}

func (connection *webSocketRuntimeConnection) bindPacketArguments(packet websocketmeta.Packet, types []reflect.Type) ([]any, error) {
	files := make(map[uint64]gox.File)
	var referencedTransfers []uint64
	var err error
	for _, argument := range packet.Arguments {
		var ids []uint64
		if argument.Type == websocketmeta.ArgumentFile {
			id, decodeErr := websocketmeta.DecodeFileReference(argument.Data)
			if decodeErr != nil {
				err = decodeErr
				break
			}
			ids = []uint64{id}
		} else if argument.Type == websocketmeta.ArgumentFiles {
			ids, err = websocketmeta.DecodeFilesReference(argument.Data)
			if err != nil {
				break
			}
		}
		for _, id := range ids {
			transfer, exists := connection.incoming[id]
			if !exists || !transfer.complete || (!transfer.unknown && uint64(len(transfer.data)) != transfer.total) {
				err = fmt.Errorf("file transfer %d is incomplete", id)
				break
			}
			file, fileErr := gox.NewFile(gox.FileConfig{Name: transfer.name, Type: transfer.mimeType, Content: bytes.NewReader(transfer.data)})
			if fileErr != nil {
				err = fileErr
				break
			}
			files[id] = file
			referencedTransfers = append(referencedTransfers, id)
		}
		if err != nil {
			break
		}
	}
	var values []any
	if err == nil {
		values, err = websocketmeta.BindArgumentsWithTransfers(packet.Arguments, types, files)
	}
	for _, id := range referencedTransfers {
		delete(connection.incoming, id)
	}
	return values, err
}

func (connection *webSocketRuntimeConnection) sendResponse(requestID uint64, event string, value any) error {
	arguments, transfers, err := websocketmeta.EncodeValuesWithTransfers([]any{value}, func() uint64 {
		id := connection.nextTransferID.Add(1)
		if id == 0 {
			id = connection.nextTransferID.Add(1)
		}
		return id
	}, connection.runtime.webSocketClientAssets())
	if err != nil {
		return gox.NewGoxSocketError(gox.GoxSocketEncodeFailed, "WebSocket reply could not be encoded", gox.GoxSocketPhaseProtocol, event, false, err)
	}
	response, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type:      websocketmeta.PacketResponse,
		Headers:   []websocketmeta.Header{{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)}},
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
	return connection.writeBinary(event, response)
}

func (connection *webSocketRuntimeConnection) dispatchRequestFailure(requestID uint64, event string, socketErr gox.GoxSocketError) {
	dispatchWebSocketError(connection.socket, socketErr)
	connection.sendPublicRequestError(requestID, event, socketErr)
}

func (connection *webSocketRuntimeConnection) sendPublicRequestError(requestID uint64, event string, socketErr gox.GoxSocketError) {
	details, err := json.Marshal(webSocketEventErrorDetails{Message: socketErr.Message, Argument: socketErr.Argument, Received: socketErr.Received, Expected: socketErr.Expected})
	if err != nil {
		return
	}
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketError,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)},
			{Type: websocketmeta.HeaderErrorCode, Value: []byte(socketErr.Code)},
		},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentJSON, Data: details}},
	})
	if err != nil {
		return
	}
	_ = connection.writeBinary(event, packet)
}

func (connection *webSocketRuntimeConnection) dispatchInternalRequest(packet websocketmeta.Packet) error {
	if packet.Flags != websocketmeta.FlagInternal {
		return fmt.Errorf("REQUEST packet must be internal")
	}
	requestID, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderRequestID)
	if websocketmeta.EventName(packet) != "gox:get-signature" {
		return fmt.Errorf("unknown internal request")
	}
	if len(packet.Arguments) != 1 || packet.Arguments[0].Type != websocketmeta.ArgumentText {
		return fmt.Errorf("gox:get-signature requires one TEXT argument")
	}
	event := string(packet.Arguments[0].Data)
	signature, ok := connection.socket.OnSignature(event)
	if !ok {
		connection.sendRequestError(requestID, "SIGNATURE_NOT_FOUND", "WebSocket event signature is unavailable")
		return nil
	}
	for _, name := range signature.ParameterNames {
		if name == "" {
			connection.sendRequestError(requestID, "SIGNATURE_NOT_FOUND", "WebSocket event contains an unnamed parameter")
			return nil
		}
	}
	data, err := json.Marshal(webSocketEventSignature{
		Parameters: signature.ParameterNames,
		HasReply:   signature.HasReply,
	})
	if err != nil {
		return err
	}
	response, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type:      websocketmeta.PacketResponse,
		Flags:     websocketmeta.FlagInternal,
		Headers:   []websocketmeta.Header{{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)}},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentJSON, Data: data}},
	})
	if err != nil {
		return err
	}
	return connection.writeBinary("gox:get-signature", response)
}

type webSocketEventSignature struct {
	Parameters []string `json:"parameters"`
	HasReply   bool     `json:"hasReply"`
}

func (connection *webSocketRuntimeConnection) sendRequestError(requestID uint64, code string, message string) {
	details, err := json.Marshal(webSocketEventErrorDetails{Message: message, Argument: -1})
	if err != nil {
		return
	}
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type:  websocketmeta.PacketError,
		Flags: websocketmeta.FlagInternal,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)},
			{Type: websocketmeta.HeaderErrorCode, Value: []byte(code)},
		},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentJSON, Data: details}},
	})
	if err != nil {
		return
	}
	_ = connection.writeBinary("gox:get-signature", packet)
}

func (connection *webSocketRuntimeConnection) receiveTransfer(packet websocketmeta.Packet) error {
	if connection.incoming == nil {
		connection.incoming = make(map[uint64]*webSocketIncomingTransfer)
	}
	id, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderTransferID)
	switch packet.Type {
	case websocketmeta.PacketTransferBegin:
		if _, exists := connection.incoming[id]; exists {
			return fmt.Errorf("transfer %d already exists", id)
		}
		total, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderTotalSize)
		unknown := total == webSocketUnknownTransferSize
		if !unknown && connection.maxTransferSize > 0 && total > uint64(connection.maxTransferSize) {
			return fmt.Errorf("transfer %d exceeds MaxMessageSize", id)
		}
		if !unknown && total > uint64(^uint(0)>>1) {
			return fmt.Errorf("transfer %d is too large", id)
		}
		capacity := 0
		if !unknown {
			capacity = int(total)
		}
		connection.incoming[id] = &webSocketIncomingTransfer{
			name:     websocketmeta.HeaderText(packet, websocketmeta.HeaderFileName),
			mimeType: websocketmeta.HeaderText(packet, websocketmeta.HeaderMIMEType),
			total:    total,
			data:     make([]byte, 0, capacity),
			unknown:  unknown,
		}
	case websocketmeta.PacketTransferChunk:
		transfer, exists := connection.incoming[id]
		if !exists {
			return fmt.Errorf("transfer %d is unknown", id)
		}
		offset, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderSequence)
		if offset != uint64(len(transfer.data)) {
			return fmt.Errorf("transfer %d chunk position is %d, expected %d", id, offset, len(transfer.data))
		}
		if !transfer.unknown && uint64(len(transfer.data))+uint64(len(packet.Payload)) > transfer.total {
			return fmt.Errorf("transfer %d exceeds its declared size", id)
		}
		if connection.maxTransferSize > 0 && int64(len(transfer.data))+int64(len(packet.Payload)) > connection.maxTransferSize {
			return fmt.Errorf("transfer %d exceeds MaxMessageSize", id)
		}
		transfer.data = append(transfer.data, packet.Payload...)
	case websocketmeta.PacketTransferEnd:
		transfer, exists := connection.incoming[id]
		if !exists {
			return fmt.Errorf("transfer %d is unknown", id)
		}
		total, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderTotalSize)
		if transfer.unknown {
			if total != uint64(len(transfer.data)) {
				return fmt.Errorf("transfer %d ended with an invalid size", id)
			}
			transfer.total = total
			transfer.unknown = false
		} else if total != transfer.total || uint64(len(transfer.data)) != transfer.total {
			return fmt.Errorf("transfer %d ended with an invalid size", id)
		}
		transfer.complete = true
	case websocketmeta.PacketTransferAbort:
		if _, exists := connection.incoming[id]; !exists {
			return fmt.Errorf("transfer %d is unknown", id)
		}
		delete(connection.incoming, id)
	}
	return nil
}

func (connection *webSocketRuntimeConnection) failProtocol(message string, cause error) {
	err := gox.NewGoxSocketError(gox.GoxSocketProtocolError, message, gox.GoxSocketPhaseProtocol, "protocol", false, cause)
	dispatchWebSocketError(connection.socket, err)
	connection.disconnect(gox.WSCloseProtocol, gox.WSCloseReason(gox.WSCloseProtocol))
}

func (connection *webSocketRuntimeConnection) dispatchEvent(packet websocketmeta.Packet) {
	event := websocketmeta.EventName(packet)
	eventID, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderEventID)
	types, handled, err := connection.socket.OnParameterTypes(event)
	if err != nil {
		socketErr := newWebSocketHandlerError(event, err)
		socketErr.EventID = eventID
		dispatchWebSocketError(connection.socket, socketErr)
		connection.sendEventError(socketErr)
		return
	}
	if !handled {
		socketErr := gox.NewGoxSocketError(gox.GoxSocketEventNotFound, "WebSocket event has no registered handler", gox.GoxSocketPhaseHandler, event, false, nil)
		socketErr.EventID = eventID
		dispatchWebSocketError(connection.socket, socketErr)
		connection.sendEventError(socketErr)
		return
	}
	values, err := connection.bindPacketArguments(packet, types)
	if err != nil {
		socketErr := gox.NewGoxSocketError(gox.GoxSocketBindFailed, "WebSocket event arguments do not match the Go handler", gox.GoxSocketPhaseProtocol, event, false, err)
		socketErr.EventID = eventID
		if binding, ok := err.(*websocketmeta.BindingError); ok {
			socketErr.Code = gox.GoxSocketErrorCode(binding.Code)
			socketErr.Argument = binding.Argument
			socketErr.Received = binding.Received
			socketErr.Expected = binding.Expected
			socketErr.Message = binding.Error()
		}
		dispatchWebSocketError(connection.socket, socketErr)
		connection.sendEventError(socketErr)
		return
	}
	go func() {
		_, _, dispatchErr := connection.socket.DispatchOn(event, values...)
		sessionErr := connection.persistSessionActivity(event)
		if dispatchErr != nil {
			if sessionErr != nil {
				dispatchWebSocketError(connection.socket, *sessionErr)
			}
			socketErr := newWebSocketHandlerError(event, dispatchErr)
			socketErr.EventID = eventID
			dispatchWebSocketError(connection.socket, socketErr)
			connection.sendEventError(socketErr)
			return
		}
		if sessionErr != nil {
			sessionErr.EventID = eventID
			dispatchWebSocketError(connection.socket, *sessionErr)
			connection.sendEventError(*sessionErr)
		}
	}()
}

type webSocketEventErrorDetails struct {
	Message  string `json:"message"`
	Argument int    `json:"argument"`
	Received string `json:"received"`
	Expected string `json:"expected"`
}

func (connection *webSocketRuntimeConnection) sendEventError(socketErr gox.GoxSocketError) {
	details, err := json.Marshal(webSocketEventErrorDetails{Message: socketErr.Message, Argument: socketErr.Argument, Received: socketErr.Received, Expected: socketErr.Expected})
	if err != nil {
		return
	}
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketError,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte(socketErr.Event)},
			{Type: websocketmeta.HeaderErrorCode, Value: []byte(socketErr.Code)},
			{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(socketErr.EventID)},
		},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentJSON, Data: details}},
	})
	if err != nil {
		return
	}
	if err := connection.writeBinary(socketErr.Event, packet); err != nil {
		dispatchWebSocketError(connection.socket, err.(gox.GoxSocketError))
	}
}

func (connection *webSocketRuntimeConnection) dispatchRemoteError(packet websocketmeta.Packet) bool {
	if len(packet.Arguments) != 1 || packet.Arguments[0].Type != websocketmeta.ArgumentJSON {
		connection.failProtocol("Event ERROR payload must contain one JSON argument", nil)
		return false
	}
	var details webSocketEventErrorDetails
	if err := json.Unmarshal(packet.Arguments[0].Data, &details); err != nil {
		connection.failProtocol("Event ERROR payload is invalid", err)
		return false
	}
	if requestID, hasRequest := websocketmeta.HeaderUint(packet, websocketmeta.HeaderRequestID); hasRequest {
		connection.requestMu.Lock()
		pending, ok := connection.pendingRequests[requestID]
		if ok {
			delete(connection.pendingRequests, requestID)
		}
		route := connection.clusterRequests[requestID]
		if route != nil {
			delete(connection.clusterRequests, requestID)
			delete(connection.clusterRequestIDs, route.correlationKey)
		}
		_, ignored := connection.ignoredClusterResponses[requestID]
		if ignored {
			delete(connection.ignoredClusterResponses, requestID)
		}
		connection.requestMu.Unlock()
		if ignored {
			return true
		}
		if route != nil {
			encoded, err := websocketmeta.EncodePacket(packet)
			if err != nil {
				connection.failProtocol("Request ERROR could not be encoded", err)
				return false
			}
			connection.publishClusterResponse(route, encoded)
			return true
		}
		if !ok {
			connection.failProtocol("Request ERROR references an unknown request", nil)
			return false
		}
		socketErr := gox.NewGoxSocketError(gox.GoxSocketErrorCode(websocketmeta.HeaderText(packet, websocketmeta.HeaderErrorCode)), details.Message, gox.GoxSocketPhaseHandler, pending.event, false, nil)
		socketErr.Argument = details.Argument
		socketErr.Received = details.Received
		socketErr.Expected = details.Expected
		socketErr.Remote = true
		dispatchWebSocketError(connection.socket, socketErr)
		close(pending.done)
		return true
	}
	eventID, _ := websocketmeta.HeaderUint(packet, websocketmeta.HeaderEventID)
	socketErr := gox.NewGoxSocketError(gox.GoxSocketErrorCode(websocketmeta.HeaderText(packet, websocketmeta.HeaderErrorCode)), details.Message, gox.GoxSocketPhaseHandler, websocketmeta.EventName(packet), false, nil)
	socketErr.EventID = eventID
	socketErr.Argument = details.Argument
	socketErr.Received = details.Received
	socketErr.Expected = details.Expected
	socketErr.Remote = true
	dispatchWebSocketError(connection.socket, socketErr)
	return true
}

func (connection *webSocketRuntimeConnection) disconnectReason(err error) gox.WSDisconnectReason {
	connection.mu.Lock()
	if connection.local != nil {
		reason := *connection.local
		connection.mu.Unlock()
		return reason
	}
	connection.mu.Unlock()

	if closeErr, ok := err.(*websocket.CloseError); ok {
		code := gox.WSCloseCode(closeErr.Code)
		return gox.WSDisconnectReason{
			Code:          code,
			Reason:        closeErr.Text,
			Clean:         code != gox.WSCloseAbnormal,
			Initiator:     gox.WSDisconnectInitiatorClient,
			WillReconnect: webSocketCloseShouldReconnect(code),
		}
	}
	return gox.WSDisconnectReason{
		Code:          gox.WSCloseAbnormal,
		Clean:         false,
		Initiator:     gox.WSDisconnectInitiatorUnknown,
		WillReconnect: true,
	}
}

func validWebSocketClose(code gox.WSCloseCode) bool {
	if code >= 3000 && code <= 4999 {
		return true
	}
	switch code {
	case gox.WSCloseNormal, gox.WSCloseGoingAway, gox.WSCloseProtocol,
		gox.WSCloseUnsupported, gox.WSCloseInvalidData, gox.WSClosePolicy,
		gox.WSCloseMessageLarge, gox.WSCloseInternal, gox.WSCloseRestart,
		gox.WSCloseTryLater:
		return true
	default:
		return false
	}
}

func webSocketCloseShouldReconnect(code gox.WSCloseCode) bool {
	switch code {
	case gox.WSCloseGoingAway, gox.WSCloseAbnormal, gox.WSCloseInternal,
		gox.WSCloseRestart, gox.WSCloseTryLater, gox.WSCloseReconnect:
		return true
	default:
		return false
	}
}

func (r *Runtime) ensureWebSocketReady(ctx *gox.Context) (*GoxRouteRuntime, gox.WebSocketHandler, string, *GoxError) {
	route, ok, err := r.getOrCreateGoxRoute(webSocketModuleRoutePath, nil)
	if err != nil {
		return route, nil, "", asGoxError(err, gox.GoxErrorResolve, route, ctx, "failed to resolve WebSocket module")
	}
	if !ok || route == nil {
		err = errors.New("required file websocket/index.gox was not found")
		return route, nil, "", newGoxError(gox.GoxErrorResolve, route, ctx, "WebSocket module is missing", err)
	}
	if err := r.ensureGoxRouteReady(route); err != nil {
		return route, nil, "", asGoxError(err, gox.GoxErrorLoad, route, ctx, "failed to prepare WebSocket module")
	}

	route.mu.Lock()
	handler := route.WebSocketHandler
	buildVersion := route.BuildVersion
	route.mu.Unlock()
	if handler == nil {
		err = errors.New("expected func Index(socket *gox.Socket) gox.WebSocket")
		return route, nil, "", newGoxError(gox.GoxErrorLoad, route, ctx, "WebSocket Index handler is missing", err)
	}
	return route, handler, buildVersion, nil
}

func executeWebSocketIndex(route *GoxRouteRuntime, ctx *gox.Context, socket *gox.Socket, handler gox.WebSocketHandler) (err *GoxError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = newGoxPanicRuntimeError(route, ctx, recovered)
		}
	}()
	handler(socket)
	return nil
}

func (r *Runtime) webSocketRouteEnabled(ctx *gox.Context, req *http.Request, path string) bool {
	if r == nil || ctx == nil || req == nil || ctx.Request.URL.Path() != path {
		return false
	}
	state := r.stateFor(ctx)
	return state != nil && state.Config.WebSocket.Enabled
}

func isWebSocketRequestPath(path string) bool {
	return path == websocketmeta.ClientPath || path == websocketmeta.HandshakePath
}

func (r *Runtime) webSocketClientAssets() gox.ClientIslandRuntime {
	if r == nil {
		return nil
	}
	state := r.runtimeState.Load()
	if state == nil {
		return nil
	}
	return state.ClientAssets
}
