package engine

import ()

// SieveEvent is one streaming event: either content text or parsed tool calls.
type SieveEvent struct {
	Type  string // "content" | "tool_calls"
	Text  string
	Calls []ParsedToolCall
}

// ToolSieve splits streamed content from textual tool-call envelopes. Port of
// the ToolSieve class, including holdback and orphan-parameter rules.
type ToolSieve struct {
	config     *ToolCallConfig
	tools      []map[string]any
	pending    string
	capture    string
	capturing  bool
	holdLength int
}

// NewToolSieve creates a sieve. tools/config follow the Python constructor.
func NewToolSieve(tools any, config *ToolCallConfig, holdLength int) *ToolSieve {
	if config == nil {
		config = DefaultConfig()
	}
	if holdLength <= 0 {
		holdLength = 96
	}
	return &ToolSieve{
		config:     config,
		tools:      NormalizeTools(tools),
		holdLength: holdLength,
	}
}

// ProcessChunk feeds one upstream text fragment and returns emitted events.
func (s *ToolSieve) ProcessChunk(chunk string) []SieveEvent {
	if chunk == "" {
		return nil
	}
	s.pending = canonicalizeMarkup(s.pending + chunk)
	var events []SieveEvent
	if s.capturing {
		s.capture += s.pending
		s.pending = ""
		events = append(events, s.consumeCapture(false)...)
		return events
	}
	start := firstToolMarkerIndex(s.pending, s.config)
	if start >= 0 {
		prefix := s.pending[:start]
		if prefix != "" {
			events = append(events, SieveEvent{Type: "content", Text: stripColonMarkerResidue(prefix)})
		}
		s.capture = s.pending[start:]
		s.pending = ""
		s.capturing = true
		events = append(events, s.consumeCapture(false)...)
		return events
	}
	if len(s.pending) <= s.holdLength {
		return events
	}
	safe := s.pending[:len(s.pending)-s.holdLength]
	s.pending = s.pending[len(s.pending)-s.holdLength:]
	if safe != "" {
		events = append(events, SieveEvent{Type: "content", Text: stripColonMarkerResidue(safe)})
	}
	return events
}

// Flush emits the trailing capture/pending. Port of ToolSieve.flush.
func (s *ToolSieve) Flush() []SieveEvent {
	var events []SieveEvent
	if s.capturing && s.capture != "" {
		consumed := s.consumeCapture(true)
		if len(consumed) > 0 {
			events = append(events, consumed...)
		} else if firstToolMarkerIndex(s.capture, s.config) >= 0 {
			// Markup that failed to parse (e.g. unknown tool): drop, never leak.
		} else {
			events = append(events, SieveEvent{Type: "content", Text: stripColonMarkerResidue(s.capture)})
		}
		s.capture = ""
		s.capturing = false
	}
	if s.pending != "" {
		if firstToolMarkerIndex(s.pending, s.config) >= 0 {
			// Pending markup that was never captured: drop.
		} else {
			events = append(events, SieveEvent{Type: "content", Text: stripColonMarkerResidue(s.pending)})
		}
		s.pending = ""
	}
	return events
}

func (s *ToolSieve) consumeCapture(force bool) []SieveEvent {
	s.capture = canonicalizeMarkup(s.capture)
	if !force && hasOpenProtocolBlock(s.capture, s.config) {
		if s.rootOpen(s.capture) {
			// Parallel invokes share one root: wait for the whole root to close.
			if !s.rootClosed(s.capture) {
				return nil
			}
		} else if s.orphanParameter(s.capture) {
			// R35: hold until flush; parse_tool_calls covers it then.
			return nil
		} else if !looksStructurallyClosed(s.capture, s.config) {
			// A standalone invoke: parse once it closes.
			return nil
		}
	}
	// Ported fix: an outer CDATA literal is still open, so an inner protocol
	// close tag must not end the capture. Only force (flush) may parse it.
	if !force && hasUnclosedCDATALiteral(s.capture) {
		return nil
	}
	calls, err := ParseToolCalls(s.capture, s.tools, s.config)
	if err != nil || len(calls) == 0 {
		return nil
	}
	s.capture = ""
	s.capturing = false
	return []SieveEvent{{Type: "tool_calls", Calls: calls}}
}

func (s *ToolSieve) rootOpen(text string) bool {
	for _, protocol := range s.config.ParseProtocols {
		if protocolOpenTagRe(protocol.Root).MatchString(text) {
			return true
		}
	}
	return false
}

func (s *ToolSieve) orphanParameter(text string) bool {
	for _, protocol := range s.config.ParseProtocols {
		if protocolOpenTagRe(protocol.Root).MatchString(text) {
			return false
		}
		if protocolOpenTagRe(protocol.Invoke).MatchString(text) {
			return false
		}
	}
	for _, protocol := range s.config.ParseProtocols {
		if protocolOpenTagRe(protocol.Parameter).MatchString(text) {
			return true
		}
	}
	return false
}

func (s *ToolSieve) rootClosed(text string) bool {
	for _, protocol := range s.config.ParseProtocols {
		if len(iterProtocolBlocks(text, protocol, protocol.Root)) > 0 {
			return true
		}
	}
	return false
}
