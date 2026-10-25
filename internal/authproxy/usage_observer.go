package authproxy

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

const usageSampleLimit = 1 << 20

// Only bounded upstream response fragments are inspected; nothing is logged.
// A missing/truncated/unsupported usage record is unknown, never zero usage.
type usageObserver struct {
	buffer                                      []byte
	input, output, cacheRead, cacheWrite, total int64
	known, complete, invalid                    bool
	seenInput, seenOutput, seenTotal            bool
}

func (o *usageObserver) Observe(data []byte, contentType string) {
	if o.invalid {
		return
	}
	if len(o.buffer)+len(data) > usageSampleLimit {
		o.invalid = true
		o.buffer = nil
		return
	}
	o.buffer = append(o.buffer, data...)
	if strings.HasPrefix(contentType, "text/event-stream") {
		// Normalize CRLF even when the pair straddles two Read calls.
		o.buffer = bytes.ReplaceAll(o.buffer, []byte("\r\n"), []byte("\n"))
		for {
			end := bytes.Index(o.buffer, []byte("\n\n"))
			if end < 0 {
				break
			}
			var payload []byte
			for _, line := range bytes.Split(o.buffer[:end], []byte("\n")) {
				if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
					if len(payload) > 0 {
						payload = append(payload, '\n')
					}
					payload = append(payload, bytes.TrimPrefix(value, []byte(" "))...)
				}
			}
			if bytes.Equal(payload, []byte("[DONE]")) {
				o.complete = true
			} else if len(payload) > 0 {
				o.parse(payload)
			}
			o.buffer = append(o.buffer[:0], o.buffer[end+2:]...)
		}
	}
}

type observedUsage struct {
	Input      *int64 `json:"input_tokens"`
	Output     *int64 `json:"output_tokens"`
	Prompt     *int64 `json:"prompt_tokens"`
	Completion *int64 `json:"completion_tokens"`
	Total      *int64 `json:"total_tokens"`
	CacheRead  *int64 `json:"cache_read_input_tokens"`
	CacheWrite *int64 `json:"cache_creation_input_tokens"`
}

func (o *usageObserver) parse(data []byte) {
	var value struct {
		Type     string         `json:"type"`
		Usage    *observedUsage `json:"usage"`
		Response struct {
			Usage *observedUsage `json:"usage"`
		} `json:"response"`
		Message struct {
			Usage *observedUsage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		o.invalid = true
		return
	}
	if value.Type == "response.completed" || value.Type == "response.incomplete" || value.Type == "response.failed" || value.Type == "message_stop" {
		o.complete = true
	}
	for _, u := range []*observedUsage{value.Usage, value.Response.Usage, value.Message.Usage} {
		if u == nil {
			continue
		}
		o.seenInput = o.seenInput || u.Input != nil || u.Prompt != nil
		o.seenOutput = o.seenOutput || u.Output != nil || u.Completion != nil
		o.seenTotal = o.seenTotal || u.Total != nil
		for _, field := range []struct {
			value  *int64
			target *int64
		}{
			{u.Input, &o.input}, {u.Prompt, &o.input}, {u.Output, &o.output}, {u.Completion, &o.output},
			{u.Total, &o.total}, {u.CacheRead, &o.cacheRead}, {u.CacheWrite, &o.cacheWrite},
		} {
			if field.value == nil {
				continue
			}
			if *field.value < 0 {
				o.invalid = true
				return
			}
			o.known = true
			*field.target = max(*field.target, *field.value)
		}
	}
}

func (o *usageObserver) Result(contentType string) (int64, bool) {
	if o.invalid {
		return 0, false
	}
	if strings.HasPrefix(contentType, "application/json") {
		o.parse(o.buffer)
		o.complete = !o.invalid
	}
	if o.invalid || !o.known || !o.complete || !o.seenTotal && (!o.seenInput || !o.seenOutput) {
		return 0, false
	}
	sum := int64(0)
	for _, n := range []int64{o.input, o.output, o.cacheRead, o.cacheWrite} {
		if n > math.MaxInt64-sum {
			return 0, false
		}
		sum += n
	}
	return max(sum, o.total), true
}
