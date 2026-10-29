package host

import (
	"context"
	"fmt"
)

// invocation is the only owner captured by the program capability lent to a
// handler. Its synchronous lifetime ends when Invoke unwinds, even on panic.
// A retained capability must not retain a SQL scope, settings, frame or actor.
type invocation struct {
	context context.Context
	admit   func() error
	program ProgramFunc
}

func (i *invocation) close() { i.context = nil; i.admit = nil; i.program = nil }
func (i *invocation) enter() error {
	if i.program == nil {
		return fmt.Errorf("stuntgres: host invocation has ended")
	}
	if err := i.context.Err(); err != nil {
		return err
	}
	if i.admit != nil {
		return i.admit()
	}
	return nil
}
func (i *invocation) run(ctx context.Context, args []Object) (Object, error) {
	if err := i.enter(); err != nil {
		return Object{}, err
	}
	if ctx == nil {
		return Object{}, fmt.Errorf("stuntgres: nil host program context")
	}
	result, err := i.program(ctx, args)
	if admissionErr := i.enter(); admissionErr != nil {
		return Object{}, admissionErr
	}
	return result, err
}
