package main

import (
	"errors"
	"log/slog"
	"net"
	"stuntgres/internal/engine"
)

type protocolState uint8

const (
	protocolNormal protocolState = iota
	protocolExtended
	protocolCopyIn
	protocolCopyOut
	protocolRecovery
)

func (s *server) serve() error {
	for {
		socket, err := s.listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return s.runtimeError()
			}
			s.fail(err)
			return err
		}
		c, err := s.newClient(socket)
		if err != nil {
			_ = socket.Close()
			if s.ctx.Err() != nil {
				return s.runtimeError()
			}
			s.fail(err)
			return err
		}
		go c.run()
	}
}
func (c *client) run() {
	reason := engine.InvalidConnection
	defer func() { c.finish(reason) }()
	c.server.log(slog.LevelInfo, "connection opened", "connection_id", c.id)
	admitted, err := c.startup()
	if err != nil {
		_ = c.wire.errorResponse(err, true)
		_ = c.wire.flush()
		reason = engine.InitFailure
		return
	}
	if !admitted {
		reason = engine.CleanClose
		return
	}
	c.cycle = c.session.NewProtocolCycle()
	c.stopPeerWatch, err = watchPeerShutdown(c.socket, func() { c.cancel(); _ = c.socket.Close() }, func(e error) {
		c.server.log(slog.LevelError, "peer shutdown monitor failed", "connection_id", c.id, "error", e.Error())
	})
	if err != nil {
		if !errors.Is(err, errPeerWatchUnsupported) {
			c.fatal(err)
			return
		}
		c.server.log(slog.LevelWarn, "passive peer shutdown monitor unavailable", "error", err.Error())
	}
	c.messages, c.sessionEnded = c.startReader(), c.watchSessionEnd()
	for {
		wake, err := c.drainNotifications()
		if err != nil {
			if !c.fatalSent {
				_ = c.writeSQLError(c.ctx, "", err)
			}
			return
		}
		var m frontendMessage
		select {
		case terminal := <-c.sessionEnded:
			reason = engine.Terminated
			if !c.fatalSent {
				_ = c.writeSQLError(c.ctx, "", terminal)
			}
			return
		case <-wake:
			if err = c.wire.flush(); err != nil {
				return
			}
			continue
		case current, open := <-c.messages:
			if !open {
				if c.readFailure != nil {
					c.fatal(c.readFailure)
				}
				return
			}
			m = current
		}
		if c.ctx.Err() != nil {
			c.releaseMessage(m)
			continue
		}
		if m.tag == 'X' {
			c.releaseMessage(m)
			reason = engine.CleanClose
			return
		}
		if c.protocolState == protocolRecovery && m.tag != 'S' {
			c.releaseMessage(m)
			continue
		}
		ctx, done := c.beginCommand()
		switch m.tag {
		case 'Q':
			input := protocolInput{body: m.body}
			source, e := input.String()
			if e == nil {
				e = input.End()
			}
			c.releaseMessage(m)
			if e != nil {
				e = c.rejectCommand(e)
				err = c.writeSQLError(ctx, source, e)
				if err == nil {
					err = c.readyBoundary()
				}
			} else {
				err = c.executeSimple(ctx, source)
			}
		case 'P', 'B', 'D', 'E', 'C':
			c.protocolState = protocolExtended
			err = c.extendedCommand(ctx, m)
		case 'S':
			c.protocolState = protocolNormal
			input := protocolInput{body: m.body}
			e := input.End()
			op, _, admitErr := c.session.AcquireCommand(ctx)
			if admitErr != nil {
				e = admitErr
			} else {
				if e != nil {
					scope, se := c.db.engine.StartProtocolCommand(op, c.cycle)
					if se == nil {
						e = scope.Finish(e)
					} else {
						e = se
					}
				}
				syncErr := c.db.engine.SyncProtocolCycle(op, c.cycle)
				e = op.CommandError(errors.Join(e, syncErr))
				op.Done()
			}
			c.releaseMessage(m)
			if e != nil {
				err = c.writeSQLError(ctx, "", e)
			}
			if err == nil {
				err = c.readyBoundary()
			}
		case 'H':
			input := protocolInput{body: m.body}
			e := input.End()
			c.releaseMessage(m)
			if e != nil {
				e = c.rejectProtocolCommand(ctx, e)
				err = c.writeSQLError(ctx, "", e)
				c.protocolState = protocolRecovery
			}
			if err == nil {
				err = c.wire.flush()
			}
		case 'F':
			err = c.functionCommand(ctx, &protocolInput{body: m.body})
			c.releaseMessage(m)
		case 'd', 'c', 'f':
			c.releaseMessage(m)
		default:
			c.releaseMessage(m)
			c.fatal(framingError("08P01", "invalid frontend message type"))
			done()
			return
		}
		done()
		if err != nil {
			if diagnostic(err).Severity == "FATAL" {
				reason = engine.Terminated
				if !c.fatalSent {
					_ = c.writeSQLError(c.ctx, "", err)
				}
			}
			return
		}
	}
}
func (c *client) fatal(err error) {
	if !c.fatalSent {
		_ = c.wire.errorResponse(err, true)
		_ = c.wire.flush()
		c.fatalSent = true
	}
}
func (c *client) rejectCommand(err error) error {
	op, _, e := c.session.AcquireCommand(c.ctx)
	if e != nil {
		return e
	}
	defer op.Done()
	scope, e := c.db.engine.StartStatement(op)
	if e != nil {
		return op.CommandError(e)
	}
	return op.CommandError(scope.Finish(err))
}
func knownFrontendTag(tag byte) bool {
	switch tag {
	case 'Q', 'P', 'B', 'D', 'E', 'C', 'S', 'H', 'F', 'd', 'c', 'f':
		return true
	}
	return false
}
func (c *client) readyBoundary() error {
	if c.ctx.Err() != nil {
		return nil
	}
	if err := c.sendSettings(c.settingsSnapshot()); err != nil {
		return err
	}
	if _, err := c.drainNotifications(); err != nil {
		return err
	}
	if err := c.sendReady(); err != nil {
		return err
	}
	return c.wire.flush()
}
func (c *client) drainNotifications() (<-chan struct{}, error) {
	if c.ctx.Err() != nil || c.session.TransactionState() != engine.TxIdle {
		return nil, nil
	}
	for {
		event, available, wake, err := c.session.PollProtocolNotification()
		if err != nil {
			return nil, err
		}
		if !available {
			return wake, nil
		}
		body := appendInt32(nil, uint32(event.PID))
		body = appendString(body, event.Channel)
		body = appendString(body, event.Payload)
		if err := c.wire.send('A', body); err != nil {
			return nil, err
		}
		if err := c.wire.flush(); err != nil {
			return nil, err
		}
	}
}
