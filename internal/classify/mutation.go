package classify

import (
	"encoding/json"
	"strings"
	"unicode"
)

// MutationType constants.
const (
	Mutating = "mutating"
	ReadOnly = "readonly"
)

// writeVerbs are verb tokens that indicate a mutating operation.
var writeVerbs = map[string]bool{
	"write": true, "create": true, "delete": true, "remove": true,
	"update": true, "set": true, "put": true, "post": true,
	"patch": true, "insert": true, "append": true, "modify": true,
	"rename": true, "move": true, "copy": true, "replace": true,
	"add": true, "drop": true, "truncate": true, "edit": true,
	"save": true, "push": true, "execute": true, "run": true,
	"apply": true, "install": true, "uninstall": true,
}

// readVerbs are verb tokens that indicate a read-only operation.
var readVerbs = map[string]bool{
	"read": true, "get": true, "list": true, "search": true,
	"find": true, "query": true, "fetch": true, "show": true,
	"describe": true, "inspect": true, "view": true, "check": true,
	"stat": true, "info": true, "status": true, "count": true,
	"exists": true, "head": true, "options": true, "browse": true,
}

// writeArgKeys are argument keys that suggest mutation.
var writeArgKeys = map[string]bool{
	"content": true, "data": true, "body": true,
	"text": true, "value": true, "payload": true,
	"source": true, "code": true, "script": true,
}

// sqlWritePattern checks for SQL write patterns.
var sqlWriteKeywords = []string{
	"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER",
	"TRUNCATE", "REPLACE", "MERGE", "GRANT", "REVOKE",
}

// ClassifyMutation classifies a tool call as mutating or readonly.
// Uses a 3-layer strategy: schema -> verb heuristic -> arg inspection.
// Unknown = mutating (deny-by-default).
func ClassifyMutation(toolName string, args map[string]interface{}, inputSchema json.RawMessage) string {
	// Layer 1: Schema-based
	if result := classifyBySchema(inputSchema); result != "" {
		return result
	}
	// Layer 2: Verb heuristic
	if result := classifyByVerb(toolName); result != "" {
		return result
	}
	// Layer 3: Arg inspection
	if result := classifyByArgs(args); result != "" {
		return result
	}
	// Default: mutating (deny-by-default)
	return Mutating
}

func classifyBySchema(schema json.RawMessage) string {
	if len(schema) == 0 {
		return ""
	}
	var s struct {
		Properties map[string]interface{} `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil || s.Properties == nil {
		return ""
	}
	for key := range s.Properties {
		if writeArgKeys[strings.ToLower(key)] {
			return Mutating
		}
	}
	return ""
}

func classifyByVerb(toolName string) string {
	// tokenize: split on _, -, and camelCase boundaries
	tokens := tokenize(toolName)

	for _, token := range tokens {
		if writeVerbs[token] {
			return Mutating
		}
		if readVerbs[token] {
			return ReadOnly
		}
	}
	return ""
}

// tokenize splits a tool name into lowercase tokens by _, -, and camelCase boundaries.
// Examples:
//
//	"get_status"           → ["get", "status"]
//	"delete-file"          → ["delete", "file"]
//	"getConfluenceSpaces"  → ["get", "confluence", "spaces"]
//	"atlassianUserInfo"    → ["atlassian", "user", "info"]
//	"getHTTPResponse"      → ["get", "http", "response"]
func tokenize(name string) []string {
	// First split on _ and -
	name = strings.ReplaceAll(name, "-", "_")
	parts := strings.Split(name, "_")

	var tokens []string
	for _, part := range parts {
		tokens = append(tokens, splitCamelCase(part)...)
	}
	return tokens
}

// splitCamelCase splits a single word on camelCase boundaries and lowercases each token.
// Handles runs of uppercase letters (acronyms) like "HTTP" in "getHTTPResponse".
func splitCamelCase(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var tokens []string
	start := 0
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) {
			// Transition: lowercase→uppercase starts a new token
			if !unicode.IsUpper(runes[i-1]) {
				tokens = append(tokens, strings.ToLower(string(runes[start:i])))
				start = i
			} else if i+1 < len(runes) && !unicode.IsUpper(runes[i+1]) {
				// Transition: uppercase run ending (e.g., "HTT|P|R" → split before last upper of run)
				tokens = append(tokens, strings.ToLower(string(runes[start:i])))
				start = i
			}
		}
	}
	tokens = append(tokens, strings.ToLower(string(runes[start:])))
	return tokens
}

func classifyByArgs(args map[string]interface{}) string {
	for key := range args {
		if writeArgKeys[strings.ToLower(key)] {
			return Mutating
		}
	}
	// check for SQL write patterns in string values
	for _, v := range args {
		if s, ok := v.(string); ok {
			upper := strings.ToUpper(s)
			for _, kw := range sqlWriteKeywords {
				if strings.Contains(upper, kw) {
					return Mutating
				}
			}
		}
	}
	return ""
}
