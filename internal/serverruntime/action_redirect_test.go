package serverruntime

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/gox-lang/gox"
)

func TestActionRedirectNegotiation(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, mode := range []string{"page", "legacy-action", "navigation-action", "header-only"} {
			t.Run(strconv.Itoa(status)+"/"+mode, func(t *testing.T) {
				writer, recorder := newTestGoxWriter(t, noCompressionConfig(), "", true)
				headers := map[string][]string{}
				if mode == "legacy-action" || mode == "navigation-action" {
					headers["Invoke-Action"] = []string{"SignOut"}
				}
				if mode == "navigation-action" || mode == "header-only" {
					headers["Gox-Action-Redirect"] = []string{"1"}
				}
				ctx := &gox.Context{
					Request:  gox.NewRequest(gox.RequestInput{Headers: gox.NewHeaders(headers)}),
					Response: writer.res.response,
					Writer:   writer,
				}
				ctx.Response.Headers.Set("Set-Cookie", "session=; Max-Age=0; Path=/; HttpOnly")
				_, _ = writer.Write([]byte("discard this body"))
				if err := executeResponseFlow(ctx, nil, gox.FlowControl{Kind: gox.FlowRedirect, StatusCode: status, Location: "/sign?next=%2Fdashboard"}, nil); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				want := status
				if mode == "navigation-action" {
					want = http.StatusOK
					if recorder.Header().Get("Action-Response") != "redirect" || recorder.Header().Get("Action-Redirect-Status") != strconv.Itoa(status) {
						t.Fatal("missing redirect envelope")
					}
					if recorder.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("cacheable action redirect")
					}
				} else if recorder.Header().Get("Action-Response") == "redirect" {
					t.Fatal("legacy redirect changed")
				}
				if recorder.Code != want || recorder.Header().Get("Location") != "/sign?next=%2Fdashboard" {
					t.Fatalf("response: %d %v", recorder.Code, recorder.Header())
				}
				if recorder.Body.Len() != 0 || recorder.Header().Get("Set-Cookie") == "" {
					t.Fatal("body leaked or session cookie lost")
				}
			})
		}
	}
}
