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
	// Server profiles
	profileFlag := flag.String("profile", "", "Server profile name or path")
	profilesCmd := flag.Bool("profiles", false, "List available server profiles")
	loginCmd := flag.String("login", "", "Perform OAuth2 browser login for a profile")

	// Global config
	globalConfig := flag.String("config", "", "Path to global config file (JSON)")

	// Analysis commands
	stateDir := flag.String("state-dir", "", "Override state directory for analysis commands")
	viewCmd := flag.Bool("view", false, "View receipt timeline")
	verifyCmd := flag.Bool("verify", false, "Verify hash chain integrity")
	explainCmd := flag.Bool("explain", false, "Explain session activity")
	receiptsCmd := flag.Bool("receipts", false, "Show session summary")

	// View filters
	filterTool := flag.String("tool", "", "Filter by tool name (for --view)")
	filterOutcome := flag.String("outcome", "", "Filter by outcome (for --view)")
	viewLimit := flag.Int("limit", 0, "Limit number of receipts (for --view)")

	showVersion := flag.Bool("version", false, "Show version")

	flag.Parse()

	if *showVersion {
		fmt.Printf("mcp-guardian %s\n", version)
		os.Exit(0)
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

	// Resolve stateDir from --profile for analysis commands
	isAnalysisCmd := *viewCmd || *verifyCmd || *explainCmd || *receiptsCmd
	resolvedStateDir := *stateDir
	if *profileFlag != "" && resolvedStateDir == "" {
		profile, err := config.ResolveProfile(*profileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if profile.StateDir != "" {
			resolvedStateDir = profile.StateDir
		}
	}
	if resolvedStateDir == "" {
		resolvedStateDir = ".governance"
	}

	// Analysis commands
	if isAnalysisCmd {
		if *profileFlag == "" && *stateDir == "" {
			receiptsPath := filepath.Join(resolvedStateDir, "receipts.jsonl")
			if _, err := os.Stat(receiptsPath); os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "No receipts found in default state directory (.governance).")
				fmt.Fprintln(os.Stderr, "Specify a profile: mcp-guardian --profile <name> --receipts")
				os.Exit(1)
			}
		}

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
	}

	// Proxy mode: --profile is required
	if *profileFlag == "" {
		fmt.Fprintln(os.Stderr, "error: --profile is required")
		fmt.Fprintln(os.Stderr, "usage: mcp-guardian --profile <name|path>")
		fmt.Fprintln(os.Stderr, "       mcp-guardian --profiles")
		fmt.Fprintln(os.Stderr, "       mcp-guardian --login <name|path>")
		os.Exit(1)
	}

	// Build config: Defaults → Global → Profile
	cfg := config.Defaults()

	// Layer 1: Global config (auto-discover or explicit)
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

	// Layer 2: Server profile
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

	// Canonicalize stateDir
	if cfg.StateDir == "" {
		cfg.StateDir = ".governance"
	}
	abs, err := filepath.Abs(cfg.StateDir)
	if err == nil {
		cfg.StateDir = abs
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := proxy.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-guardian: %v\n", err)
		os.Exit(1)
	}
}
