package logs

import (
	"testing"
	"time"
)

func TestRequestHeaderOrdersStatusDurationAndRouteMethod(t *testing.T) {
	elapsed := 154708 * time.Nanosecond
	header := requestHeader(200, elapsed, "Greeting", fgcolor.green)
	if len(header) != 3 || header[0].value != 200 || header[1].value != elapsed || header[2].value != "Greeting" {
		t.Fatalf("request header = %#v", header)
	}
}
