package engine

import ()

// Engine is the ergonomic facade over the XYML parser/renderer (the Go
// replacement for the deleted Python ToolCallEngine class).
type Engine struct {
	Config *ToolCallConfig
}

// New builds an Engine from options.
func New(opts Options) *Engine { return &Engine{Config: NewConfig(opts)} }

// NewWithConfig builds an Engine around an existing config.
func NewWithConfig(config *ToolCallConfig) *Engine {
	if config == nil {
		config = DefaultConfig()
	}
	return &Engine{Config: config}
}

// Parse extracts tool calls from model text.
func (e *Engine) Parse(text string, tools any) ([]ParsedToolCall, error) {
	return ParseToolCalls(text, tools, e.Config)
}

// Render renders one tool call in the configured protocol.
func (e *Engine) Render(name string, input any) string {
	return RenderToolCall(name, input, e.Config, nil)
}

// Instructions renders the injected protocol instruction block.
func (e *Engine) Instructions(tools any) string {
	return BuildToolInstructions(tools, e.Config, nil)
}

// Sieve creates a streaming sieve with the engine's configuration.
func (e *Engine) Sieve(tools any, holdLength int) *ToolSieve {
	return NewToolSieve(tools, e.Config, holdLength)
}

// Coerce applies schema coercion to a tool input.
func (e *Engine) Coerce(name string, input any, tools any) any {
	return CoerceToolInput(name, input, tools, e.Config)
}
