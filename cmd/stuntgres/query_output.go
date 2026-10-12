package main

import (
	"context"
	"errors"
	"math"

	"stuntgres/internal/engine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// writeQuery owns finalized results only: execution has already released all
// engine admission before any of these writes. Buffers are reused across every
// statement and row in this message, independently of retained cursor storage.
func (c *client) writeQuery(ctx context.Context, source string, out messageOutput) (err error) {
	closeRows := func() error {
		var closeErr error
		for i := range out.statements {
			if rows := out.statements[i].result.Rows; rows != nil {
				out.statements[i].result.Rows = nil
				closeErr = errors.Join(closeErr, rows.Close())
			}
		}
		return closeErr
	}
	defer func() { err = errors.Join(err, closeRows()) }()
	if c.ctx.Err() != nil {
		return nil // Reader/transport termination owns the fatal/EOF boundary.
	}

	var body []byte
	var values []sqltype.Value
	logicalErr := out.err
	outputFailed := false
	for i := range out.statements {
		statement := &out.statements[i]
		for _, notice := range statement.notices {
			if err := c.wire.notice(notice); err != nil {
				return err
			}
		}
		if err := c.sendSettings(statement.settings); err != nil {
			return err
		}
		if rows := statement.result.Rows; rows != nil {
			rowErr, transportErr := c.writeRows(ctx, rows, &body, &values)
			// A required close failure vetoes completion just like a codec error.
			statement.result.Rows = nil
			closeErr := rows.Close()
			if transportErr != nil {
				return errors.Join(transportErr, closeErr)
			}
			if rowErr != nil || closeErr != nil {
				logicalErr = errors.Join(rowErr, closeErr)
				outputFailed = true
				break
			}
		} else if ctx.Err() != nil {
			logicalErr = ctx.Err()
			outputFailed = true
			break
		}
		if statement.result.CommandTag == "" {
			logicalErr = errors.New("stuntgres: completed statement has no command tag")
			outputFailed = true
			break
		}
		body = appendString(body[:0], statement.result.CommandTag)
		if err := c.wire.send('C', body); err != nil {
			return err
		}
	}
	// Close even un-emitted suffix results before reporting an output error.
	// Native diagnostics remain the primary error when cleanup also fails.
	closeErr := closeRows()
	if closeErr != nil {
		outputFailed = true
	}
	logicalErr = errors.Join(logicalErr, closeErr)
	if c.ctx.Err() != nil {
		return nil // Never emit ordinary recovery ahead of a framing FATAL.
	}
	if outputFailed {
		// SQL admission is already released. Apply this late logical failure
		// through the existing failed-command owner, not a transport TxState.
		logicalErr = c.rejectCommand(logicalErr)
		out.settings = c.settingsSnapshot()
	}
	for _, notice := range out.notices {
		if err := c.wire.notice(notice); err != nil {
			return err
		}
	}
	if err := c.sendSettings(out.settings); err != nil {
		return err
	}
	if logicalErr != nil {
		if err := c.writeSQLError(ctx, source, logicalErr); err != nil {
			return err
		}
	}
	if logicalErr == nil && out.empty {
		if err := c.wire.send('I', nil); err != nil {
			return err
		}
	}
	if err := c.sendReady(); err != nil {
		if diagnostic(err).Severity == "FATAL" {
			return c.writeSQLError(ctx, source, err)
		}
		return err
	}
	return c.wire.flush()
}

// writeRows distinguishes SQL/output-provider failures from socket failures.
// A partially emitted rowset must never receive a successful CommandComplete.
func (c *client) writeRows(ctx context.Context, rows engine.RowResult, buffer *[]byte, scratch *[]sqltype.Value) (logicalErr, transportErr error) {
	schema := rows.Schema()
	if len(schema) > math.MaxInt16 {
		return &sqlstate.Error{Code: "54000", Message: "too many result columns"}, nil
	}
	body := appendInt16((*buffer)[:0], int16(len(schema)))
	defer func() { *buffer = body }()
	for _, column := range schema {
		if len(column.Name) > maxMessageLength-4-len(body)-19 {
			return &sqlstate.Error{Code: "54000", Message: "row description exceeds maximum allowed length"}, nil
		}
		body = appendString(body, column.Name)
		body = appendInt32(body, column.OriginTable)
		body = appendInt16(body, column.OriginAttribute)
		body = appendInt32(body, uint32(column.Usage.Type.ID()))
		body = appendInt16(body, column.Usage.Type.PostgreSQLTypeLength())
		body = appendInt32(body, uint32(column.Usage.Typmod))
		body = appendInt16(body, 0) // Phase 1 has text output only.
	}
	if err := c.wire.send('T', body); err != nil {
		return nil, err
	}
	if cap(*scratch) < len(schema) {
		*scratch = make([]sqltype.Value, len(schema))
	} else {
		*scratch = (*scratch)[:len(schema)]
	}
	values := *scratch
	environment := rows.OutputEnvironment()
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
			if len(body) > maxMessageLength-8 {
				return &sqlstate.Error{Code: "54000", Message: "data row exceeds maximum allowed length"}, nil
			}
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

// writeSQLError emits an ordinary error without a ready boundary. The dispatcher
// owns whether recovery ends immediately (simple query) or waits through Sync.
func (c *client) writeSQLError(ctx context.Context, source string, err error) error {
	if errors.Is(err, context.Canceled) {
		if cause := context.Cause(ctx); cause == errUserCancel {
			err = &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
		} else if cause != nil && diagnostic(cause).Severity == "FATAL" {
			err = cause
		}
	}
	if logErr := c.server.logs.sqlError(ctx, source, err, "connection_id", c.id, "database", c.database, "user", c.user); logErr != nil {
		c.server.fail(logErr)
		return logErr
	}
	if err := c.wire.errorResponse(err, false); err != nil {
		return err
	}
	if fatal := diagnostic(err); fatal.Severity == "FATAL" {
		if flushErr := c.wire.flush(); flushErr != nil {
			return flushErr
		}
		c.fatalSent = true
		return fatal // A FATAL has no ReadyForQuery or recoverable cycle.
	}
	return nil
}
