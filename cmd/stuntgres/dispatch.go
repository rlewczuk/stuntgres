package main

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"net"

	"stuntgres/internal/engine"
	"stuntgres/internal/sqlstate"
)

const extendedUnsupported = "extended query protocol is not supported by stuntgres Phase 1"

func protocolError(message string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "08P01", Message: message}
}
func unsupported(message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: message}
}

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
	c.stopPeerWatch, err = watchPeerShutdown(c.socket, func() {
		c.cancel()
		_ = c.socket.Close()
	}, func(watchErr error) {
		c.server.log(slog.LevelError, "peer shutdown monitor failed", "connection_id", c.id, "error", watchErr.Error())
	})
	if err != nil {
		if !errors.Is(err, errPeerWatchUnsupported) {
			c.fatal(err)
			return
		}
		// Ordinary portable networking still has its real EOF reader; do not
		// advertise a passive queued-data close watcher on unsupported systems.
		c.server.log(slog.LevelWarn, "passive peer shutdown monitor unavailable", "error", err.Error())
	}
	draining := false
	messages, sessionEnded := c.startReader(), c.watchSessionEnd()
readMessages:
	for {
		var message frontendMessage
		select {
		case terminalErr := <-sessionEnded:
			reason = engine.Terminated
			_ = c.writeSQLError(c.ctx, "", terminalErr)
			return
		case current, open := <-messages:
			if !open {
				break readMessages
			}
			message = current
		}
		if c.ctx.Err() != nil {
			c.releaseMessage(message)
			continue
		}
		if message.tag == 'X' {
			if len(message.body) != 0 {
				c.fatal(protocolError("invalid Terminate message"))
				return
			}
			reason = engine.CleanClose
			return
		}
		if draining {
			switch message.tag {
			case 'S':
				if len(message.body) != 0 {
					c.fatal(protocolError("invalid Sync message"))
					return
				}
				draining = false
				err = c.sendSettings(c.settingsSnapshot())
				if err == nil {
					err = c.sendReady()
				}
				if err == nil {
					err = c.wire.flush()
				}
			case 'H':
				if len(message.body) != 0 {
					c.fatal(protocolError("invalid Flush message"))
					return
				}
				err = c.wire.flush()
			default:
				// Extended-query recovery discards all ordinary frontend work through Sync.
				if !knownFrontendTag(message.tag) {
					c.fatal(protocolError("unknown frontend message"))
					return
				}
			}
			c.releaseMessage(message)
			if err != nil {
				return
			}
			continue
		}
		switch message.tag {
		case 'Q':
			body := message.body
			var source string
			source, err = consumeString(&body)
			if err != nil || len(body) != 0 {
				c.fatal(protocolError("invalid Query message"))
				return
			}
			ctx, done := c.beginCommand()
			out := c.executeQuery(ctx, source)
			err = c.writeQuery(ctx, source, out)
			done()
		case 'P', 'B', 'D', 'E', 'C':
			source, valid := validateExtended(message)
			if !valid {
				c.fatal(protocolError("invalid extended query message"))
				return
			}
			err = c.rejectCommand(unsupported(extendedUnsupported))
			err = c.writeSQLError(c.ctx, source, err)
			if err == nil {
				err = c.wire.flush()
			}
			draining = true
		case 'S':
			if len(message.body) != 0 {
				c.fatal(protocolError("invalid Sync message"))
				return
			}
			err = c.sendSettings(c.settingsSnapshot())
			if err == nil {
				err = c.sendReady()
			}
			if err == nil {
				err = c.wire.flush()
			}
		case 'H':
			if len(message.body) != 0 {
				c.fatal(protocolError("invalid Flush message"))
				return
			}
			err = c.wire.flush()
		case 'F':
			if !validateFunctionCall(message.body) {
				c.fatal(protocolError("invalid FunctionCall message"))
				return
			}
			err = c.rejectCommand(unsupported("function-call protocol is not supported by stuntgres Phase 1"))
			err = c.writeSQLError(c.ctx, "", err)
			if err == nil {
				err = c.sendSettings(c.settingsSnapshot())
			}
			if err == nil {
				err = c.sendReady()
			}
			if err == nil {
				err = c.wire.flush()
			}
		case 'd', 'c', 'f':
			c.fatal(protocolError("COPY stream message outside COPY mode"))
			return
		default:
			c.fatal(protocolError("unknown frontend message"))
			return
		}
		c.releaseMessage(message)
		if err != nil {
			if diagnostic(err).Severity == "FATAL" {
				if !c.fatalSent {
					_ = c.writeSQLError(c.ctx, "", err)
				}
				reason = engine.Terminated
			}
			return
		}
	}
	if c.readFailure != nil {
		c.fatal(c.readFailure)
	}
}
func (c *client) fatal(err error) { _ = c.wire.errorResponse(err, true); _ = c.wire.flush() }

// Wire capability errors still use the engine's failed-command authority. In
// particular, an explicit transaction becomes aborted without synthetic SQL.
func (c *client) rejectCommand(err error) error {
	op, _, admissionErr := c.session.AcquireCommand(c.ctx)
	if admissionErr != nil {
		return admissionErr
	}
	defer op.Done()
	scope, admissionErr := c.session.Engine().StartStatement(op)
	if admissionErr != nil {
		return op.CommandError(admissionErr)
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

func take16(body *[]byte) (uint16, bool) {
	if len(*body) < 2 {
		return 0, false
	}
	v := binary.BigEndian.Uint16(*body)
	*body = (*body)[2:]
	return v, true
}
func take32(body *[]byte) (uint32, bool) {
	if len(*body) < 4 {
		return 0, false
	}
	v := binary.BigEndian.Uint32(*body)
	*body = (*body)[4:]
	return v, true
}
func takeFormats(body *[]byte) bool {
	n, ok := take16(body)
	if !ok || int(n) > len(*body)/2 {
		return false
	}
	for range n {
		v, _ := take16(body)
		if v > 1 {
			return false
		}
	}
	return true
}
func takeParameters(body *[]byte) bool {
	n, ok := take16(body)
	if !ok {
		return false
	}
	for range n {
		size, ok := take32(body)
		if !ok {
			return false
		}
		if size == mathNullLength {
			continue
		}
		if size > uint32(len(*body)) {
			return false
		}
		*body = (*body)[int(size):]
	}
	return true
}

const mathNullLength = ^uint32(0)

func validateExtended(message frontendMessage) (source string, valid bool) {
	body := message.body
	switch message.tag {
	case 'P':
		if _, err := consumeString(&body); err != nil {
			return "", false
		}
		var err error
		source, err = consumeString(&body)
		if err != nil {
			return "", false
		}
		n, ok := take16(&body)
		if !ok || len(body) != int(n)*4 {
			return "", false
		}
		return source, true
	case 'B':
		for range 2 {
			if _, err := consumeString(&body); err != nil {
				return "", false
			}
		}
		return "", takeFormats(&body) && takeParameters(&body) && takeFormats(&body) && len(body) == 0
	case 'D', 'C':
		if len(body) == 0 || (body[0] != 'S' && body[0] != 'P') {
			return "", false
		}
		body = body[1:]
		_, err := consumeString(&body)
		return "", err == nil && len(body) == 0
	case 'E':
		if _, err := consumeString(&body); err != nil {
			return "", false
		}
		count, ok := take32(&body)
		return "", ok && count <= 0x7fffffff && len(body) == 0
	}
	return "", false
}
func validateFunctionCall(body []byte) bool {
	if _, ok := take32(&body); !ok {
		return false
	}
	if !takeFormats(&body) || !takeParameters(&body) {
		return false
	}
	format, ok := take16(&body)
	return ok && format <= 1 && len(body) == 0
}
