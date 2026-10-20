package util

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/Sliverkiss/toolkit/jsonx"
)

// FormatSSE frames one payload as an SSE event. Multi-line data is split on the
// same boundaries as Python's str.splitlines(). Port of format_sse.
func FormatSSE(payload any, event string) string {
	data, ok := payload.(string)
	if !ok {
		data = jsonx.Dumps(payload)
	}
	var lines []string
	if event != "" {
		lines = append(lines, "event: "+event)
	}
	parts := splitLines(data)
	if len(parts) == 0 {
		parts = []string{""}
	}
	for _, part := range parts {
		lines = append(lines, "data: "+part)
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n") + "\n"
}

// DoneFrame is the OpenAI end-of-stream sentinel.
func DoneFrame() string { return "data: [DONE]\n\n" }

// TryJSON decodes a data frame into a JSON object; anything else (including
// "[DONE]" and arrays/scalars) yields nil. Port of try_json.
func TryJSON(data string) map[string]any {
	if data == "" || data == "[DONE]" {
		return nil
	}
	value, ok := jsonx.TryJSONValue(data)
	if !ok {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

// SSEEvent is one parsed server-sent event.
type SSEEvent struct {
	Event    string
	Data     string
	Comments []string
	ID       string
	Retry    *int
}

// IsDone reports whether the event data is the [DONE] sentinel.
func (e SSEEvent) IsDone() bool { return strings.TrimSpace(e.Data) == "[DONE]" }

// JSON returns the decoded object payload, if any.
func (e SSEEvent) JSON() map[string]any { return TryJSON(e.Data) }

// SSEParser incrementally parses SSE frames. Feed lines without trailing \n.
// Port of util.sse.SSEParser.
type SSEParser struct {
	event     string
	dataParts []string
	comments  []string
	id        string
	retry     *int
}

// NewSSEParser creates an empty parser.
func NewSSEParser() *SSEParser { return &SSEParser{} }

func (p *SSEParser) empty() bool {
	return p.event == "" && len(p.dataParts) == 0 && len(p.comments) == 0 && p.id == "" && p.retry == nil
}

// FeedLine consumes one line and returns a completed event on a blank line.
func (p *SSEParser) FeedLine(line string) *SSEEvent {
	if line == "" {
		if p.empty() {
			return nil
		}
		event := &SSEEvent{
			Event:    p.event,
			Data:     strings.Join(p.dataParts, "\n"),
			Comments: append([]string(nil), p.comments...),
			ID:       p.id,
			Retry:    p.retry,
		}
		p.reset()
		return event
	}
	switch {
	case strings.HasPrefix(line, ":"):
		p.comments = append(p.comments, strings.TrimLeft(line[1:], " \t\n\v\f\r"))
	case strings.HasPrefix(line, "event:"):
		p.event = strings.TrimLeft(line[6:], " \t\n\v\f\r")
	case strings.HasPrefix(line, "data:"):
		p.dataParts = append(p.dataParts, strings.TrimLeft(line[5:], " \t\n\v\f\r"))
	case strings.HasPrefix(line, "id:"):
		p.id = strings.TrimLeft(line[3:], " \t\n\v\f\r")
	case strings.HasPrefix(line, "retry:"):
		if n, err := strconv.Atoi(strings.TrimSpace(line[6:])); err == nil {
			p.retry = &n
		}
	default:
		// Bare data fallback (non-strict servers).
		p.dataParts = append(p.dataParts, line)
	}
	return nil
}

// Flush returns the trailing partial event, if any.
func (p *SSEParser) Flush() *SSEEvent {
	if p.empty() {
		return nil
	}
	event := &SSEEvent{
		Event:    p.event,
		Data:     strings.Join(p.dataParts, "\n"),
		Comments: append([]string(nil), p.comments...),
		ID:       p.id,
		Retry:    p.retry,
	}
	p.reset()
	return event
}

func (p *SSEParser) reset() {
	p.event = ""
	p.dataParts = nil
	p.comments = nil
	p.id = ""
	p.retry = nil
}

// LineSource yields upstream lines and an end condition. Port of Python's
// AsyncIterator[str] line stream. Implementations may return io.EOF.
type LineSource interface {
	Next() (string, error)
}

// IterSSEEvents drains a line source into SSE events, stripping trailing CR.
// Port of iter_sse_events.
func IterSSEEvents(source LineSource) ([]SSEEvent, error) {
	parser := NewSSEParser()
	var events []SSEEvent
	for {
		raw, err := source.Next()
		if err != nil {
			if isEOF(err) {
				break
			}
			return events, err
		}
		line := strings.TrimRight(raw, "\r")
		if event := parser.FeedLine(line); event != nil {
			events = append(events, *event)
		}
	}
	if tail := parser.Flush(); tail != nil {
		events = append(events, *tail)
	}
	return events, nil
}

// splitLines mirrors Python str.splitlines() for the separators SSE payloads
// can contain (\n, \r, \r\n, \v, \f, \x1c-\x1e, \x85,  ,  ).
func isEOF(err error) bool { return errors.Is(err, io.EOF) }

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e:
			out = append(out, s[start:i])
			if b == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
