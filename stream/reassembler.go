package stream

import (
	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/models"
)

// Reassembler is the requested public streaming facade. It owns one ToolSieve
// and turns fragmented model text into content events and normalized tool calls,
// exactly like the Python ToolSieve used by the prompt-FC adapters.
type Reassembler struct {
	Sieve *engine.ToolSieve
}

// NewReassembler builds a reassembler for a tool list and protocol.
func NewReassembler(tools []models.ToolDef, protocol string, holdLength int) *Reassembler {
	return &Reassembler{Sieve: engine.NewToolSieve(toolSet(tools), engine.ForPrompt(protocol, ""), holdLength)}
}

// Feed processes one text fragment.
func (r *Reassembler) Feed(chunk string) []engine.SieveEvent { return r.Sieve.ProcessChunk(chunk) }

// Flush emits the trailing capture.
func (r *Reassembler) Flush() []engine.SieveEvent { return r.Sieve.Flush() }

// Calls converts a tool_calls event's parsed calls into canonical ToolCalls.
func Calls(event engine.SieveEvent) []models.ToolCall {
	out := make([]models.ToolCall, 0, len(event.Calls))
	for _, call := range event.Calls {
		arguments, ok := call.Input.(map[string]any)
		if !ok {
			if call.Input == nil {
				arguments = map[string]any{}
			} else {
				arguments = map[string]any{"value": call.Input}
			}
		}
		out = append(out, models.ToolCall{ID: call.ID, Name: call.Name, Arguments: arguments})
	}
	return out
}

func toolSet(tools []models.ToolDef) []map[string]any {
	return engine.NormalizeTools(models.ToolDefsToOpenAI(tools))
}
