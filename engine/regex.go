package engine

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Static patterns — direct translations of the Python module-level regexes.
// Where Python used look-around or backreferences, a small scanner is provided
// below instead (see DESIGN.md §4.2).
var (
	attrRe        = regexp.MustCompile(`(?is)\b(?:name|parameter)\s*=\s*(?:"([^"]*)"|'([^']*)'|([A-Za-z0-9_.:-]+))`)
	cdataOpenerRe = regexp.MustCompile(`(?i)<!\[CDATA[\[|]`)
	cdataRe       = regexp.MustCompile(`(?i)<!\[CDATA[\[|]([\s\S]*?)\]\]>`)

	fusedTagRe = regexp.MustCompile(`(?is)(</?)(xyml|dsml|qnml)([A-Za-z0-9_]*?)(tool_calls|invoke|parameter|tool)(\s[^>]*>|>)`)

	protoCloseAfterCDATA = regexp.MustCompile(`(?i)</[|:]+[A-Za-z_][^>]*>`)

	xmlToolCallRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<tool_use\b([^>]*)>([\s\S]*?)</tool_use>`),
		regexp.MustCompile(`(?i)<tool_call\b([^>]*)>([\s\S]*?)</tool_call>`),
		regexp.MustCompile(`(?i)<function\b([^>]*)>([\s\S]*?)</function>`),
		regexp.MustCompile(`(?i)<invoke\b([^>]*)>([\s\S]*?)</invoke>`),
	}

	textKVLineRe     = regexp.MustCompile(`(?s)^([A-Za-z_.-][A-Za-z0-9_.-]*)\s*:\s*(.*)$`)
	xmlOpenTagRe     = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_.:-]*)\b[^>]*>`)
	pathPollutionRe  = regexp.MustCompile(`[\r\n<>]`)
	nameAttrRe       = regexp.MustCompile(`(?s)(?:^|[\s|])name\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s|/>]+))`)
	jsonToolMarkerRe = regexp.MustCompile(`(?is)^\s*\{\s*"tool_calls"`)
	funcNameMarkerRe = regexp.MustCompile(`(?is)function\.name\s*:`)
	structCloseRe    = regexp.MustCompile(`\n\s*[\]}]\s*$`)
	mdFenceRe        = regexp.MustCompile("```[a-zA-Z0-9_-]*\\s*([\\s\\S]*?)```")
	jsonFenceRe      = regexp.MustCompile("(?i)```(?:json)?\\s*([\\s\\S]*?)```")
	repairNameEqRe   = regexp.MustCompile(`(?is)"name="\s*`)
	repairNameEq2Re  = regexp.MustCompile(`(?is)"name=([^",}\s]+)"`)
	repairKeyEqRe    = regexp.MustCompile(`(?is)"(name|input|arguments|args|parameters|tool|tool_name|function_name)"\s*=\s*`)
	repairKeyColonRe = regexp.MustCompile(`(?is)([{,]\s*)(name|input|arguments|args|parameters|tool|tool_name|function_name)\s*:`)
	aliasKeyRe       = regexp.MustCompile(`[^a-z0-9]+`)

	looseTagMarkerRe = regexp.MustCompile(`(?i)<\s*[^>]*[|:][^>]*\b(?:tool_calls|invoke|parameter)\b[^>]*>` +
		`|` + `<\s*[^>]*[|:][^>]*\bname\s*=[^>]*>`)

	collapsedOpenRe = regexp.MustCompile(`(?is)<([|:]{1,2})([A-Za-z_][A-Za-z0-9_]*)([|:]{1,2})\s+name\s*=\s*["']?(\w+)["']?[^>]*>`)

	structOrWordRe = regexp.MustCompile(`[A-Za-z0-9_]+`)
)

// Dynamic protocol regexes. Python caches these with functools.lru_cache keyed
// by (protocol, tag); a sync.Map gives the same one-time compilation.
var (
	openTagCache  sync.Map // tag -> *regexp.Regexp
	anyOpenCache  sync.Map // root\x00invoke\x00parameter -> *regexp.Regexp
	anyCloseCache sync.Map
	nameTagCache  sync.Map // protocol -> *regexp.Regexp
	bareCache     sync.Map // protocol -> *regexp.Regexp
)

// protocolOpenTagRe ports _protocol_open_tag_re. The protocol name is
// intentionally not part of the pattern: Python widened it to any identifier
// and constrains the tag word instead.
func protocolOpenTagRe(tag string) *regexp.Regexp {
	if cached, ok := openTagCache.Load(tag); ok {
		return cached.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`(?i)<\s*(?:[|:]{1,2})?\s*(?:[A-Za-z0-9_]+[|:])?\s*[A-Za-z0-9_]+\s*(?:[|:]{1,2}|\s+)\s*` +
		regexp.QuoteMeta(tag) + `\b[^>]*>`)
	openTagCache.Store(tag, re)
	return re
}

// protocolAnyOpenTagRe ports _protocol_any_open_tag_re.
func protocolAnyOpenTagRe(root, invoke, parameter string) *regexp.Regexp {
	key := root + "\x00" + invoke + "\x00" + parameter
	if cached, ok := anyOpenCache.Load(key); ok {
		return cached.(*regexp.Regexp)
	}
	words := []string{root, invoke, parameter}
	sort.SliceStable(words, func(i, j int) bool { return len(words[i]) > len(words[j]) })
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = regexp.QuoteMeta(word)
	}
	re := regexp.MustCompile(`(?i)<\s*(?:[|:]{1,2})?\s*(?:[A-Za-z0-9_]+[|:])?\s*[A-Za-z0-9_]+\s*[|:]{1,2}\s*(?P<tag>` +
		strings.Join(quoted, "|") + `|[A-Za-z_][A-Za-z0-9_.:-]*)\b(?P<attrs>[^>]*)>`)
	anyOpenCache.Store(key, re)
	return re
}

// protocolAnyCloseTagRe ports _protocol_any_close_tag_re (SPEC-21: at least one
// | or : separator is required).
func protocolAnyCloseTagRe(root, invoke, parameter string) *regexp.Regexp {
	key := root + "\x00" + invoke + "\x00" + parameter
	if cached, ok := anyCloseCache.Load(key); ok {
		return cached.(*regexp.Regexp)
	}
	words := []string{root, invoke, parameter}
	sort.SliceStable(words, func(i, j int) bool { return len(words[i]) > len(words[j]) })
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = regexp.QuoteMeta(word)
	}
	re := regexp.MustCompile(`(?i)<\s*/\s*(?:` +
		`[|:]{1,2}\s*[A-Za-z0-9_]+\s*[|:]{1,2}\s*` +
		`|` +
		`[A-Za-z0-9_]+\s*[|:]{1,2}\s*` +
		`|` +
		`[|:]{1,2}\s*[A-Za-z0-9_]+\s*` +
		`)(?:` + strings.Join(quoted, "|") + `|[A-Za-z_][A-Za-z0-9_.:-]*)?` +
		`\s*>`)
	anyCloseCache.Store(key, re)
	return re
}

// parameterNameTagRe ports the open half of _parameter_name_tag_re. The close
// half needs the captured name as a backreference, so it is built per match in
// findParameterNameTags.
func parameterNameTagRe(protocol string) *regexp.Regexp {
	if cached, ok := nameTagCache.Load(protocol); ok {
		return cached.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`(?is)<\s*(?:[|:]{1,2})?\s*(?:[A-Za-z0-9_]+[|:])?\s*[A-Za-z0-9_]+\s*(?:[|:]{1,2}|\s+)\s*(?P<name>[A-Za-z_][A-Za-z0-9_.:-]*)\b[^>]*>`)
	nameTagCache.Store(protocol, re)
	return re
}

// bareProtocolMarkerRe ports _bare_protocol_marker_re (protocol name stays a
// literal, which keeps plain prose from being captured).
func bareProtocolMarkerRe(protocol string) *regexp.Regexp {
	if cached, ok := bareCache.Load(protocol); ok {
		return cached.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`(?i)<\s*(?:[|:]{1,2})?\s*(?:[A-Za-z0-9_]+[|:])?\s*` +
		regexp.QuoteMeta(protocol) + `\s*(?:[|:]{1,2})?\s*>`)
	bareCache.Store(protocol, re)
	return re
}

// xmlTagBody is one `<name>body</name>` match.
type xmlTagBody struct {
	Name  string
	Body  string
	Start int
	End   int
}

// findXMLTagBodies ports _XML_TAG_BODY_RE.finditer, including Python's
// non-overlapping scan with a backreferenced close tag.
func findXMLTagBodies(text string) []xmlTagBody {
	var out []xmlTagBody
	offset := 0
	for offset <= len(text) {
		locs := xmlOpenTagRe.FindStringSubmatchIndex(text[offset:])
		if locs == nil {
			break
		}
		name := text[offset+locs[2] : offset+locs[3]]
		openEnd := offset + locs[1]
		closeTag := "</" + name + ">"
		closeStart := strings.Index(text[openEnd:], closeTag)
		if closeStart < 0 {
			// Python retries at offset+1 when the whole match fails.
			offset += locs[0] + 1
			continue
		}
		closeStart += openEnd
		end := closeStart + len(closeTag)
		out = append(out, xmlTagBody{
			Name:  name,
			Body:  text[openEnd:closeStart],
			Start: offset + locs[0],
			End:   end,
		})
		offset = end
	}
	return out
}

// parameterNameTag is one leaked-name parameter match.
type parameterNameTag struct {
	Name string
	Val  string
}

// findParameterNameTags ports _parameter_name_tag_re.finditer. The close tag is
// either the literal `parameter` or the leaked name itself.
func findParameterNameTags(text, protocol string) []parameterNameTag {
	openRe := parameterNameTagRe(protocol)
	var out []parameterNameTag
	offset := 0
	for offset <= len(text) {
		locs := openRe.FindStringSubmatchIndex(text[offset:])
		if locs == nil {
			break
		}
		name := text[offset+locs[2] : offset+locs[3]]
		openEnd := offset + locs[1]
		closeRe := regexp.MustCompile(`(?is)</\s*(?:[|:]{1,2})?\s*(?:[A-Za-z0-9_]+[|:])?\s*[A-Za-z0-9_]+\s*(?:[|:]{1,2}|\s+)\s*(?:parameter|` +
			regexp.QuoteMeta(name) + `)\s*>`)
		closeLoc := closeRe.FindStringIndex(text[openEnd:])
		if closeLoc == nil {
			offset += locs[0] + 1
			continue
		}
		val := text[openEnd : openEnd+closeLoc[0]]
		out = append(out, parameterNameTag{
			Name: name,
			Val:  strings.Trim(val, " \t\n\r\f\v"),
		})
		offset = openEnd + closeLoc[1]
	}
	return out
}

// collapsedParam is one collapsed protocol marker match.
type collapsedParam struct {
	Protocol string
	Name     string
	Value    string
	Start    int
	End      int
}

// findCollapsedParams ports _COLLAPSED_PARAM_RE.finditer.
func findCollapsedParams(text string) []collapsedParam {
	var out []collapsedParam
	offset := 0
	for offset <= len(text) {
		locs := collapsedOpenRe.FindStringSubmatchIndex(text[offset:])
		if locs == nil {
			break
		}
		delim := text[offset+locs[2] : offset+locs[3]]
		protocol := text[offset+locs[4] : offset+locs[5]]
		closeDelim := text[offset+locs[6] : offset+locs[7]]
		name := text[offset+locs[8] : offset+locs[9]]
		if delim != closeDelim {
			offset += locs[0] + 1
			continue
		}
		openEnd := offset + locs[1]
		closeTag := "</" + delim + protocol + delim
		idx := strings.Index(strings.ToLower(text[openEnd:]), strings.ToLower(closeTag))
		if idx < 0 {
			offset += locs[0] + 1
			continue
		}
		closeStart := openEnd + idx
		rest := closeStart + len(closeTag)
		// Python allows optional whitespace then `>`.
		for rest < len(text) && (text[rest] == ' ' || text[rest] == '\t' || text[rest] == '\n' || text[rest] == '\r' || text[rest] == '\f' || text[rest] == '\v') {
			rest++
		}
		if rest >= len(text) || text[rest] != '>' {
			offset += locs[0] + 1
			continue
		}
		out = append(out, collapsedParam{
			Protocol: protocol,
			Name:     name,
			Value:    text[openEnd:closeStart],
			Start:    offset + locs[0],
			End:      rest + 1,
		})
		offset = rest + 1
	}
	return out
}

// stripColonMarkerResidue ports _COLON_MARKER_RESIDUE_RE.sub("", …): only
// standalone `<:>` lines are removed (see DESIGN.md §4.2 for the scanner).
func stripColonMarkerResidue(text string) string {
	var b strings.Builder
	i := 0
	for i < len(text) {
		idx := strings.Index(text[i:], "<:>")
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		pos := i + idx
		lineStart := pos
		for lineStart > i && (text[lineStart-1] == ' ' || text[lineStart-1] == '\t') {
			lineStart--
		}
		atLineStart := lineStart == 0 || text[lineStart-1] == '\n'
		after := pos + 3
		for after < len(text) && (text[after] == ' ' || text[after] == '\t') {
			after++
		}
		atLineEnd := after == len(text) || text[after] == '\n'
		if !atLineStart || !atLineEnd {
			b.WriteString(text[i : pos+3])
			i = pos + 3
			continue
		}
		removeFrom := pos
		if lineStart > 0 && text[lineStart-1] == '\n' {
			removeFrom = lineStart - 1
		}
		b.WriteString(text[i:removeFrom])
		i = after
	}
	return b.String()
}

// compact strips whitespace used by the scanner helpers.
func compact(value string) string { return strings.TrimSpace(value) }
