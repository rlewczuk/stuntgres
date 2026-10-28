package main

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Simple batches retain the existing engine message clock/transaction owner.
// Only detached output or canonical unlocked callbacks may touch the socket.
func (c *client) executeSimple(ctx context.Context, source string) (transportErr error) {
	op, options, err := c.session.AcquireCommand(ctx)
	if err != nil {
		if e := c.writeSQLError(ctx, source, err); e != nil {
			return e
		}
		return c.readyBoundary()
	}
	defer op.Done()
	op.OriginalSQL, op.SQLAvailable = source, true
	scope, err := c.db.engine.StartStatement(op)
	if err != nil {
		if e := c.writeSQLError(ctx, source, op.CommandError(err)); e != nil {
			return e
		}
		return c.readyBoundary()
	}
	batch, finished := false, false
	defer func() {
		if !finished {
			if batch {
				_ = scope.FinishMessage(errors.New("frontend output interrupted"))
			} else {
				_ = scope.Finish(errors.New("frontend output interrupted"))
			}
		}
	}()
	err = scope.InvalidateUnnamedProtocolStatement()
	var statements []parser.Statement
	if err == nil {
		statements, err = parser.ParseWithOptions(source, options)
	}
	if err == nil {
		scope.ProtocolSimpleQuery(len(statements))
	}
	if err == nil && len(statements) > 1 {
		err = scope.RetainMessage()
		batch = err == nil
	}
	var buffer []byte
	stream := &c.copyStream
	stream.ctx = op.Context()
	defer stream.closeCommand()
	for i, statement := range statements {
		if err != nil {
			break
		}
		if i != 0 {
			var next *core.StatementScope
			next, err = scope.NextStatement()
			if err != nil {
				break
			}
			scope = next
		}
		var result core.Result
		describe := func(columns []catalog.Column) error {
			if e := op.Context().Err(); e != nil {
				return e
			}
			if logical, socketErr := c.writeNotices(op); logical != nil {
				return logical
			} else if socketErr != nil {
				transportErr = socketErr
				return socketErr
			}
			e := c.writeDescription(columns, nil)
			if e != nil {
				var logical *sqlstate.Error
				if !errors.As(e, &logical) {
					transportErr = e
				}
			}
			return e
		}
		row := func(fields []session.ProtocolField) error {
			if e := op.Context().Err(); e != nil {
				return e
			}
			logicalErr, socketErr := c.writeFields(fields, &buffer)
			if socketErr != nil {
				transportErr = socketErr
			}
			if logicalErr != nil {
				return logicalErr
			}
			return socketErr
		}
		result, err = scope.ExecuteProtocolSimple(statement, describe, row, stream, stream, c.copyHooks)
		stream.release()
		if logical, socketErr := c.writeActiveNotices(scope); socketErr != nil {
			transportErr = socketErr
			if err == nil {
				err = socketErr
			}
		} else if err == nil {
			err = logical
		}
		c.refreshEncoding()
		err = op.CommandError(scope.Finish(err))
		if !batch {
			finished = true
		}
		if stream.transportErr != nil {
			return stream.transportErr
		}
		if transportErr != nil {
			return transportErr
		}
		if c.ctx.Err() != nil {
			return nil
		}
		notices := op.DetachNotices()
		outputFailed := false
		output := func() error {
			for _, notice := range notices {
				if e := c.wire.notice(notice); e != nil {
					return e
				}
			}
			if err != nil {
				return nil
			}
			if e := op.Context().Err(); e != nil {
				err = e
				outputFailed = true
				return nil
			}
			if e := stream.complete(); e != nil {
				return e
			}
			body, encodeErr := c.wire.appendText(ctx, buffer[:0], result.CommandTag)
			if encodeErr != nil {
				return encodeErr
			}
			return c.wire.send('C', body)
		}
		if batch {
			var callbackErr error
			hookErr := scope.ProtocolOutput(func() error { callbackErr = output(); return callbackErr })
			if callbackErr != nil {
				return callbackErr
			}
			if hookErr != nil {
				err = errors.Join(err, hookErr)
			}
		} else {
			if e := output(); e != nil {
				return e
			}
			if outputFailed {
				err = c.rejectCommand(err)
			}
		}
		if err != nil {
			break
		}
	}
	if batch {
		err = scope.FinishMessage(err)
		finished = true
	} else if !finished {
		err = scope.Finish(err)
		finished = true
	}
	err = op.CommandError(err)
	c.protocolState = protocolNormal
	for _, notice := range op.DetachNotices() {
		if e := c.wire.notice(notice); e != nil {
			return e
		}
	}
	if c.ctx.Err() != nil {
		return nil
	}
	if err != nil {
		if e := c.writeSQLError(ctx, source, err); e != nil {
			return e
		}
	} else if len(statements) == 0 {
		if e := c.wire.send('I', nil); e != nil {
			return e
		}
	}
	return c.readyBoundary()
}
