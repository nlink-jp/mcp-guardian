package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nlink-jp/mcp-guardian/internal/cli"
	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/proxy"
)

var version = "dev"

func main() {
	// CLI flags
	upstream := flag.String("upstream", "", "Upstream MCP server command")
	stateDir := flag.String("state-dir", ".governance", "Governance state directory")
	enforcement := flag.String("enforcement", "strict", "Enforcement mode: strict or advisory")
	schemaMode := flag.String("schema", "warn", "Schema validation: off, warn, or strict")
	maxCalls := flag.Int("max-calls", 0, "Maximum tool calls (0 = unlimited)")
	timeoutMs := flag.Int("timeout", 300000, "Upstream response timeout in ms")

	// Analysis commands
	viewCmd := flag.Bool("view", false, "View receipt timeline")
	verifyCmd := flag.Bool("verify", false, "Verify hash chain integrity")
	explainCmd := flag.Bool("explain", false, "Explain session activity")
	receiptsCmd := flag.Bool("receipts", false, "Show session summary")

	// View filters
	filterTool := flag.String("tool", "", "Filter by tool name (for --view)")
	filterOutcome := flag.String("outcome", "", "Filter by outcome (for --view)")
	viewLimit := flag.Int("limit", 0, "Limit number of receipts (for --view)")

	// Wrap/unwrap
	wrapServer := flag.String("wrap", "", "Wrap an MCP server in .mcp.json")
	unwrapServer := flag.String("unwrap", "", "Unwrap an MCP server in .mcp.json")
	configPath := flag.String("config", "", "Path to .mcp.json (for wrap/unwrap)")

	// Webhook (can be specified multiple times)
	var webhookURLs multiFlag
	flag.Var(&webhookURLs, "webhook", "Webhook URL (repeatable)")

	showVersion := flag.Bool("version", false, "Show version")

	flag.Parse()

	if *showVersion {
		fmt.Printf("mcp-guardian %s\n", version)
		os.Exit(0)
	}

	// Analysis commands
	if *viewCmd {
		cli.View(*stateDir, *filterTool, *filterOutcome, *viewLimit)
		return
	}
	if *verifyCmd {
		cli.Verify(*stateDir)
		return
	}
	if *explainCmd {
		cli.Explain(*stateDir)
		return
	}
	if *receiptsCmd {
		cli.Receipts(*stateDir)
		return
	}

	// Wrap/unwrap
	if *wrapServer != "" {
		cli.Wrap(*wrapServer, *stateDir, *enforcement, *configPath)
		return
	}
	if *unwrapServer != "" {
		cli.Unwrap(*unwrapServer, *configPath)
		return
	}

	// Determine upstream command
	upstreamCmd := *upstream
	upstreamArgs := flag.Args()
	if upstreamCmd == "" && len(upstreamArgs) > 0 {
		upstreamCmd = upstreamArgs[0]
		upstreamArgs = upstreamArgs[1:]
	}

	if upstreamCmd == "" {
		fmt.Fprintln(os.Stderr, "error: --upstream or trailing command required")
		fmt.Fprintln(os.Stderr, "usage: mcp-guardian --upstream 'command' [options]")
		fmt.Fprintln(os.Stderr, "       mcp-guardian [options] -- command [args...]")
		os.Exit(1)
	}

	// Proxy mode
	cfg := config.Defaults()
	cfg.Upstream = upstreamCmd
	cfg.StateDir = *stateDir
	cfg.Enforcement = *enforcement
	cfg.SchemaMode = *schemaMode
	cfg.MaxCalls = *maxCalls
	cfg.TimeoutMs = *timeoutMs
	cfg.WebhookURLs = []string(webhookURLs)

	if err := proxy.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-guardian: %v\n", err)
		os.Exit(1)
	}
}

// multiFlag allows a flag to be specified multiple times.
type multiFlag []string

func (f *multiFlag) String() string { return fmt.Sprintf("%v", *f) }
func (f *multiFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}
