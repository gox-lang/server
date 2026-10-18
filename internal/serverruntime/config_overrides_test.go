package serverruntime

import (
	"testing"

	"github.com/gox-lang/gox/server"
)

func TestExecutionConfigOverridesOnlySelectedHTTPValues(t *testing.T) {
	config := server.Config{HTTP: server.HTTPConfig{Host: "configured.local", Port: 9000}}
	ConfigOverrides{HTTPPort: 3001, HTTPPortSet: true}.apply(&config)
	if config.HTTP.Host != "configured.local" || config.HTTP.Port != 3001 {
		t.Fatalf("HTTP config = %#v", config.HTTP)
	}
}

func TestExecutionConfigOverridesDevelopmentMode(t *testing.T) {
	config := server.Config{}
	ConfigOverrides{DevEnabled: true, DevEnabledSet: true}.apply(&config)
	if !config.Dev.Enabled {
		t.Fatal("development override did not enable development mode")
	}
}

func TestEmptyExecutionConfigOverridesPreserveConfiguration(t *testing.T) {
	config := server.Config{Dev: server.DevConfig{Enabled: true}, HTTP: server.HTTPConfig{Host: "configured.local", Port: 9000}}
	ConfigOverrides{}.apply(&config)
	if !config.Dev.Enabled || config.HTTP.Host != "configured.local" || config.HTTP.Port != 9000 {
		t.Fatalf("config = %#v", config)
	}
}
