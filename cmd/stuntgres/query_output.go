package main

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func (c *client) descriptionBody(schema []catalog.Column, formats []int16) ([]byte, error) {
	c.refreshEncoding()
	if len(schema) > math.MaxInt16 {
		return nil, &sqlstate.Error{Code: "54000", Message: "too many result columns"}
	}
	body := appendInt16(nil, int16(len(schema)))
	for i, column := range schema {
		var err error
		body, err = c.wire.appendText(c.ctx, body, column.Name)
		if err != nil {
			return nil, err
		}
		body = appendInt32(body, column.OriginTable)
		body = appendInt16(body, column.OriginAttribute)
		usage := column.Usage.BaseUsage()
		body = appendInt32(body, uint32(usage.Type.ID()))
		body = appendInt16(body, usage.Type.PostgreSQLTypeLength())
		body = appendInt32(body, uint32(usage.Typmod))
		var format int16
		if len(formats) == 1 {
			format = formats[0]
		} else if len(formats) > 1 {
			format = formats[i]
		}
		body = appendInt16(body, format)
	}
	return body, nil
}

func (c *client) writeDescription(schema []catalog.Column, formats []int16) error {
	body, err := c.descriptionBody(schema, formats)
	if err != nil {
		return err
	}
	return c.wire.send('T', body)
}

// writeFields frames already encoded values from the engine's shared codecs.
// It never retains the borrowed fields after this synchronous write.
func (c *client) writeFields(fields []session.ProtocolField, buffer *[]byte) (logicalErr, transportErr error) {
	if len(fields) > math.MaxInt16 {
		return &sqlstate.Error{Code: "54000", Message: "too many result columns"}, nil
	}
	body := appendInt16((*buffer)[:0], int16(len(fields)))
	defer func() { *buffer = body }()
	for _, field := range fields {
		if field.Null {
			body = appendInt32(body, math.MaxUint32)
			continue
		}
		if len(field.Bytes) > maxMessageLength-8-len(body) {
			return &sqlstate.Error{Code: "54000", Message: "data row exceeds maximum allowed length"}, nil
		}
		body = appendInt32(body, uint32(len(field.Bytes)))
		body = append(body, field.Bytes...)
	}
	return nil, c.wire.send('D', body)
}

func (c *client) writeSQLError(ctx context.Context, source string, err error) error {
	return c.writeContextError(ctx, session.ProtocolContext{Source: source}, err)
}
func (c *client) writeContextError(ctx context.Context, source session.ProtocolContext, err error) error {
	// An execution owner may already have enriched the typed diagnostic (COPY
	// line context, for example). Never replace that primary error with a
	// context cause just because cleanup also returned context.Canceled.
	var primary *sqlstate.Error
	if !errors.As(err, &primary) && errors.Is(err, context.Canceled) {
		if cause := context.Cause(ctx); cause == errUserCancel {
			err = cause
		} else if cause != nil && diagnostic(cause).Severity == "FATAL" {
			err = cause
		}
	}
	if logErr := c.server.logs.sqlError(ctx, source.Source, err, logParameters(source.Parameters), "connection_id", c.id, "database", c.database, "user", c.user); logErr != nil {
		c.server.fail(logErr)
		return logErr
	}
	if err := c.wire.errorResponse(err, false); err != nil {
		return err
	}
	if fatal := diagnostic(err); fatal.Severity == "FATAL" {
		if e := c.wire.flush(); e != nil {
			return e
		}
		c.fatalSent = true
		return fatal
	}
	return nil
}
