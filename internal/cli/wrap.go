package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nlink-jp/mcp-guardian/internal/state"
)

// Wrap modifies .mcp.json to interpose the proxy on a server.
func Wrap(serverName, stateDir, enforcement, configPath string) {
	if configPath == "" {
		configPath = findMCPConfig()
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read %s: %v\n", configPath, err)
		os.Exit(1)
	}

	var mcpConfig map[string]interface{}
	if err := json.Unmarshal(data, &mcpConfig); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid JSON in %s: %v\n", configPath, err)
		os.Exit(1)
	}

	servers, ok := mcpConfig["mcpServers"].(map[string]interface{})
	if !ok {
		fmt.Fprintf(os.Stderr, "error: no mcpServers found in %s\n", configPath)
		os.Exit(1)
	}

	server, ok := servers[serverName].(map[string]interface{})
	if !ok {
		fmt.Fprintf(os.Stderr, "error: server '%s' not found\n", serverName)
		os.Exit(1)
	}

	// Check if already wrapped
	if _, wrapped := server["_unwrap"]; wrapped {
		fmt.Fprintf(os.Stderr, "error: server '%s' is already wrapped\n", serverName)
		os.Exit(1)
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

	newArgs := []string{
		"--enforcement", enforcement,
		"--state-dir", stateDir + "-" + serverName,
		"--", origCommand,
	}
	newArgs = append(newArgs, origArgs...)

	server["command"] = self
	server["args"] = newArgs

	out, _ := json.MarshalIndent(mcpConfig, "", "  ")
	if err := state.AtomicWrite(configPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write %s: %v\n", configPath, err)
		os.Exit(1)
	}

	fmt.Printf("Wrapped server '%s' with mcp-guardian\n", serverName)
}

// Unwrap restores .mcp.json to the original server configuration.
func Unwrap(serverName, configPath string) {
	if configPath == "" {
		configPath = findMCPConfig()
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read %s: %v\n", configPath, err)
		os.Exit(1)
	}

	var mcpConfig map[string]interface{}
	if err := json.Unmarshal(data, &mcpConfig); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid JSON in %s: %v\n", configPath, err)
		os.Exit(1)
	}

	servers, ok := mcpConfig["mcpServers"].(map[string]interface{})
	if !ok {
		fmt.Fprintf(os.Stderr, "error: no mcpServers found in %s\n", configPath)
		os.Exit(1)
	}

	server, ok := servers[serverName].(map[string]interface{})
	if !ok {
		fmt.Fprintf(os.Stderr, "error: server '%s' not found\n", serverName)
		os.Exit(1)
	}

	unwrap, ok := server["_unwrap"].(map[string]interface{})
	if !ok {
		fmt.Fprintf(os.Stderr, "error: server '%s' is not wrapped\n", serverName)
		os.Exit(1)
	}

	server["command"] = unwrap["command"]
	server["args"] = unwrap["args"]
	delete(server, "_unwrap")

	out, _ := json.MarshalIndent(mcpConfig, "", "  ")
	if err := state.AtomicWrite(configPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write %s: %v\n", configPath, err)
		os.Exit(1)
	}

	fmt.Printf("Unwrapped server '%s'\n", serverName)
}

func findMCPConfig() string {
	// Check .mcp.json in current directory first
	if _, err := os.Stat(".mcp.json"); err == nil {
		return ".mcp.json"
	}
	// Check ~/.claude/mcp.json
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
