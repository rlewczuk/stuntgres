package main

import (
	"context"
	"fmt"
	"io"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/sqlstate"
)

// The serial executor itself is the COPY stream. It borrows at most one reader
// buffer and returns it before receiving the next; no stream worker or queue.
type protocolCopy struct {
	client       *client
	ctx          context.Context
	current      frontendMessage
	offset       int
	borrowed     bool
	ended        bool
	started      bool
	from         bool
	transportErr error
}

func (p *protocolCopy) release() {
	if p.borrowed {
		p.client.releaseMessage(p.current)
		p.borrowed = false
		p.current = frontendMessage{}
		p.offset = 0
	}
}

// closeCommand releases operation/frame references on every exit. Inactive
// ordinary SQL only clears the borrowed operation context; COPY flags are
// initialized when the engine actually starts a new stream.
func (p *protocolCopy) closeCommand() {
	p.ctx = nil
	if !p.started && !p.borrowed && p.transportErr == nil {
		return
	}
	p.release()
	p.started = false
	p.transportErr = nil
}
func (p *protocolCopy) start(metadata core.CopyProtocolMetadata) error {
	p.release()
	p.ended = false
	p.transportErr = nil
	p.started = true
	p.from = metadata.From
	tag := byte('H')
	p.client.protocolState = protocolCopyOut
	if metadata.From {
		tag = 'G'
		p.client.protocolState = protocolCopyIn
	}
	body := []byte{byte(metadata.Format)}
	body = appendInt16(body, int16(len(metadata.ColumnFormats)))
	for _, format := range metadata.ColumnFormats {
		body = appendInt16(body, format)
	}
	if err := p.client.wire.send(tag, body); err != nil {
		p.transportErr = err
		return err
	}
	if err := p.client.wire.flush(); err != nil {
		p.transportErr = err
		return err
	}
	return nil
}
func (p *protocolCopy) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for {
		if err := p.client.ctx.Err(); err != nil {
			return 0, err
		}
		select {
		case terminal := <-p.client.sessionEnded:
			return 0, terminal
		default:
		}
		operationDone := p.ctx.Done()
		if err := p.ctx.Err(); err != nil {
			if context.Cause(p.ctx) != errUserCancel {
				return 0, err
			}
			// COPY receives complete frontend frames with logical user cancel
			// deferred. Only the engine's real record/conversion boundary may
			// consume that pending cause; this adapter never examines row bytes.
			operationDone = nil
		}
		if p.borrowed && p.offset < len(p.current.body) {
			n := copy(dst, p.current.body[p.offset:])
			p.offset += n
			return n, nil
		}
		p.release()
		if p.ended {
			return 0, io.EOF
		}
		var m frontendMessage
		select {
		case terminal := <-p.client.sessionEnded:
			return 0, terminal
		case <-operationDone:
			// Reclassify the exact immutable cause before the next wait.
			continue
		case <-p.client.ctx.Done():
			return 0, p.client.ctx.Err()
		case current, open := <-p.client.messages:
			if !open {
				return 0, io.ErrUnexpectedEOF
			}
			m = current
		}
		p.current = m
		p.borrowed = true
		p.client.capture.copyInput(m)
		if p.client.reference != nil {
			p.client.reference.forward(m)
		}
		switch m.tag {
		case 'd':
			continue
		case 'c':
			p.ended = true
			p.release()
			return 0, io.EOF
		case 'f':
			input := protocolInput{body: m.body, encoding: p.client.session.Settings.ClientEncoding(), ctx: p.ctx}
			message, err := input.String()
			if err == nil {
				err = input.End()
			}
			p.release()
			if err != nil {
				return 0, err
			}
			return 0, &sqlstate.Error{Code: "57014", Message: "COPY from stdin failed: " + message}
		case 'H', 'S':
			p.release()
			continue
		default:
			p.release()
			return 0, protocolError(fmt.Sprintf("unexpected message type 0x%02X during COPY from stdin", m.tag))
		}
	}
}
func (p *protocolCopy) finishInput() error {
	// Text may stop on its in-stream marker. Its buffered suffix is deliberately
	// discarded; the actual protocol terminator still has final veto authority.
	for {
		_, err := p.Read(p.client.copyDrain[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
func (p *protocolCopy) Write(body []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	if err := p.client.wire.send('d', body); err != nil {
		p.transportErr = err
		return 0, err
	}
	return len(body), nil
}
func (p *protocolCopy) complete() error {
	if !p.started {
		return nil
	}
	p.started = false
	if !p.from {
		if err := p.client.wire.send('c', nil); err != nil {
			p.transportErr = err
			return err
		}
	}
	return nil
}
