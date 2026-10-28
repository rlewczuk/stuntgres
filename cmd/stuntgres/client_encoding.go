package main

import (
	"context"
	"errors"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func (c *client) refreshEncoding() {
	if c.session != nil {
		c.wire.encoding = c.session.Settings.ClientEncoding()
		c.wire.encodingReady = true
	}
}
func (w *wireCodec) appendText(ctx context.Context, body []byte, value string) ([]byte, error) {
	if !w.encodingReady {
		return appendString(body, value), nil
	}
	start := len(body)
	body, err := w.encoding.Append(ctx, body, value)
	if err != nil {
		return body[:start], err
	}
	if len(body) >= maxMessageLength-4 {
		return body[:start], &sqlstate.Error{Code: "54000", Message: "message exceeds maximum allowed length"}
	}
	return append(body, 0), nil
}

func (c *client) writeNotices(op *session.Operation) (logical, transport error) {
	c.refreshEncoding()
	for _, notice := range op.DetachNotices() {
		if err := c.wire.notice(notice); err != nil {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) {
				return err, nil
			}
			return nil, err
		}
	}
	return nil, nil
}

func (c *client) writeActiveNotices(scope *core.StatementScope) (logical, transport error) {
	if len(scope.Op.Notices) == 0 {
		return nil, nil
	}
	err := scope.ProtocolNotices(func() error {
		logical, transport = c.writeNotices(scope.Op)
		if logical != nil {
			return logical
		}
		return transport
	})
	if transport != nil {
		return nil, transport
	}
	return err, nil
}

// Normal diagnostics are fallible and staged in full. A conversion failure
// replaces the original, clearing its context; only actual recursive trouble
// uses bytewise ASCII replacement. Pre-initialization output stays raw instead.
func (w *wireCodec) encodedDiagnostic(d sqlstate.Error, severity string) ([]byte, error) {
	if !w.encodingReady {
		return diagnosticBody(d, severity), nil
	}
	var body []byte
	for _, field := range diagnosticFields(d, severity) {
		if field.value == "" {
			continue
		}
		body = append(body, field.tag)
		var err error
		body, err = w.appendText(context.Background(), body, field.value)
		if err != nil {
			return nil, err
		}
	}
	return append(body, 0), nil
}
func asciiDiagnostic(d sqlstate.Error, severity string) []byte {
	var body []byte
	for _, field := range diagnosticFields(d, severity) {
		if field.value == "" {
			continue
		}
		body = append(body, field.tag)
		for i := range len(field.value) {
			b := field.value[i]
			if b >= 128 {
				b = '?'
			}
			body = append(body, b)
		}
		body = append(body, 0)
	}
	return append(body, 0)
}
