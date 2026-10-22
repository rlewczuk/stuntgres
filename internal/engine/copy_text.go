package engine

import (
	"context"
	"errors"
	"io"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Wire cancellation carries an immutable SQL diagnostic through the operation's
// context. Ordinary embedded Go cancellation retains its existing identity.
func (s *StatementScope) copyCommandError(err error) error {
	var primary *sqlstate.Error
	if errors.As(err, &primary) {
		return err
	}
	err = s.op.CommandError(err)
	if errors.As(err, &primary) || !(s.protocolOrZero().command || s.protocolOrZero().simple) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		cancellation := context.Cause(s.op.Context())
		if cancellation == nil {
			cancellation = context.Cause(s.op.Ctx)
		}
		var cause *sqlstate.Error
		if errors.As(cancellation, &cause) && cause != nil && (cause.Code == "57014" || cause.Severity == "FATAL") {
			return cause
		}
	}
	return err
}
func (s *StatementScope) copyOutput(rows exec.RowResult, o copyfmt.Options, w io.Writer) (count int64, err error) {
	if o.Format == copyfmt.Binary {
		return s.copyBinaryOutput(rows, w)
	}
	schema := rows.Schema()
	values := make([]sqltype.Value, len(schema))
	buffer := make([]byte, 0, 4096)
	writer := copyWriter{s, w}
	write := func() error {
		n, err := writer.Write(buffer)
		if err == nil && n != len(buffer) {
			err = io.ErrShortWrite
		}
		return err
	}
	if o.Header != 0 {
		for i, column := range schema {
			if i != 0 {
				buffer = append(buffer, o.Delimiter)
			}
			if o.Format == copyfmt.CSV {
				buffer = copyfmt.AppendCSV(buffer, column.Name, o, false, len(schema) == 1)
			} else {
				buffer = copyfmt.AppendText(buffer, column.Name, o.Delimiter)
			}
		}
		buffer = append(buffer, '\n')
		if err = write(); err != nil {
			return 0, err
		}
	}
	for {
		present, nextErr := rows.Next(s.op.Context(), values)
		if nextErr != nil {
			return 0, nextErr
		}
		if !present {
			break
		}
		buffer = buffer[:0]
		output := rows.OutputEnvironment()
		for i, value := range values {
			if i != 0 {
				buffer = append(buffer, o.Delimiter)
			}
			if value.IsNull() {
				buffer = append(buffer, o.Null...)
				continue
			}
			text, outputErr := value.Type().OutputWithEnvironment(s.op.Context(), value, output)
			if outputErr != nil {
				return 0, outputErr
			}
			if o.Format == copyfmt.CSV {
				buffer = copyfmt.AppendCSV(buffer, text, o, copyfmt.Forced(o.QuoteFields, i), len(schema) == 1)
			} else {
				buffer = copyfmt.AppendText(buffer, text, o.Delimiter)
			}
		}
		buffer = append(buffer, '\n')
		if err = write(); err != nil {
			return 0, err
		}
		count++
	}
	return count, s.active()
}
