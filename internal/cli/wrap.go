package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nlink-jp/mcp-guardian/internal/state"
)

// WrapOptions holds options for the Wrap command.
type WrapOptions struct {
	ServerName   string
	StateDir     string
	Enforcement  string
	MCPConfigPath string // path to .mcp.json
	MaskPatterns []string
	MaskFile     string
	GlobalConfig string // path to global config file
	ProfileName  string // server profile name or path
}

// Wrap modifies .mcp.json to interpose the proxy on a server.
func Wrap(serverName, stateDir, enforcement, mcpConfigPath string) error {
	return WrapWithOptions(WrapOptions{
		ServerName:    serverName,
		StateDir:      stateDir,
		Enforcement:   enforcement,
		MCPConfigPath: mcpConfigPath,
	})
}

// WrapWithOptions modifies .mcp.json with full option support.
func WrapWithOptions(opts WrapOptions) error {
	configPath := opts.MCPConfigPath
	if configPath == "" {
		configPath = findMCPConfig()
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", configPath, err)
	}

	var mcpConfig map[string]interface{}
	if err := json.Unmarshal(data, &mcpConfig); err != nil {
		return fmt.Errorf("invalid JSON in %s: %w", configPath, err)
	}

	servers, ok := mcpConfig["mcpServers"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no mcpServers found in %s", configPath)
	}

	serverName := opts.ServerName
	server, ok := servers[serverName].(map[string]interface{})
	if !ok {
		return fmt.Errorf("server '%s' not found", serverName)
	}

	// Check if already wrapped
	if _, wrapped := server["_unwrap"]; wrapped {
		return fmt.Errorf("server '%s' is already wrapped", serverName)
	}

	// Save original command/args for unwrap
	server["_unwrap"] = map[string]interface{}{
		"command": server["command"],
		"args":    server["args"],
	}

	// Find our binary path
	self, err := os.Executable()
	if err != nil {
		self = "mcp-guardian"
	}

	// Build new args
	origCommand, _ := server["command"].(string)
	origArgs, _ := toStringSlice(server["args"])

	var newArgs []string

	if opts.ProfileName != "" {
		// Profile mode: profile contains all server settings
		newArgs = append(newArgs, "--profile", opts.ProfileName)
		if opts.GlobalConfig != "" {
			newArgs = append(newArgs, "--config", opts.GlobalConfig)
		}
	} else {
		// Inline mode: individual flags
		newArgs = append(newArgs, "--enforcement", opts.Enforcement)
		newArgs = append(newArgs, "--state-dir", opts.StateDir+"-"+serverName)
		for _, pattern := range opts.MaskPatterns {
			newArgs = append(newArgs, "--mask", pattern)
		}
		if opts.MaskFile != "" {
			newArgs = append(newArgs, "--mask-file", opts.MaskFile)
		}
		if opts.GlobalConfig != "" {
			newArgs = append(newArgs, "--config", opts.GlobalConfig)
		}
	}

	newArgs = append(newArgs, "--", origCommand)
	newArgs = append(newArgs, origArgs...)

	server["command"] = self
	server["args"] = newArgs

	out, _ := json.MarshalIndent(mcpConfig, "", "  ")
	if err := state.AtomicWrite(configPath, out, 0644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}

	fmt.Printf("Wrapped server '%s' with mcp-guardian\n", serverName)
	return nil
}

// Unwrap restores .mcp.json to the original server configuration.
func Unwrap(serverName, configPath string) error {
	if configPath == "" {
		configPath = findMCPConfig()
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", configPath, err)
	}

	var mcpConfig map[string]interface{}
	if err := json.Unmarshal(data, &mcpConfig); err != nil {
		return fmt.Errorf("invalid JSON in %s: %w", configPath, err)
	}

	servers, ok := mcpConfig["mcpServers"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no mcpServers found in %s", configPath)
	}

	server, ok := servers[serverName].(map[string]interface{})
	if !ok {
		return fmt.Errorf("server '%s' not found", serverName)
	}

	unwrap, ok := server["_unwrap"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("server '%s' is not wrapped", serverName)
	}

	server["command"] = unwrap["command"]
	server["args"] = unwrap["args"]
	delete(server, "_unwrap")

	out, _ := json.MarshalIndent(mcpConfig, "", "  ")
	if err := state.AtomicWrite(configPath, out, 0644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}

	fmt.Printf("Unwrapped server '%s'\n", serverName)
	return nil
}

func findMCPConfig() string {
	if _, err := os.Stat(".mcp.json"); err == nil {
		return ".mcp.json"
	}
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".claude", "mcp.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ".mcp.json"
}

func toStringSlice(v interface{}) ([]string, bool) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	result := make([]string, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		result[i] = s
	}
	return result, true
}
