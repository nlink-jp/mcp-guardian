package config

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{"defaults are valid", func(c *Config) { c.Upstream = "cmd" }, false},
		{"empty upstream", func(c *Config) {}, true},
		{"bad enforcement", func(c *Config) { c.Upstream = "cmd"; c.Enforcement = "invalid" }, true},
		{"bad schema mode", func(c *Config) { c.Upstream = "cmd"; c.SchemaMode = "invalid" }, true},
		{"negative max-calls", func(c *Config) { c.Upstream = "cmd"; c.MaxCalls = -1 }, true},
		{"zero timeout", func(c *Config) { c.Upstream = "cmd"; c.TimeoutMs = 0 }, true},
		{"negative timeout", func(c *Config) { c.Upstream = "cmd"; c.TimeoutMs = -100 }, true},
		{"advisory enforcement", func(c *Config) { c.Upstream = "cmd"; c.Enforcement = "advisory" }, false},
		{"schema off", func(c *Config) { c.Upstream = "cmd"; c.SchemaMode = "off" }, false},
		{"schema strict", func(c *Config) { c.Upstream = "cmd"; c.SchemaMode = "strict" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
