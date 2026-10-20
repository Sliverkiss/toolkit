// Package stream ports app/stream/: SSE reassembly of tool calls for the OpenAI
// and Responses surfaces.
package stream

import (
	"io"
	"strings"

	"github.com/Sliverkiss/toolkit/util"
)

// SliceSource is a LineSource over a fixed slice (tests and buffered replay).
type SliceSource struct {
	Lines []string
	idx   int
}

// Next returns the next line or io.EOF.
func (s *SliceSource) Next() (string, error) {
	if s.idx >= len(s.Lines) {
		return "", io.EOF
	}
	line := s.Lines[s.idx]
	s.idx++
	return line, nil
}

// eventIterator walks SSE events one at a time (the Go equivalent of Python's
// async generator over iter_sse_events).
type eventIterator struct {
	parser *util.SSEParser
	source util.LineSource
	done   bool
}

func newEventIterator(source util.LineSource) *eventIterator {
	return &eventIterator{parser: util.NewSSEParser(), source: source}
}

// Next returns the next event; ok=false at end of stream. Errors surface as the
// error return (used for the [stream error: …] frame).
func (it *eventIterator) Next() (util.SSEEvent, bool, error) {
	for {
		if it.done {
			return util.SSEEvent{}, false, nil
		}
		raw, err := it.source.Next()
		if err != nil {
			if err == io.EOF {
				it.done = true
				if tail := it.parser.Flush(); tail != nil {
					return *tail, true, nil
				}
				return util.SSEEvent{}, false, nil
			}
			return util.SSEEvent{}, false, err
		}
		line := strings.TrimRight(raw, "\r")
		if event := it.parser.FeedLine(line); event != nil {
			return *event, true, nil
		}
	}
}

// Collector accumulates emitted SSE frames for tests and buffered callers.
type Collector struct {
	Frames []string
}

// Emit appends one frame, propagating an error if the sink is closed.
func (c *Collector) Emit(frame string) error {
	c.Frames = append(c.Frames, frame)
	return nil
}

// Joined returns all frames concatenated.
func (c *Collector) Joined() string { return strings.Join(c.Frames, "") }
