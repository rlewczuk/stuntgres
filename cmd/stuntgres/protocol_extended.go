package main

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

func (c *client) rejectProtocolCommand(ctx context.Context, err error) error {
	op, _, e := c.session.AcquireCommand(ctx)
	if e != nil {
		return e
	}
	defer op.Done()
	scope, e := c.db.engine.StartProtocolCommand(op, c.cycle)
	if e != nil {
		return op.CommandError(e)
	}
	return op.CommandError(scope.Finish(err))
}
func (c *client) extendedCommand(ctx context.Context, message frontendMessage) (transportErr error) {
	released := false
	release := func() {
		if !released {
			c.releaseMessage(message)
			released = true
		}
	}
	defer release()
	input := protocolInput{body: message.body, encoding: c.wire.encoding, ctx: ctx}
	op, _, err := c.session.AcquireCommand(ctx)
	if err != nil {
		c.protocolState = protocolRecovery
		return c.writeSQLError(ctx, "", err)
	}
	defer op.Done()
	scope, err := c.db.engine.StartProtocolCommand(op, c.cycle)
	if err != nil {
		c.protocolState = protocolRecovery
		return c.writeSQLError(ctx, "", op.CommandError(err))
	}
	var provenance session.ProtocolContext
	var stepName string // statement or portal name, for query.log
	var stepTarget byte // Describe/Close subtype, for query.log
	var response func() error
	var stream *protocolCopy
	switch message.tag {
	case 'P':
		var name, source string
		name, err = input.String()
		if err == nil {
			source, err = input.String()
		}
		provenance.Source, stepName = source, name
		var count uint16
		if err == nil {
			count, err = input.Uint16()
		}
		var hints []uint32
		if err == nil {
			// Count never controls a capacity until all its bytes are present.
			if int(count) > len(input.body)/4 {
				err = protocolError("insufficient data left in message")
			} else {
				hints = make([]uint32, int(count))
				for i := range hints {
					v, _ := input.Int32()
					hints[i] = uint32(v)
				}
				err = input.End()
			}
		}
		if err == nil {
			_, err = scope.ParseProtocol(name, source, hints)
		}
		response = func() error { return c.wire.send('1', nil) }
	case 'B':
		var portal, statement string
		portal, err = input.String()
		if err == nil {
			statement, err = input.String()
		}
		if err == nil {
			provenance, err = scope.BindProtocolPortal(portal, statement, &input)
		}
		stepName = portal
		response = func() error { return c.wire.send('2', nil) }
	case 'D', 'C':
		var subtype byte
		var name string
		subtype, err = input.Byte()
		if err == nil {
			name, err = input.String()
		}
		stepName, stepTarget = name, subtype
		if err == nil {
			err = input.End()
		}
		if err == nil {
			switch subtype {
			case 'S':
				provenance = scope.ProtocolStatementContext(name)
				if message.tag == 'C' {
					err = scope.CloseProtocolStatement(name)
				} else {
					var statement core.ProtocolStatement
					statement, err = scope.DescribeProtocolStatement(name)
					var description []byte
					if err == nil && statement.HasRows {
						description, err = c.descriptionBody(statement.Description.Results, nil)
					}
					if err == nil {
						response = func() error {
							body := appendInt16(nil, int16(len(statement.ParameterOIDs)))
							for _, oid := range statement.ParameterOIDs {
								body = appendInt32(body, oid)
							}
							if e := c.wire.send('t', body); e != nil {
								return e
							}
							if !statement.HasRows {
								return c.wire.send('n', nil)
							}
							return c.wire.send('T', description)
						}
					}
				}
			case 'P':
				provenance = scope.ProtocolPortalContext(name)
				if message.tag == 'C' {
					err = scope.CloseProtocolPortal(name)
				} else {
					var portal core.ProtocolPortalDescription
					portal, err = scope.DescribeProtocolPortal(name)
					var description []byte
					if err == nil && portal.HasRows {
						description, err = c.descriptionBody(portal.Columns, portal.Formats)
					}
					if err == nil {
						response = func() error {
							if !portal.HasRows {
								return c.wire.send('n', nil)
							}
							return c.wire.send('T', description)
						}
					}
				}
			default:
				command := "DESCRIBE"
				if message.tag == 'C' {
					command = "CLOSE"
				}
				err = protocolError(fmt.Sprintf("invalid %s message subtype %d", command, subtype))
			}
		}
		if message.tag == 'C' {
			response = func() error { return c.wire.send('3', nil) }
		}
	case 'E':
		var name string
		var limit int32
		name, err = input.String()
		if err == nil {
			limit, err = input.Int32()
		}
		stepName = name
		if err == nil {
			err = input.End()
		}
		if err == nil {
			provenance = scope.ProtocolPortalContext(name)
			release() // COPY must have both bounded reader buffers available.
			stream = &c.copyStream
			stream.ctx = op.Context()
			defer stream.closeCommand()
			var body []byte
			sink := func(fields []session.ProtocolField) error {
				if e := op.Context().Err(); e != nil {
					return e
				}
				if logical, socketErr := c.writeNotices(op); logical != nil {
					return logical
				} else if socketErr != nil {
					transportErr = socketErr
					return socketErr
				}
				logicalErr, socketErr := c.writeFields(fields, &body)
				if socketErr != nil {
					transportErr = socketErr
				}
				if logicalErr != nil {
					return logicalErr
				}
				return socketErr
			}
			var execution core.ProtocolExecution
			execution, err = scope.ExecuteProtocolPortal(name, uint32(max(limit, 0)), sink, stream, stream, c.copyHooks)
			response = func() error {
				if e := stream.complete(); e != nil {
					return e
				}
				if execution.Empty {
					return c.wire.send('I', nil)
				}
				if execution.Suspended {
					return c.wire.send('s', nil)
				}
				body, err := c.wire.appendText(ctx, nil, execution.CommandTag)
				if err != nil {
					return err
				}
				return c.wire.send('C', body)
			}
		}
	}
	c.capture.detail(message.tag, stepTarget, stepName, provenance)
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
	if stream != nil && stream.transportErr != nil {
		return stream.transportErr
	}
	if transportErr != nil {
		return transportErr
	}
	if c.ctx.Err() != nil {
		return nil
	}
	for _, notice := range op.DetachNotices() {
		if e := c.wire.notice(notice); e != nil {
			return e
		}
	}
	if err != nil {
		c.protocolState = protocolRecovery
		if e := c.writeContextError(ctx, provenance, err); e != nil {
			return e
		}
		return c.wire.flush()
	}
	c.protocolState = protocolExtended
	if response != nil {
		return response()
	}
	return nil
}
func (c *client) functionCommand(ctx context.Context, input *protocolInput) error {
	op, _, err := c.session.AcquireCommand(ctx)
	if err != nil {
		if e := c.writeSQLError(ctx, "", err); e != nil {
			return e
		}
		return c.readyBoundary()
	}
	defer op.Done()
	scope, err := c.db.engine.StartStatement(op)
	var field session.ProtocolField
	var transportErr error
	if err == nil {
		field, err = scope.CallProtocolFunction(input)
		if logical, socketErr := c.writeActiveNotices(scope); socketErr != nil {
			transportErr = socketErr
			if err == nil {
				err = socketErr
			}
		} else if err == nil {
			err = logical
		}
		c.refreshEncoding()
		err = scope.Finish(err)
	}
	err = op.CommandError(err)
	if transportErr != nil {
		return transportErr
	}
	for _, notice := range op.DetachNotices() {
		if e := c.wire.notice(notice); e != nil {
			return e
		}
	}
	if c.ctx.Err() != nil {
		return nil
	}
	if err != nil {
		if e := c.writeSQLError(ctx, "", err); e != nil {
			return e
		}
	} else {
		body := appendInt32(nil, math.MaxUint32)
		if !field.Null {
			body = appendInt32(body[:0], uint32(len(field.Bytes)))
			body = append(body, field.Bytes...)
		}
		if e := c.wire.send('V', body); e != nil {
			return e
		}
	}
	return c.readyBoundary()
}

// Ensure the receiver contract stays mechanical, without importing transport
// ownership into engine or introducing a second parameter conversion registry.
var _ session.ProtocolInput = (*protocolInput)(nil)
