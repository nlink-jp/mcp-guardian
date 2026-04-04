package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const webhookTimeout = 2 * time.Second

// Event types.
const (
	EventBlocked         = "blocked"
	EventLoopDetected    = "loop_detected"
	EventSessionComplete = "session_complete"
)

// Payload represents a webhook notification payload.
type Payload struct {
	Event     string                 `json:"event"`
	Timestamp int64                  `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// Fire sends a webhook notification to all configured URLs.
// Fire-and-forget: errors are silently ignored.
func Fire(urls []string, event string, data map[string]interface{}) {
	if len(urls) == 0 {
		return
	}

	payload := Payload{
		Event:     event,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}

	for _, url := range urls {
		go send(url, payload)
	}
}

func send(url string, payload Payload) {
	body, err := json.Marshal(formatForTarget(url, payload))
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), webhookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// formatForTarget adjusts the payload for specific webhook targets.
func formatForTarget(url string, payload Payload) interface{} {
	// Discord webhook format
	if strings.Contains(url, "discord.com/api/webhooks") {
		msg := formatDiscord(payload)
		return map[string]string{"content": msg}
	}
	// Telegram format
	if strings.Contains(url, "api.telegram.org") {
		msg := formatTelegram(payload)
		return map[string]interface{}{
			"text":       msg,
			"parse_mode": "Markdown",
		}
	}
	return payload
}

func formatDiscord(p Payload) string {
	var b strings.Builder
	b.WriteString("**mcp-guardian** | ")
	b.WriteString(p.Event)
	b.WriteString("\n")
	if tool, ok := p.Data["toolName"].(string); ok {
		b.WriteString("Tool: `" + tool + "`\n")
	}
	if reason, ok := p.Data["reason"].(string); ok {
		b.WriteString("Reason: " + reason + "\n")
	}
	return b.String()
}

func formatTelegram(p Payload) string {
	var b strings.Builder
	b.WriteString("*mcp-guardian* | ")
	b.WriteString(p.Event)
	b.WriteString("\n")
	if tool, ok := p.Data["toolName"].(string); ok {
		b.WriteString("Tool: `" + tool + "`\n")
	}
	if reason, ok := p.Data["reason"].(string); ok {
		b.WriteString("Reason: " + reason + "\n")
	}
	return b.String()
}
