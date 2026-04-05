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

	// Server profiles
	profileFlag := flag.String("profile", "", "Server profile name or path")
	profilesCmd := flag.Bool("profiles", false, "List available server profiles")
	loginCmd := flag.String("login", "", "Perform OAuth2 browser login for a profile")

	// Tool masking
	var maskPatterns multiFlag
	flag.Var(&maskPatterns, "mask", "Tool mask pattern (repeatable, supports wildcards)")
	maskFile := flag.String("mask-file", "", "Path to mask patterns file (one pattern per line)")

	// Transport mode
	transportMode := flag.String("transport", "stdio", "Upstream transport: stdio or sse")
	upstreamURL := flag.String("upstream-url", "", "Upstream MCP server URL (for sse transport)")
	var sseHeaders headerFlag
	flag.Var(&sseHeaders, "sse-header", "SSE HTTP header KEY=VALUE (repeatable)")

	// OAuth2 authentication (for sse transport)
	oauth2TokenURL := flag.String("oauth2-token-url", "", "OAuth2 token endpoint URL")
	oauth2ClientID := flag.String("oauth2-client-id", "", "OAuth2 client ID")
	oauth2ClientSecret := flag.String("oauth2-client-secret", "", "OAuth2 client secret")
	var oauth2Scopes multiFlag
	flag.Var(&oauth2Scopes, "oauth2-scope", "OAuth2 scope (repeatable)")
	tokenCommand := flag.String("token-command", "", "External command to obtain Bearer token")
	var tokenCommandArgs multiFlag
	flag.Var(&tokenCommandArgs, "token-command-arg", "Token command argument (repeatable)")

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

	// Resolve stateDir from --profile if specified and --state-dir was not explicit
	resolvedStateDir := *stateDir
	if *profileFlag != "" {
		profile, err := config.ResolveProfile(*profileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if profile.StateDir != "" {
			// Use profile's stateDir unless --state-dir was explicitly set
			setFlags := flagsExplicitlySet()
			if !setFlags["state-dir"] {
				resolvedStateDir = profile.StateDir
			}
		}
	}

	// Analysis commands
	if *viewCmd {
		if err := cli.View(resolvedStateDir, *filterTool, *filterOutcome, *viewLimit); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *verifyCmd {
		if err := cli.Verify(resolvedStateDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *explainCmd {
		if err := cli.Explain(resolvedStateDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *receiptsCmd {
		if err := cli.Receipts(resolvedStateDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// List profiles
	if *profilesCmd {
		names, err := config.ListProfiles()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(names) == 0 {
			fmt.Fprintf(os.Stderr, "No profiles found in %s\n", config.ProfileDir())
		} else {
			for _, name := range names {
				fmt.Println(name)
			}
		}
		return
	}

	// OAuth2 browser login
	if *loginCmd != "" {
		if err := cli.Login(*loginCmd); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Wrap/unwrap
	if *wrapServer != "" {
		opts := cli.WrapOptions{
			ServerName:    *wrapServer,
			StateDir:      *stateDir,
			Enforcement:   *enforcement,
			MCPConfigPath: *mcpConfigPath,
			ProfileName:   *profileFlag,
			GlobalConfig:  *globalConfig,
		}
		if err := cli.WrapWithOptions(opts); err != nil {
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

	// Proxy mode: build config with priority Defaults → Global → Profile → CLI flags
	cfg := config.Defaults()

	// Layer 2: Apply global config (telemetry, org defaults)
	// Auto-discover from ~/.config/mcp-guardian/config.json unless --config is explicit
	if *globalConfig != "" {
		gc, err := config.LoadGlobal(*globalConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		gc.ApplyTo(cfg)
	} else {
		gc, err := config.LoadGlobalAuto()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to load global config: %v\n", err)
		}
		if gc != nil {
			gc.ApplyTo(cfg)
		}
	}

	// Layer 3: Apply server profile
	if *profileFlag != "" {
		profile, err := config.ResolveProfile(*profileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := profile.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		profile.ApplyTo(cfg)
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

	// Transport settings
	if setFlags["transport"] {
		cfg.Transport = *transportMode
	}
	if setFlags["upstream-url"] {
		cfg.UpstreamURL = *upstreamURL
	}
	sseHeaderMap := sseHeaders.toMap()
	if len(sseHeaderMap) > 0 {
		if cfg.SSEHeaders == nil {
			cfg.SSEHeaders = make(map[string]string)
		}
		for k, v := range sseHeaderMap {
			cfg.SSEHeaders[k] = v
		}
	}

	// OAuth2 settings
	if setFlags["oauth2-token-url"] {
		cfg.OAuth2TokenURL = *oauth2TokenURL
	}
	if setFlags["oauth2-client-id"] {
		cfg.OAuth2ClientID = *oauth2ClientID
	}
	if setFlags["oauth2-client-secret"] {
		cfg.OAuth2ClientSecret = *oauth2ClientSecret
	}
	if len(oauth2Scopes) > 0 {
		cfg.OAuth2Scopes = append(cfg.OAuth2Scopes, []string(oauth2Scopes)...)
	}
	if setFlags["token-command"] {
		cfg.TokenCommand = *tokenCommand
		cfg.TokenCommandArgs = []string(tokenCommandArgs)
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

	// Set upstream command for stdio transport
	// Trailing args (-- command) override profile/config command if present
	if cfg.Transport == "" || cfg.Transport == "stdio" {
		if len(args) > 0 {
			cfg.Upstream = args[0]
			cfg.UpstreamArgs = args[1:]
		} else if cfg.Upstream == "" {
			fmt.Fprintln(os.Stderr, "error: upstream command required (use --profile or -- command [args...])")
			fmt.Fprintln(os.Stderr, "usage: mcp-guardian [options] -- command [args...]")
			fmt.Fprintln(os.Stderr, "       mcp-guardian --profile <name>")
			os.Exit(1)
		}
	}

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
