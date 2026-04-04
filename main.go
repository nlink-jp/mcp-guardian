package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nlink-jp/mcp-guardian/internal/cli"
	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/proxy"
)

var version = "dev"

func main() {
	// CLI flags
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

	// Validate flags
	if err := validateFlags(*enforcement, *schemaMode, *maxCalls, *timeoutMs); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Canonicalize state-dir to prevent path traversal
	absStateDir, err := filepath.Abs(*stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid state-dir: %v\n", err)
		os.Exit(1)
	}

	// Upstream command from trailing args (after --)
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: upstream command required after --")
		fmt.Fprintln(os.Stderr, "usage: mcp-guardian [options] -- command [args...]")
		os.Exit(1)
	}

	// Proxy mode
	cfg := config.Defaults()
	cfg.Upstream = args[0]
	cfg.UpstreamArgs = args[1:]
	cfg.StateDir = absStateDir
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

func validateFlags(enforcement, schemaMode string, maxCalls, timeoutMs int) error {
	switch enforcement {
	case "strict", "advisory":
	default:
		return fmt.Errorf("--enforcement must be 'strict' or 'advisory', got '%s'", enforcement)
	}
	switch schemaMode {
	case "off", "warn", "strict":
	default:
		return fmt.Errorf("--schema must be 'off', 'warn', or 'strict', got '%s'", schemaMode)
	}
	if maxCalls < 0 {
		return fmt.Errorf("--max-calls must be >= 0, got %d", maxCalls)
	}
	if timeoutMs <= 0 {
		return fmt.Errorf("--timeout must be > 0, got %d", timeoutMs)
	}
	return nil
}

// multiFlag allows a flag to be specified multiple times.
type multiFlag []string

func (f *multiFlag) String() string { return fmt.Sprintf("%v", *f) }
func (f *multiFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}
