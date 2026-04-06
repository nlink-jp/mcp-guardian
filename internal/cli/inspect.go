package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/jsonrpc"
	"github.com/nlink-jp/mcp-guardian/internal/proxy"
)

// Inspect connects to the MCP server defined in the profile, retrieves
// server info and available tools, and prints them to stdout.
func Inspect(profileNameOrPath, globalConfigPath string) error {
	profile, err := config.ResolveProfile(profileNameOrPath)
	if err != nil {
		return fmt.Errorf("load profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("validate profile: %w", err)
	}

	cfg := config.Defaults()

	// Apply global config
	if globalConfigPath != "" {
		gc, err := config.LoadGlobal(globalConfigPath)
		if err != nil {
			return err
		}
		gc.ApplyTo(cfg)
	} else {
		gc, _ := config.LoadGlobalAuto()
		if gc != nil {
			gc.ApplyTo(cfg)
		}
	}

	profile.ApplyTo(cfg)

	if cfg.StateDir == "" {
		cfg.StateDir = config.DefaultStateDir(profile.Name)
	}

	// Create upstream transport
	up, err := proxy.CreateUpstreamTransport(cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer up.Close()

	// Send initialize
	initReq := jsonrpc.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"inspect-init"`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"mcp-guardian-inspect","version":"1.0"}}`),
	}
	initData, _ := jsonrpc.Marshal(&initReq)
	if err := up.Send(initData); err != nil {
		return fmt.Errorf("send initialize: %w", err)
	}

	initRespData, ok := up.ReadLine()
	if !ok {
		return fmt.Errorf("no response to initialize")
	}

	initResp, err := jsonrpc.Parse(initRespData)
	if err != nil {
		return fmt.Errorf("parse initialize response: %w", err)
	}

	// Print server info
	if initResp.Error != nil {
		return fmt.Errorf("server error: %s", initResp.Error.Message)
	}

	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if initResp.Result != nil {
		json.Unmarshal(initResp.Result, &initResult)
	}

	fmt.Printf("Server: %s %s\n", initResult.ServerInfo.Name, initResult.ServerInfo.Version)
	fmt.Printf("Protocol: %s\n", initResult.ProtocolVersion)

	// Send tools/list
	listReq := jsonrpc.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"inspect-tools"`),
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	}
	listData, _ := jsonrpc.Marshal(&listReq)
	if err := up.Send(listData); err != nil {
		return fmt.Errorf("send tools/list: %w", err)
	}

	// Read response (may need to skip notifications)
	var listResp *jsonrpc.Message
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-deadline:
			return fmt.Errorf("timeout waiting for tools/list response")
		default:
		}
		data, ok := up.ReadLine()
		if !ok {
			return fmt.Errorf("connection closed before tools/list response")
		}
		msg, err := jsonrpc.Parse(data)
		if err != nil {
			continue
		}
		if msg.IsResponse() && msg.IDString() == `"inspect-tools"` {
			listResp = msg
			break
		}
	}

	if listResp.Error != nil {
		return fmt.Errorf("tools/list error: %s", listResp.Error.Message)
	}

	var toolsResult jsonrpc.ToolsListResult
	if listResp.Result != nil {
		json.Unmarshal(listResp.Result, &toolsResult)
	}

	fmt.Printf("Tools: %d\n\n", len(toolsResult.Tools))

	for i, tool := range toolsResult.Tools {
		fmt.Printf("  %d. %s\n", i+1, tool.Name)
		if tool.Description != "" {
			fmt.Printf("     %s\n", tool.Description)
		}
		if len(tool.InputSchema) > 0 {
			var schema struct {
				Properties map[string]struct {
					Type        string `json:"type"`
					Description string `json:"description"`
				} `json:"properties"`
				Required []string `json:"required"`
			}
			if json.Unmarshal(tool.InputSchema, &schema) == nil && len(schema.Properties) > 0 {
				fmt.Printf("     Parameters:\n")
				for name, prop := range schema.Properties {
					req := ""
					for _, r := range schema.Required {
						if r == name {
							req = " (required)"
							break
						}
					}
					desc := ""
					if prop.Description != "" {
						desc = " — " + prop.Description
					}
					fmt.Printf("       - %s: %s%s%s\n", name, prop.Type, req, desc)
				}
			}
		}
		fmt.Println()
	}

	return nil
}
