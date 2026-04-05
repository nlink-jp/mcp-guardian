package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nlink-jp/mcp-guardian/internal/cli"
	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/mask"
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
	mcpConfigPath := flag.String("mcp-config", "", "Path to .mcp.json (for wrap/unwrap)")

	// Config files
	globalConfig := flag.String("config", "", "Path to global config file (JSON)")
	serverConfig := flag.String("server-config", "", "Path to per-server config file (JSON)")

	// Tool masking
	var maskPatterns multiFlag
	flag.Var(&maskPatterns, "mask", "Tool mask pattern (repeatable, supports wildcards)")
	maskFile := flag.String("mask-file", "", "Path to mask patterns file (one pattern per line)")

	// OTLP export
	otlpEndpoint := flag.String("otlp-endpoint", "", "OTLP/HTTP endpoint URL (empty = disabled)")
	var otlpHeaders headerFlag
	flag.Var(&otlpHeaders, "otlp-header", "OTLP HTTP header KEY=VALUE (repeatable)")
	otlpBatchSize := flag.Int("otlp-batch-size", 10, "OTLP export batch size")
	otlpBatchTimeout := flag.Int("otlp-batch-timeout", 5000, "OTLP export batch timeout in ms")

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
		if err := cli.View(*stateDir, *filterTool, *filterOutcome, *viewLimit); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *verifyCmd {
		if err := cli.Verify(*stateDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *explainCmd {
		if err := cli.Explain(*stateDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *receiptsCmd {
		if err := cli.Receipts(*stateDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Wrap/unwrap
	if *wrapServer != "" {
		if err := cli.Wrap(*wrapServer, *stateDir, *enforcement, *mcpConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *unwrapServer != "" {
		if err := cli.Unwrap(*unwrapServer, *mcpConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
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

	// Proxy mode: build config with priority Defaults → config file → CLI flags
	cfg := config.Defaults()

	// Layer 2: Apply global config (OTLP, webhooks, defaults)
	if *globalConfig != "" {
		gc, err := config.LoadGlobal(*globalConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		gc.ApplyTo(cfg)
	}

	// Layer 3: Apply server config (enforcement, mask, etc.)
	if *serverConfig != "" {
		sc, err := config.LoadServer(*serverConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		sc.ApplyTo(cfg)
	}

	// Layer 4: Apply explicitly set CLI flags (override config files)
	setFlags := flagsExplicitlySet()
	if setFlags["enforcement"] {
		cfg.Enforcement = *enforcement
	}
	if setFlags["schema"] {
		cfg.SchemaMode = *schemaMode
	}
	if setFlags["max-calls"] {
		cfg.MaxCalls = *maxCalls
	}
	if setFlags["timeout"] {
		cfg.TimeoutMs = *timeoutMs
	}
	if setFlags["state-dir"] {
		cfg.StateDir = absStateDir
	} else if cfg.StateDir == "" {
		cfg.StateDir = absStateDir
	} else {
		// Canonicalize config-file stateDir too
		abs, err := filepath.Abs(cfg.StateDir)
		if err == nil {
			cfg.StateDir = abs
		}
	}
	if setFlags["otlp-endpoint"] {
		cfg.OTLPEndpoint = *otlpEndpoint
	}
	if setFlags["otlp-batch-size"] {
		cfg.OTLPBatchSize = *otlpBatchSize
	}
	if setFlags["otlp-batch-timeout"] {
		cfg.OTLPBatchTimeout = *otlpBatchTimeout
	}

	// CLI OTLP headers override config file headers for same keys
	cliHeaders := otlpHeaders.toMap()
	if len(cliHeaders) > 0 {
		if cfg.OTLPHeaders == nil {
			cfg.OTLPHeaders = make(map[string]string)
		}
		for k, v := range cliHeaders {
			cfg.OTLPHeaders[k] = v
		}
	}

	// Merge list fields: CLI appends to config file values
	if len(webhookURLs) > 0 {
		cfg.WebhookURLs = append(cfg.WebhookURLs, []string(webhookURLs)...)
	}
	if len(maskPatterns) > 0 {
		cfg.MaskPatterns = append(cfg.MaskPatterns, []string(maskPatterns)...)
	}
	if *maskFile != "" {
		filePatterns, err := mask.LoadPatterns(*maskFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		cfg.MaskPatterns = append(cfg.MaskPatterns, filePatterns...)
	}

	cfg.Upstream = args[0]
	cfg.UpstreamArgs = args[1:]

	// Validate merged config
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := proxy.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-guardian: %v\n", err)
		os.Exit(1)
	}
}

// flagsExplicitlySet returns a set of flag names that were explicitly provided on the CLI.
func flagsExplicitlySet() map[string]bool {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

// multiFlag allows a flag to be specified multiple times.
type multiFlag []string

func (f *multiFlag) String() string { return fmt.Sprintf("%v", *f) }
func (f *multiFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// headerFlag parses KEY=VALUE pairs for HTTP headers.
type headerFlag []string

func (f *headerFlag) String() string { return fmt.Sprintf("%v", *f) }
func (f *headerFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func (f *headerFlag) toMap() map[string]string {
	m := make(map[string]string)
	for _, h := range *f {
		for i := 0; i < len(h); i++ {
			if h[i] == '=' {
				m[h[:i]] = h[i+1:]
				break
			}
		}
	}
	return m
}
