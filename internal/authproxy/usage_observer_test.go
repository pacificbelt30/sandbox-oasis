package authproxy

import (
	"strings"
	"testing"
)

func TestUsageObserverRequiresCompleteStructuredProviderUsage(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		tokens                  int64
		known                   bool
	}{
		{"openai-json", "application/json", `{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`, 5, true},
		{"openai-sse", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"total_tokens\":6}}}\r\n\r\n", 6, true},
		{"chat-sse", "text/event-stream", "data: {\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\n", 7, true},
		{"anthropic-sse", "text/event-stream", "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3,\"cache_read_input_tokens\":4}}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\ndata: {\"type\":\"message_stop\"}\n\n", 9, true},
		{"incomplete-stream", "text/event-stream", "data: {\"usage\":{\"total_tokens\":7}}\n\n", 0, false},
		{"missing-count", "application/json", `{"usage":{"input_tokens":3}}`, 0, false},
		{"missing-usage", "application/json", `{"output":"no usage"}`, 0, false},
		{"spoofed-content", "application/json", `{"output":"{\"usage\":{\"total_tokens\":0}}"}`, 0, false},
		{"negative", "application/json", `{"usage":{"total_tokens":-1}}`, 0, false},
		{"fraction", "application/json", `{"usage":{"total_tokens":1.5}}`, 0, false},
		{"overflow", "application/json", `{"usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`, 0, false},
		{"unknown-format", "application/octet-stream", `{"usage":{"total_tokens":1}}`, 0, false},
		{"oversize", "application/json", strings.Repeat(" ", usageSampleLimit+1), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{1, 7, 32768} {
				o := &usageObserver{}
				for offset := 0; offset < len(tc.body); offset += chunk {
					end := min(offset+chunk, len(tc.body))
					o.Observe([]byte(tc.body[offset:end]), tc.contentType)
				}
				tokens, known := o.Result(tc.contentType)
				if known != tc.known || tokens != tc.tokens {
					t.Fatalf("chunk %d: tokens=%d known=%t", chunk, tokens, known)
				}
			}
		})
	}
}
