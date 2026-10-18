package serverruntime

import (
	"sync/atomic"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox/server"
)

type RuntimeState struct {
	Config       server.Config
	Methods      *MethodPolicy
	IPResolver   *IPResolverPolicy
	Session      *SessionPolicy
	RateLimit    *RateLimitPolicy
	Static       *StaticPolicy
	PreRouter    *PreRouter
	ClientAssets *ClientAssetPolicy
}

type runtimeStateStore struct {
	current atomic.Pointer[RuntimeState]
}

func NewRuntimeState(config server.Config, projectRoot string, result *goxcompiler.CompileResult) (*RuntimeState, error) {
	tlsConfig, tlsErr := normalizeTLSConfig(config.HTTP)
	if tlsErr != nil {
		return nil, tlsErr
	}
	config.HTTP.TLS = tlsConfig
	methods := NewMethodPolicy(config.HTTP.MethodsAllowed)

	ipResolver, err := NewIPResolverPolicy(config.HTTP.ResolverIPForwarded)
	if err != nil {
		return nil, newRuntimeConfigError("compile-resolver-ip-forwarded-failed", "compile resolverIPForwarded failed", err)
	}

	session, err := NewSessionPolicy(config.ProjectName, config.Session)
	if err != nil {
		return nil, newRuntimeConfigError("compile-session-failed", "compile session failed", err)
	}

	rateLimit, err := NewRateLimitPolicy(config.RateLimit)
	if err != nil {
		return nil, newRuntimeConfigError("compile-ratelimit-failed", "compile rateLimit failed", err)
	}

	static, err := NewStaticPolicy(projectRoot, config.Static)
	if err != nil {
		return nil, newRuntimeConfigError("compile-static-failed", "compile static failed", err)
	}

	preRouter, err := NewPreRouter(config.Routes)
	if err != nil {
		return nil, newRuntimeConfigError("compile-preroute-failed", "compile preRoute failed", err)
	}

	clientAssets := NewClientAssetPolicy(result)
	precompressClientAssets(projectRoot, config.Static.Compression, clientAssets)

	state := &RuntimeState{
		Config:       config,
		Methods:      methods,
		IPResolver:   ipResolver,
		Session:      session,
		RateLimit:    rateLimit,
		Static:       static,
		PreRouter:    preRouter,
		ClientAssets: clientAssets,
	}

	return state, nil
}

func (s *runtimeStateStore) Store(state *RuntimeState) {
	s.current.Store(state)
}

func (s *runtimeStateStore) Load() *RuntimeState {
	return s.current.Load()
}

func (r *Runtime) state() *RuntimeState {
	state := r.runtimeState.Load()
	if state == nil {
		logs.Warm("runtime state is not initialized")
		return &RuntimeState{
			Config:     r.Config,
			Methods:    NewMethodPolicy(nil),
			IPResolver: NewIPResolverPolicyUnsafe(nil),
			Session:    nil,
			RateLimit:  nil,
			Static:     nil,
			PreRouter:  &PreRouter{},
		}
	}

	return state
}
