package engine

import (
	"regexp"
	"strings"
)

const (
	cdataOpener = "<![cdata["
	cdataCloser = "]]>"
)

var (
	cdataTailRe   = regexp.MustCompile(`\]\]?>[ \t]*$`)
	cdataCloserRe = regexp.MustCompile(`\]\]>`)
)

// ComputeCDATABounds returns the single spanning CDATA range in text, if any.
// start is the opener offset; end is the exclusive content end. Port of
// _compute_cdata_bounds (R24 / SPEC-10/11/16/20).
func ComputeCDATABounds(text string) (int, int, bool) {
	lower := strings.ToLower(text)
	matches := cdataRe.FindAllStringSubmatchIndex(text, -1)
	openCount := strings.Count(lower, cdataOpener)
	if openCount <= len(matches) {
		// SPEC-20: an extra closer means the value itself contains "]]>".
		closerCount := strings.Count(lower, cdataCloser)
		if closerCount > len(matches) && len(matches) > 0 {
			lastClose := strings.LastIndex(lower, cdataCloser)
			if lastClose > matches[0][0] {
				return matches[0][0], lastClose, true
			}
		}
		return 0, 0, false
	}
	firstOpen := strings.Index(lower, cdataOpener)
	if firstOpen < 0 {
		return 0, 0, false
	}
	lastClose := strings.LastIndex(lower, cdataCloser)
	if lastClose > firstOpen {
		// Greedy candidate. SPEC-16 anchored guard: only when the surplus
		// opener is nested inside a matched pair AND an orphan closer remains.
		pairStarts := make([]int, 0, len(matches))
		pairEnds := make([]int, 0, len(matches))
		consumedOpeners := map[int]struct{}{}
		consumedClosers := map[int]struct{}{}
		for _, match := range matches {
			pairStarts = append(pairStarts, match[0])
			pairEnds = append(pairEnds, match[1])
			consumedOpeners[match[0]] = struct{}{}
			consumedClosers[match[1]-len(cdataCloser)] = struct{}{}
		}
		surplus := make([]int, 0)
		for _, loc := range cdataOpenerRe.FindAllStringIndex(text, -1) {
			if _, ok := consumedOpeners[loc[0]]; !ok {
				surplus = append(surplus, loc[0])
			}
		}
		if len(surplus) == 0 {
			return 0, 0, false
		}
		orphans := make([]int, 0)
		for _, loc := range cdataCloserRe.FindAllStringIndex(lower, -1) {
			if _, ok := consumedClosers[loc[0]]; !ok {
				orphans = append(orphans, loc[0])
			}
		}
		if len(orphans) == 0 {
			return 0, 0, false
		}
		for i, start := range pairStarts {
			end := pairEnds[i]
			for _, pos := range surplus {
				if start < pos && pos < end {
					return start, lastClose, true
				}
			}
		}
		return 0, 0, false
	}
	// No closer after the first opener (SPEC-10/11): the model omitted "]]>".
	if loc := protoCloseAfterCDATA.FindStringIndex(text[firstOpen:]); loc != nil {
		return firstOpen, firstOpen + loc[0], true
	}
	if firstOpen == 0 {
		return firstOpen, len(text), true
	}
	return 0, 0, false
}

// CDATASections returns opaque (start, end) ranges covering every CDATA
// section. Port of _cdata_sections.
func CDATASections(text string) [][2]int {
	matches := cdataRe.FindAllStringIndex(text, -1)
	sections := make([][2]int, 0, len(matches))
	for _, match := range matches {
		sections = append(sections, [2]int{match[0], match[1]})
	}
	if start, end, ok := ComputeCDATABounds(text); ok {
		sections = [][2]int{{start, end}}
	}
	return sections
}

// ExtractCDATAFromValue extracts CDATA contents from a parameter value. Port of
// _extract_cdata_from_value.
func ExtractCDATAFromValue(raw string) []string {
	matches := cdataRe.FindAllStringSubmatch(raw, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match[1])
	}
	if start, end, ok := ComputeCDATABounds(raw); ok {
		out = []string{raw[start+len("<![CDATA[") : end]}
	}
	return out
}

// stripNestedCDATAMarkers loop-peels remaining CDATA openers and closer
// residue. Port of _strip_nested_cdata_markers; ok=false when the value is not
// CDATA-shaped.
func stripNestedCDATAMarkers(raw string, requireCloser bool) (string, bool) {
	text := raw
	loc := cdataOpenerRe.FindStringIndex(text)
	if text == "" || loc == nil || loc[0] != 0 {
		return "", false
	}
	if requireCloser && !cdataTailRe.MatchString(text) {
		return "", false
	}
	for {
		match := cdataOpenerRe.FindStringIndex(text)
		if match == nil || match[0] != 0 {
			break
		}
		text = text[match[1]:]
	}
	return cdataTailRe.ReplaceAllString(text, ""), true
}
