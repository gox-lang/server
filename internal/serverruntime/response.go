package serverruntime

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gox-lang/gox"
)

type responseRuntimeState struct {
	responseValue   gox.Response
	response        *gox.Response
	contextValue    gox.Context
	body            gox.Body
	contextState    contextRuntimeState
	writer          http.ResponseWriter
	clientAssets    *ClientAssetPolicy
	wroteHeader     bool
	bodyWritten     bool
	bodyConstructed bool
	done            bool
	upgraded        bool
	development     bool
	component       gox.Component
	location        string
}

var responseRuntimeStatePool = sync.Pool{
	New: func() any {
		state := &responseRuntimeState{body: gox.NewBody(nil, "")}
		gox.ResetRuntimeResponse(&state.responseValue)
		state.response = &state.responseValue
		return state
	},
}

func newRuntimeResponse(w http.ResponseWriter, clientAssets *ClientAssetPolicy) (*gox.Response, *responseRuntimeState) {
	state := responseRuntimeStatePool.Get().(*responseRuntimeState)
	gox.ResetRuntimeResponse(&state.responseValue)
	state.response = &state.responseValue
	gox.ResetRuntimeBody(state.body, nil, "")
	state.contextState = contextRuntimeState{}
	state.writer = w
	state.clientAssets = clientAssets
	state.wroteHeader = false
	state.bodyWritten = false
	state.bodyConstructed = false
	state.done = false
	state.upgraded = false
	state.development = false
	state.component = nil
	state.location = ""
	gox.SetRuntimeResponseState(state.response, state)
	return state.response, state
}

func unregisterRuntimeResponse(res *gox.Response) {
	if res == nil {
		return
	}
	value := gox.RuntimeResponseState(res)
	state, _ := value.(*responseRuntimeState)
	if state == nil {
		gox.SetRuntimeResponseState(res, nil)
		return
	}
	releaseRuntimeResponse(state)
}

func releaseRuntimeResponse(state *responseRuntimeState) {
	if state == nil {
		return
	}
	gox.SetRuntimeResponseState(state.response, nil)
	gox.ResetRuntimeBody(state.body, nil, "")
	gox.ResetRuntimeResponse(&state.responseValue)
	state.response = &state.responseValue
	gox.ResetRuntimeContext(&state.contextValue)
	state.contextState = contextRuntimeState{}
	state.writer = nil
	state.clientAssets = nil
	state.wroteHeader = false
	state.bodyWritten = false
	state.bodyConstructed = false
	state.done = false
	state.upgraded = false
	state.development = false
	state.component = nil
	state.location = ""
	responseRuntimeStatePool.Put(state)
}

func runtimeResponse(ctx *gox.Context) (*responseRuntimeState, bool) {
	if ctx == nil || ctx.Response == nil {
		return nil, false
	}
	value := gox.RuntimeResponseState(ctx.Response)
	if value == nil {
		return nil, false
	}
	state, ok := value.(*responseRuntimeState)
	return state, ok && state != nil
}

func (s *responseRuntimeState) SetClientAssets(clientAssets *ClientAssetPolicy) {
	if s == nil {
		return
	}
	s.clientAssets = clientAssets
}

func (s *responseRuntimeState) Status() int {
	if s == nil {
		return http.StatusOK
	}
	return s.response.Status.GetCode()
}

func (s *responseRuntimeState) Header() http.Header {
	if s == nil || s.writer == nil {
		return nil
	}
	return s.writer.Header()
}

func (s *responseRuntimeState) WriteHeader(status int) {
	if s == nil || s.writer == nil || s.wroteHeader {
		return
	}
	s.applyHeaders()
	s.wroteHeader = true
	s.writer.WriteHeader(status)
}

func (s *responseRuntimeState) Write(p []byte) (int, error) {
	if s == nil || s.writer == nil {
		return 0, http.ErrHijacked
	}
	if !s.wroteHeader {
		s.WriteHeader(s.Status())
	}
	n, err := s.writer.Write(p)
	s.MarkBodyWritten(n)
	return n, err
}

func (s *responseRuntimeState) WroteHeader() bool {
	return s != nil && s.wroteHeader
}

func (s *responseRuntimeState) BodyWritten() bool {
	return s != nil && s.bodyWritten
}

func (s *responseRuntimeState) BodyConstructed() bool {
	return s != nil && s.bodyConstructed
}

func (s *responseRuntimeState) Done() bool {
	return s != nil && s.done
}

func (s *responseRuntimeState) MarkUpgraded() {
	if s != nil {
		s.upgraded = true
	}
}

func (s *responseRuntimeState) Upgraded() bool {
	return s != nil && s.upgraded
}

func (s *responseRuntimeState) Component() gox.Component {
	if s == nil {
		return nil
	}
	return s.component
}

func (s *responseRuntimeState) Location() string {
	if s == nil {
		return ""
	}
	return s.location
}

func (s *responseRuntimeState) MarkBodyWritten(n int) {
	if s == nil || n <= 0 {
		return
	}
	s.bodyConstructed = true
	s.bodyWritten = true
}

func (s *responseRuntimeState) MarkBodyConstructed(n int) {
	if s == nil || n <= 0 {
		return
	}
	s.bodyConstructed = true
}

func (s *responseRuntimeState) ApplyFlow(flow gox.FlowControl) {
	if s == nil {
		return
	}
	s.done = true
	s.component = flow.Component
	s.location = flow.Location
}

func (s *responseRuntimeState) applyHeaders() {
	if s == nil || s.writer == nil {
		return
	}
	dst := s.writer.Header()
	s.response.Headers.Range(func(key string, value string, first bool) bool {
		if key == "Content-Type" && first {
			dst[key] = append(dst[key][:0], value)
			return true
		}
		if first {
			dst.Set(key, value)
		} else {
			dst.Add(key, value)
		}
		return true
	})
	if s.development {
		dst.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		dst.Set("Pragma", "no-cache")
		dst.Set("Expires", "0")
	}
	pendingCookies := s.response.Cookies.PendingValues()
	cookieNames := make([]string, 0, len(pendingCookies))
	for name := range pendingCookies {
		cookieNames = append(cookieNames, name)
	}
	sort.Strings(cookieNames)
	for _, name := range cookieNames {
		for _, cookie := range pendingCookies[name] {
			http.SetCookie(s.writer, toHTTPCookie(cookie))
		}
	}
}

func toHTTPCookie(cookie gox.Cookie) *http.Cookie {
	if cookie.Expired {
		cookie.Value = ""
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(0, 0)
	}

	return &http.Cookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Path:     cookie.Path,
		Domain:   cookie.Domain,
		Expires:  cookie.Expires,
		MaxAge:   cookie.MaxAge,
		Secure:   cookie.Secure,
		HttpOnly: cookie.HttpOnly,
		SameSite: toHTTPSameSite(cookie.SameSite),
	}
}

func toHTTPSameSite(value gox.SameSite) http.SameSite {
	switch value {
	case gox.SameSiteLax:
		return http.SameSiteLaxMode
	case gox.SameSiteStrict:
		return http.SameSiteStrictMode
	case gox.SameSiteNone:
		return http.SameSiteNoneMode
	default:
		return http.SameSiteDefaultMode
	}
}

func writeResponseHeader(ctx *gox.Context, status int) {
	state, ok := runtimeResponse(ctx)
	if !ok || state == nil {
		return
	}
	state.WriteHeader(status)
}
