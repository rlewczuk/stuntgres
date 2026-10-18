package main

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *client) writeDescription(schema []catalog.Column, formats []int16) error {
	if len(schema) > math.MaxInt16 {
		return &sqlstate.Error{Code: "54000", Message: "too many result columns"}
	}
	body := appendInt16(nil, int16(len(schema)))
	for i, column := range schema {
		body = appendString(body, column.Name)
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
	return c.wire.send('T', body)
}

// writeFields frames already encoded values from the engine's shared codecs.
// It never retains the borrowed fields after this synchronous write.
func (c *client) writeFields(fields []engine.ProtocolField, buffer *[]byte) (logicalErr, transportErr error) {
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

// writeRows writes only retained data; its caller owns the result descriptor's
// execution-stage timing and must send it exactly once before data.
func (c *client) writeRows(ctx context.Context, rows engine.RowResult, buffer *[]byte, scratch *[]sqltype.Value) (logicalErr, transportErr error) {
	schema := rows.Schema()
	if cap(*scratch) < len(schema) {
		*scratch = make([]sqltype.Value, len(schema))
	} else {
		*scratch = (*scratch)[:len(schema)]
	}
	values := *scratch
	environment := rows.OutputEnvironment()
	body := (*buffer)[:0]
	defer func() { *buffer = body }()
	for {
		if err := ctx.Err(); err != nil {
			return err, nil
		}
		next, err := rows.Next(ctx, values)
		if err != nil {
			return err, nil
		}
		if !next {
			return nil, nil
		}
		body = appendInt16(body[:0], int16(len(schema)))
		for _, value := range values {
			if value.IsNull() {
				body = appendInt32(body, math.MaxUint32)
				continue
			}
			text, err := value.Type().OutputWithEnvironment(ctx, value, environment)
			if err != nil {
				return err, nil
			}
			if len(text) > maxMessageLength-8-len(body) {
				return &sqlstate.Error{Code: "54000", Message: "data row exceeds maximum allowed length"}, nil
			}
			body = appendInt32(body, uint32(len(text)))
			body = append(body, text...)
		}
		if err := c.wire.send('D', body); err != nil {
			return nil, err
		}
	}
}
func (c *client) writeSQLError(ctx context.Context, source string, err error) error {
	return c.writeContextError(ctx, engine.ProtocolContext{Source: source}, err)
}
func (c *client) writeContextError(ctx context.Context, source engine.ProtocolContext, err error) error {
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
