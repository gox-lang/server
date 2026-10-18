package gox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type webSocketEventTypeDecl struct {
	Source     string                          `json:"source"`
	Scope      WebSocketContractScope          `json:"scope"`
	Direction  WebSocketContractDirection      `json:"direction"`
	Channel    string                          `json:"channel,omitempty"`
	Event      string                          `json:"event"`
	Arguments  []webSocketContractTypeArgument `json:"arguments,omitempty"`
	HasReply   bool                            `json:"hasReply,omitempty"`
	ReplyTypes []string                        `json:"replyTypes,omitempty"`
}

type webSocketEventTypeRegistry struct {
	Sources map[string][]webSocketEventTypeDecl `json:"sources"`
}

type webSocketEventBucketKey struct {
	scope     WebSocketContractScope
	channel   string
	direction WebSocketContractDirection
}

var webSocketEventTypeProjectLocks sync.Map

func webSocketEventTypeProjectLock(root string) *sync.Mutex {
	key, err := filepath.Abs(root)
	if err != nil {
		key = filepath.Clean(root)
	}
	lock, _ := webSocketEventTypeProjectLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// UpdateWebSocketEventTypesForModule updates the generated WebSocket event typings
// for one GOX source. It is used by development tooling such as the LSP so an
// unsaved in-memory document can remain the source of truth.
func UpdateWebSocketEventTypesForModule(root string, mod *Module) error {
	if mod == nil || strings.TrimSpace(root) == "" {
		return nil
	}
	return mutateWebSocketEventTypes(root, func(registry *webSocketEventTypeRegistry) {
		source := actionTypeSource(root, mod.Path)
		registry.Sources[source] = collectModuleWebSocketEventTypes(mod, source)
	})
}

// RemoveWebSocketEventTypesForSource removes a GOX source from the generated
// WebSocket event typings after the source is deleted or renamed.
func RemoveWebSocketEventTypesForSource(root, sourcePath string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(sourcePath) == "" {
		return nil
	}
	return mutateWebSocketEventTypes(root, func(registry *webSocketEventTypeRegistry) {
		delete(registry.Sources, actionTypeSource(root, sourcePath))
	})
}

// PruneWebSocketEventTypesSources removes cached sources that are not part of
// the current project index. This makes a fresh LSP workspace index authoritative
// even when .build/cache was produced by an earlier editor session.
func PruneWebSocketEventTypesSources(root string, sourcePaths []string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	keep := make(map[string]struct{}, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		if strings.TrimSpace(sourcePath) != "" {
			keep[actionTypeSource(root, sourcePath)] = struct{}{}
		}
	}
	return mutateWebSocketEventTypes(root, func(registry *webSocketEventTypeRegistry) {
		for source := range registry.Sources {
			if _, ok := keep[source]; !ok {
				delete(registry.Sources, source)
			}
		}
	})
}

func mutateWebSocketEventTypes(root string, mutate func(*webSocketEventTypeRegistry)) error {
	lock := webSocketEventTypeProjectLock(root)
	lock.Lock()
	defer lock.Unlock()

	buildDir := filepath.Join(root, ".build")
	cacheDir := filepath.Join(buildDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	registryPath := filepath.Join(cacheDir, "websocket-event-types.json")
	registry := webSocketEventTypeRegistry{Sources: map[string][]webSocketEventTypeDecl{}}
	if data, err := os.ReadFile(registryPath); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &registry)
	}
	if registry.Sources == nil {
		registry.Sources = map[string][]webSocketEventTypeDecl{}
	}
	mutate(&registry)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeAtomicIfChanged(registryPath, data); err != nil {
		return err
	}
	return writeAtomicIfChanged(filepath.Join(buildDir, "websocket-events.d.ts"), renderWebSocketEventTypes(registry))
}

func updateWebSocketEventTypes(graph *ModuleGraph) error {
	if graph == nil || strings.TrimSpace(graph.RootDir) == "" {
		return nil
	}
	lock := webSocketEventTypeProjectLock(graph.RootDir)
	lock.Lock()
	defer lock.Unlock()

	buildDir := filepath.Join(graph.RootDir, ".build")
	cacheDir := filepath.Join(buildDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	registryPath := filepath.Join(cacheDir, "websocket-event-types.json")
	registry := webSocketEventTypeRegistry{Sources: map[string][]webSocketEventTypeDecl{}}
	if data, err := os.ReadFile(registryPath); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &registry)
	}
	if registry.Sources == nil {
		registry.Sources = map[string][]webSocketEventTypeDecl{}
	}
	for source := range registry.Sources {
		if _, err := os.Stat(filepath.Join(graph.RootDir, filepath.FromSlash(source))); os.IsNotExist(err) {
			delete(registry.Sources, source)
		}
	}
	for _, mod := range graph.Modules {
		source := actionTypeSource(graph.RootDir, mod.Path)
		registry.Sources[source] = collectModuleWebSocketEventTypes(mod, source)
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeAtomicIfChanged(registryPath, data); err != nil {
		return err
	}
	return writeAtomicIfChanged(filepath.Join(buildDir, "websocket-events.d.ts"), renderWebSocketEventTypes(registry))
}

func collectModuleWebSocketEventTypes(mod *Module, source string) []webSocketEventTypeDecl {
	typed := collectModuleWebSocketContractTypes(mod)
	out := make([]webSocketEventTypeDecl, 0, len(typed))
	for _, contract := range typed {
		out = append(out, webSocketEventTypeDecl{
			Source: source, Scope: contract.Scope, Direction: contract.Direction,
			Channel: contract.Channel, Event: contract.Event, Arguments: contract.Arguments,
			HasReply: contract.HasReply, ReplyTypes: append([]string(nil), contract.ReplyTypes...),
		})
	}
	return out
}

func renderWebSocketEventTypes(reg webSocketEventTypeRegistry) []byte {
	all := make([]webSocketEventTypeDecl, 0)
	for _, decls := range reg.Sources {
		all = append(all, decls...)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		if a.Direction != b.Direction {
			return a.Direction < b.Direction
		}
		if a.Event != b.Event {
			return a.Event < b.Event
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return webSocketEventDeclSignature(a) < webSocketEventDeclSignature(b)
	})

	buckets := map[webSocketEventBucketKey]map[string][]webSocketEventTypeDecl{}
	for _, d := range all {
		key := webSocketEventBucketKey{d.Scope, d.Channel, d.Direction}
		if buckets[key] == nil {
			buckets[key] = map[string][]webSocketEventTypeDecl{}
		}
		buckets[key][d.Event] = append(buckets[key][d.Event], d)
	}

	var b strings.Builder
	b.WriteString("// Code generated by GOX. DO NOT EDIT.\n\ndeclare module \"@gox/websocket-events\" {\n")
	b.WriteString("  export interface WebSocketEventContract<Args extends readonly unknown[] = readonly unknown[], Reply = never> {\n    readonly args: Args\n    readonly reply: Reply\n  }\n\n")
	renderWebSocketEventInterface(&b, "SocketClientEmitEvents", buckets[webSocketEventBucketKey{WebSocketContractSocket, "", WebSocketContractClientEmit}])
	b.WriteString("\n")
	renderWebSocketEventInterface(&b, "SocketClientOnEvents", buckets[webSocketEventBucketKey{WebSocketContractSocket, "", WebSocketContractClientOn}])
	b.WriteString("\n  export interface ChannelClientEmitEvents {\n")
	renderChannelEventGroups(&b, buckets, WebSocketContractClientEmit)
	b.WriteString("  }\n\n  export interface ChannelClientOnEvents {\n")
	renderChannelEventGroups(&b, buckets, WebSocketContractClientOn)
	b.WriteString("  }\n}\n")
	return []byte(b.String())
}

func renderWebSocketEventInterface(b *strings.Builder, name string, events map[string][]webSocketEventTypeDecl) {
	fmt.Fprintf(b, "  export interface %s {\n", name)
	renderWebSocketEventProperties(b, events, "    ")
	b.WriteString("  }\n")
}

func renderChannelEventGroups(b *strings.Builder, buckets map[webSocketEventBucketKey]map[string][]webSocketEventTypeDecl, direction WebSocketContractDirection) {
	channels := make([]string, 0)
	for key := range buckets {
		if key.scope == WebSocketContractChannel && key.direction == direction && key.channel != "" {
			channels = append(channels, key.channel)
		}
	}
	sort.Strings(channels)
	for _, channel := range channels {
		fmt.Fprintf(b, "    %s: {\n", tsPropertyName(channel))
		renderWebSocketEventProperties(b, buckets[webSocketEventBucketKey{WebSocketContractChannel, channel, direction}], "      ")
		b.WriteString("    }\n")
	}
}

func renderWebSocketEventProperties(b *strings.Builder, events map[string][]webSocketEventTypeDecl, indent string) {
	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, event := range names {
		decls := events[event]
		seen := map[string]bool{}
		contracts := make([]string, 0, len(decls))
		for _, d := range decls {
			contract := webSocketEventDeclTSType(d)
			if !seen[contract] {
				seen[contract] = true
				contracts = append(contracts, contract)
			}
		}
		sort.Strings(contracts)
		fmt.Fprintf(b, "%s%s: %s\n", indent, strconv.Quote(event), strings.Join(contracts, " | "))
	}
}

func webSocketEventDeclTSType(d webSocketEventTypeDecl) string {
	args := make([]string, len(d.Arguments))
	for i, arg := range d.Arguments {
		name := arg.Name
		if name == "" {
			name = fmt.Sprintf("arg%d", i+1)
		}
		args[i] = name + ": " + arg.Type
	}
	reply := "never"
	if d.HasReply {
		reply = strings.Join(uniqueSortedTypes(d.ReplyTypes), " | ")
		if reply == "" {
			reply = "unknown"
		}
	}
	return "WebSocketEventContract<readonly [" + strings.Join(args, ", ") + "], " + reply + ">"
}

func webSocketEventDeclSignature(d webSocketEventTypeDecl) string { return webSocketEventDeclTSType(d) }
