# toolkit

Function-calling middleware for any LLM API — as an embeddable Go library.

`toolkit` gives any OpenAI-compatible upstream (your gateway, a third-party reverse proxy, a
local model server) **reliable tool calling**, even when the upstream has no native function
calling at all. It injects a compact XYML protocol block into the system prompt, parses the
model's textual output back into standard `tool_calls`, sanitizes protocol leaks, and
reassembles streamed tool-call fragments — byte-accurate, in pure Go.

The client never notices: it speaks plain OpenAI Chat Completions (tools in, `tool_calls` out,
`role:"tool"` back in). The upstream never notices either.

## Packages

| Package | Purpose |
|---|---|
| `models` | Canonical internal types: messages, tool definitions, tool calls |
| `jsonx` | Tolerant JSON handling for arbitrary client payloads |
| `util` | Token estimation, id generation, SSE helpers, cache, metrics |
| `config` | YAML config with `${ENV}` interpolation |
| `engine` | The XYML core: grammar, parser, renderer, sieve (CDATA nesting, fused tags, close-tag specificity) |
| `fc` | Prompt injection, text→tool_calls parsing, leak sanitization, malformed-output recovery, mode policy |
| `convert` | Protocol conversion (OpenAI Chat ⇄ Responses API) |
| `reqlog` | Request logging helpers |
| `stream` | Streaming reassembly of tool-call fragments for both protocols |
| `adapters` | Client-protocol adapter types |

## Quick start

```go
import (
    "github.com/Sliverkiss/toolkit/fc"
    "github.com/Sliverkiss/toolkit/models"
)

// 1. Inject: merge an XYML tool-protocol block into the message list
msgs := fc.InjectPromptMessages(messages, tools, "openai", true)

// 2. Send msgs to your upstream (any HTTP client) ...

// 3. Parse the model's text back into standard tool calls
calls, err := fc.ParseTextToCalls(modelText, tools, "openai", true)
```

Streaming: feed each text delta to `stream.NewReassembler`; it holds partial protocol
fragments, emits safe content incrementally, and flushes complete tool calls at end of stream.
The sieve guarantees no XYML/DSML markup ever leaks to your client.

## How it works

```
Client (OpenAI SDK)          toolkit                        Upstream (no native FC)
   tools / tool_choice ──►  inject XYML system block ──►  plain prompt
   ◄── tool_calls      ◄──  parse + sanitize + reassemble ◄── text stream
   role:"tool" results ──►  render [Tool Result] blocks ──► plain prompt
```

- `tool_choice` fully supported: `auto`, `none` (no injection), `required`, named function.
- Unknown tool names in `tool_choice` are rejected early.
- Protocol text that cannot be parsed is stripped, never leaked.
- Streaming order preserved: role → content* → tool_calls → finish_reason → usage.

## Installation

```
go get github.com/Sliverkiss/toolkit
```

Requires Go 1.22+. Single external dependency: `gopkg.in/yaml.v3`.

## License

CC BY-NC-SA 4.0 — see [LICENSE](LICENSE). **Commercial use is prohibited.**
