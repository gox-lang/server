package gox

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/gox-lang/server/internal/goxsource"
)

// WebSocketContractScope identifies the client API governed by a server-side
// WebSocket declaration.
type WebSocketContractScope string

const (
	WebSocketContractSocket  WebSocketContractScope = "socket"
	WebSocketContractChannel WebSocketContractScope = "channel"
)

// WebSocketContractDirection is expressed from the browser's point of view.
// ClientEmit is declared by GOX On; ClientOn is declared by GOX Emit.
type WebSocketContractDirection string

const (
	WebSocketContractClientEmit WebSocketContractDirection = "client-emit"
	WebSocketContractClientOn   WebSocketContractDirection = "client-on"
)

// WebSocketContractArgument keeps the Go syntax that defines one client-side
// argument. On contracts carry a Type; Emit contracts carry an Expr. TypeScript
// inference is deliberately a later step.
type WebSocketContractArgument struct {
	Name string
	Type ast.Expr
	Expr ast.Expr
}

// WebSocketContract is the server-side source of truth for one dynamic client
// WebSocket event. ReplyTarget is populated for Emit(...).Await(&target); the
// target's concrete Go type is resolved by the type-inference phase.
type WebSocketContract struct {
	Scope        WebSocketContractScope
	Direction    WebSocketContractDirection
	Channel      string
	Event        string
	Arguments    []WebSocketContractArgument
	HasReply     bool
	ReplyTarget  ast.Expr
	ReplyHandler ast.Expr
	Source       token.Pos
}

func collectWebSocketContracts(mod *Module) []WebSocketContract {
	if mod == nil || mod.File == nil {
		return nil
	}
	entrypoints := make(map[string]FuncKind)
	for _, fn := range mod.Funcs {
		if fn.Kind == FuncWebSocket || fn.Kind == FuncChannel {
			entrypoints[fn.Name] = fn.Kind
		}
	}
	var contracts []WebSocketContract
	for _, decl := range mod.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		kind, declaredEntrypoint := entrypoints[fn.Name.Name]
		eventEntrypoint := declaredEntrypoint && websocketContractEntrypointAllowed(mod, kind, fn.Name.Name)
		scope := WebSocketContractSocket
		channel := ""
		if eventEntrypoint && kind == FuncChannel {
			scope = WebSocketContractChannel
			channel = mod.Structure.ChannelName
		}
		serverSockets := serverSocketIdentifiers(fn.Body)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if eventEntrypoint {
				if contract, ok := websocketOnContract(mod.File, call, scope, channel); ok {
					contracts = append(contracts, contract)
					return true
				}
			}
			if contract, ok := websocketEmitAwaitContract(call, scope, channel); ok && (eventEntrypoint || websocketCallUsesServerSocket(call, serverSockets)) {
				contracts = append(contracts, contract)
				return false // do not also record the nested Emit as fire-and-forget
			}
			if contract, ok := websocketEmitContract(call, scope, channel); ok && (eventEntrypoint || websocketCallUsesServerSocket(call, serverSockets) || isGlobalSocketsEmit(call)) {
				contracts = append(contracts, contract)
			}
			return true
		})
	}
	return contracts
}

func websocketContractEntrypointAllowed(mod *Module, kind FuncKind, functionName string) bool {
	if mod == nil {
		return false
	}
	switch kind {
	case FuncWebSocket:
		return mod.Structure.Kind == goxsource.ModuleGlobalWebSocket
	case FuncChannel:
		return mod.Structure.Kind == goxsource.ModulePublicChannel &&
			mod.Structure.ChannelName != "" &&
			mod.Structure.ChannelFunction != "" &&
			functionName == mod.Structure.ChannelFunction
	default:
		return false
	}
}

func websocketOnContract(file *ast.File, call *ast.CallExpr, scope WebSocketContractScope, channel string) (WebSocketContract, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "On" || len(call.Args) != 2 {
		return WebSocketContract{}, false
	}
	event, ok := websocketEventLiteral(call.Args[0])
	if !ok {
		return WebSocketContract{}, false
	}
	fnType := websocketHandlerType(file, call.Args[1])
	if fnType == nil {
		return WebSocketContract{}, false
	}
	hasReply, err := validateWebSocketOnResults(fnType.Results)
	if err != nil {
		return WebSocketContract{}, false
	}
	args := websocketOnArguments(fnType.Params)
	if scope == WebSocketContractChannel && len(args) > 0 && isGoxSocketPointer(args[0].Type) {
		args = args[1:]
	}
	return WebSocketContract{Scope: scope, Direction: WebSocketContractClientEmit, Channel: channel, Event: event, Arguments: args, HasReply: hasReply, ReplyHandler: call.Args[1], Source: call.Pos()}, true
}

func websocketOnArguments(params *ast.FieldList) []WebSocketContractArgument {
	if params == nil {
		return nil
	}
	var args []WebSocketContractArgument
	for _, field := range params.List {
		if len(field.Names) == 0 {
			args = append(args, WebSocketContractArgument{Type: field.Type})
			continue
		}
		for _, name := range field.Names {
			args = append(args, WebSocketContractArgument{Name: name.Name, Type: field.Type})
		}
	}
	return args
}

func websocketEmitAwaitContract(call *ast.CallExpr, scope WebSocketContractScope, channel string) (WebSocketContract, bool) {
	if scope != WebSocketContractSocket || len(call.Args) != 1 {
		return WebSocketContract{}, false
	}
	awaitSelector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || awaitSelector.Sel.Name != "Await" {
		return WebSocketContract{}, false
	}
	emitCall, ok := awaitSelector.X.(*ast.CallExpr)
	if !ok {
		return WebSocketContract{}, false
	}
	contract, ok := websocketEmitContract(emitCall, scope, channel)
	if !ok {
		return WebSocketContract{}, false
	}
	contract.HasReply = true
	contract.ReplyTarget = call.Args[0]
	contract.Source = call.Pos()
	return contract, true
}

func websocketEmitContract(call *ast.CallExpr, scope WebSocketContractScope, channel string) (WebSocketContract, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Emit" || len(call.Args) == 0 {
		return WebSocketContract{}, false
	}
	event, ok := websocketEventLiteral(call.Args[0])
	if !ok {
		return WebSocketContract{}, false
	}
	args := make([]WebSocketContractArgument, 0, len(call.Args)-1)
	for _, expr := range call.Args[1:] {
		args = append(args, WebSocketContractArgument{Expr: expr})
	}
	return WebSocketContract{Scope: scope, Direction: WebSocketContractClientOn, Channel: channel, Event: event, Arguments: args, Source: call.Pos()}, true
}

func websocketEventLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func isGoxSocketPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := star.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Socket" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "gox"
}

func websocketCallUsesServerSocket(call *ast.CallExpr, identifiers map[string]struct{}) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok && selector.Sel.Name == "Await" {
		emitCall, ok := selector.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		selector, ok = emitCall.Fun.(*ast.SelectorExpr)
	}
	if !ok {
		return false
	}
	return isServerSocketAwaitReceiver(selector.X, identifiers)
}

func isGlobalSocketsEmit(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Emit" {
		return false
	}
	sockets, ok := selector.X.(*ast.SelectorExpr)
	if !ok || sockets.Sel.Name != "Sockets" {
		return false
	}
	pkg, ok := sockets.X.(*ast.Ident)
	return ok && pkg.Name == "gox"
}
