package toolchain

import (
	"reflect"
	"runtime/debug"
	"testing"
)

func TestPluginBuildArgsMatchHostTrimpath(t *testing.T) {
	trimmed := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "-trimpath", Value: "true"}}}
	for _, tc := range []struct {
		name string
		args []string
		info *debug.BuildInfo
		want []string
	}{
		{"portable plugin", []string{"build", "-buildmode=plugin", "page.go"}, trimmed, []string{"build", "-trimpath", "-buildmode=plugin", "page.go"}},
		{"development host", []string{"build", "-buildmode=plugin", "page.go"}, &debug.BuildInfo{}, []string{"build", "-buildmode=plugin", "page.go"}},
		{"unknown host", []string{"build", "-buildmode=plugin", "page.go"}, nil, []string{"build", "-buildmode=plugin", "page.go"}},
		{"existing flag", []string{"build", "-trimpath", "-buildmode=plugin", "page.go"}, trimmed, []string{"build", "-trimpath", "-buildmode=plugin", "page.go"}},
		{"ordinary command", []string{"env", "GOMOD"}, trimmed, []string{"env", "GOMOD"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string{}, tc.args...)
			got := pluginBuildArgs(tc.args, tc.info)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if !reflect.DeepEqual(tc.args, original) {
				t.Fatal("mutated input arguments")
			}
		})
	}
}
