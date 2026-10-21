package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// copyCursor executes text and CSV COPY FROM: it converts the fields its
// line reader produces into target values and attaches COPY diagnostics.
type copyCursor struct {
	scope             *StatementScope
	binding           analyze.Binding
	in                copyfmt.LineReader
	defaults, omitted []bool
	column, value     string
	nullInput         bool
}

func newCopyCursor(scope *StatementScope, b analyze.Binding, o copyfmt.Options, r io.Reader) *copyCursor {
	// One backing array for both row flags offsets the targets allocation.
	n := len(b.Relation.Columns)
	flags := make([]bool, 2*n)
	c := &copyCursor{scope: scope, binding: b, in: copyfmt.NewLineReader(o, bufio.NewReader(copyReader{scope, r}), CopyLineTargets(b)), defaults: flags[:n:n], omitted: flags[n:]}
	for i := range c.omitted {
		c.omitted[i] = true
	}
	for _, target := range b.Targets {
		c.omitted[target] = false
	}
	return c
}

func (c *copyCursor) Schema() []catalog.Column { return nil }
func (c *copyCursor) Close() error             { return nil }
func (c *copyCursor) rowDefaults() []bool      { return c.defaults }
func (c *copyCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	wireInput := c.scope.op.CopyInput.Active.Load()
	if wireInput {
		ctx = c.scope.op.Context()
	}
	if err := ctx.Err(); err != nil {
		if c.scope.op.CopyInput.Held() && session.PendingProtocolUserCancel(ctx) {
			// User cancellation may arrive after Context selected its live
			// standard context. Refresh only the admitted decoder's view;
			// its owner retains deadlines, terminal and source authority.
			ctx = c.scope.op.Context()
			err = ctx.Err()
		}
		if err != nil {
			return false, c.diagnostic(err)
		}
	}
	c.column = ""
	if err := c.in.Header(); err != nil {
		return false, c.diagnostic(err)
	}
	present, err := c.in.ReadLine()
	if err != nil || !present {
		return false, c.diagnostic(err)
	}
	if wireInput {
		ctx = c.scope.op.Context()
	}
	if err = c.in.Split(true); err != nil {
		return false, c.diagnostic(err)
	}
	fields := c.in.Fields()
	if len(fields) > len(dst) {
		return false, c.diagnostic(copyfmt.LineError("extra data after last expected column", ""))
	}
	copy(c.defaults, c.omitted)
	for i, target := range c.binding.Targets {
		column := c.binding.Relation.Columns[target]
		if i >= len(fields) {
			return false, c.diagnostic(copyfmt.LineError(fmt.Sprintf("missing data for column \"%s\"", column.Name), ""))
		}
		field := fields[i]
		dst[i] = sqltype.Null(column.Usage.Type)
		if c.in.FieldNull(i) {
			c.column, c.nullInput = column.Name, true
			if err := column.Usage.Type.CheckDomainInput(ctx, dst[i], c.scope.input); err != nil {
				return false, c.diagnostic(err)
			}
			c.column, c.nullInput = "", false
			continue
		}
		if field.Default {
			c.defaults[target] = true
			continue
		}
		c.column = column.Name
		c.nullInput = false
		c.value = string(field.Decoded)
		dst[i], err = column.Usage.Type.InputWithEnvironment(ctx, c.value, column.Usage.Typmod, c.scope.input)
		if err != nil {
			return false, c.diagnostic(err)
		}
		c.column = ""
	}
	if err := c.scope.op.PendingCopyInputError(); err != nil {
		return false, c.diagnostic(err)
	}
	return true, nil
}
func (c *copyCursor) diagnostic(err error) error {
	if err == nil {
		return nil
	}
	// Map only this operation's SQL-owned cancellation before attaching the
	// active COPY input context; ordinary caller context and I/O errors retain
	// their identity, and relation-opening errors never enter this cursor.
	err = c.scope.copyCommandError(err)
	var record *sqlstate.Error
	if !errors.As(err, &record) || record.Context != "" {
		return err
	}
	result := *record
	result.Context = fmt.Sprintf("COPY %s, line %d", c.binding.Relation.Name(), c.in.Number())
	if c.column != "" {
		if c.nullInput {
			result.Context += fmt.Sprintf(", column %s: null input", c.column)
		} else {
			result.Context += fmt.Sprintf(", column %s: \"%s\"", c.column, copyfmt.ContextValue(c.value))
		}
	} else if c.in.LineValid() && record.Code != "23505" {
		result.Context += fmt.Sprintf(": \"%s\"", copyfmt.ContextValue(string(c.in.Line())))
	}
	return &result
}

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
