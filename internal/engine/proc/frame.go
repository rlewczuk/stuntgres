package proc

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// Frame belongs to one invocation. sqltype.Value owns immutable payloads, so
// returning a Value detaches from the slots without copying scalar/tree data.
type Frame struct {
	Parameters []sqltype.Value
	inline     [4]sqltype.Value
	result     sqltype.Value
	closed     bool
}

func NewFrame(inputs []sqltype.Usage) *Frame {
	f := &Frame{}
	if len(inputs) <= len(f.inline) {
		f.Parameters = f.inline[:len(inputs)]
	} else {
		f.Parameters = make([]sqltype.Value, len(inputs))
	}
	return f
}
func (f *Frame) Bind(index int, value sqltype.Value, usage sqltype.Usage) error {
	if f.closed || index < 0 || index >= len(f.Parameters) || value.Type() != usage.Type {
		return fmt.Errorf("invalid SQL routine argument frame")
	}
	f.Parameters[index] = value
	return nil
}
func (f *Frame) Result(value sqltype.Value, usage sqltype.Usage) (sqltype.Value, error) {
	if f.closed || value.Type() != usage.Type {
		return sqltype.Value{}, fmt.Errorf("invalid SQL routine result frame")
	}
	f.result = value
	return f.result, nil
}
func (f *Frame) Close() {
	if f.closed {
		return
	}
	clear(f.Parameters)
	f.Parameters = nil
	f.result = sqltype.Value{}
	f.closed = true
}
