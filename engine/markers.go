package engine

import (
	"regexp"
	"strings"
)

// firstToolMarkerIndex returns the earliest offset of any tool-call marker, or
// -1. Port of _first_tool_marker_index.
func firstToolMarkerIndex(text string, config *ToolCallConfig) int {
	indexes := make([]int, 0, 8)
	for _, protocol := range config.ParseProtocols {
		for _, tag := range []string{protocol.Root, protocol.Invoke, protocol.Parameter} {
			if loc := protocolOpenTagRe(tag).FindStringIndex(text); loc != nil {
				indexes = append(indexes, loc[0])
			}
		}
	}
	for _, protocol := range config.ParseProtocols {
		if loc := bareProtocolMarkerRe(protocol.Name).FindStringIndex(text); loc != nil {
			indexes = append(indexes, loc[0])
		}
	}
	for _, re := range []*regexp.Regexp{jsonToolMarkerRe, funcNameMarkerRe} {
		if loc := re.FindStringIndex(text); loc != nil {
			indexes = append(indexes, loc[0])
		}
	}
	if loc := looseTagMarkerRe.FindStringIndex(text); loc != nil {
		indexes = append(indexes, loc[0])
	}
	if len(indexes) == 0 {
		return -1
	}
	best := indexes[0]
	for _, index := range indexes[1:] {
		if index < best {
			best = index
		}
	}
	return best
}

// hasOpenProtocolBlock keeps the sieve guard symmetric with marker detection.
// Port of _has_open_protocol_block.
func hasOpenProtocolBlock(text string, config *ToolCallConfig) bool {
	for _, protocol := range config.ParseProtocols {
		for _, tag := range []string{protocol.Root, protocol.Invoke, protocol.Parameter} {
			if protocolOpenTagRe(tag).MatchString(text) {
				return true
			}
		}
	}
	for _, protocol := range config.ParseProtocols {
		if bareProtocolMarkerRe(protocol.Name).MatchString(text) {
			return true
		}
	}
	return jsonToolMarkerRe.MatchString(text) ||
		funcNameMarkerRe.MatchString(text) ||
		looseTagMarkerRe.MatchString(text)
}

// looksStructurallyClosed ports _looks_structurally_closed.
func looksStructurallyClosed(text string, config *ToolCallConfig) bool {
	if structCloseRe.MatchString(text) {
		return true
	}
	for _, protocol := range config.ParseProtocols {
		if protocolAnyCloseTagRe(protocol.Root, protocol.Invoke, protocol.Parameter).MatchString(text) {
			return true
		}
	}
	return false
}

// hasUnclosedCDATALiteral reports whether text contains a CDATA opener that is
// not paired and has no orphan closer (a literal whose closer has not arrived
// yet). See DESIGN.md §4.6 — the ported fix for the streaming nesting case.
func hasUnclosedCDATALiteral(text string) bool {
	lower := strings.ToLower(text)
	openers := strings.Count(lower, cdataOpener)
	if openers == 0 {
		return false
	}
	pairs := len(cdataRe.FindAllStringSubmatchIndex(text, -1))
	if openers <= pairs {
		return false
	}
	// A surplus opener that already has an orphan closer is a completed greedy
	// span (R24), not a still-open literal.
	if strings.Count(lower, cdataCloser) > pairs {
		return false
	}
	return true
}
