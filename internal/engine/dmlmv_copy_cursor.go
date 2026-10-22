package engine

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CopyCursor executes text and CSV COPY FROM: it converts the fields its
// line reader produces into target values and attaches COPY diagnostics. It
// reads the operation's COPY input state and the host's input environment at
// every use, as the rows arrive.
type CopyCursor struct {
	op                *session.Operation
	host              dml.Host
	binding           analyze.Binding
	in                copyfmt.LineReader
	defaults, omitted []bool
	column, value     string
	nullInput         bool
}

// NewCopyCursor is the text or CSV COPY FROM cursor of b over in.
func NewCopyCursor(h dml.Host, b analyze.Binding, in copyfmt.LineReader) *CopyCursor {
	// One backing array for both row flags offsets the targets allocation.
	n := len(b.Relation.Columns)
	flags := make([]bool, 2*n)
	c := &CopyCursor{op: h.Operation(), host: h, binding: b, in: in, defaults: flags[:n:n], omitted: flags[n:]}
	for i := range c.omitted {
		c.omitted[i] = true
	}
	for _, target := range b.Targets {
		c.omitted[target] = false
	}
	return c
}

func (c *CopyCursor) Schema() []catalog.Column { return nil }
func (c *CopyCursor) Close() error             { return nil }
func (c *CopyCursor) input() sqltype.InputEnvironment {
	input, _ := c.host.Environments()
	return input
}
func (c *CopyCursor) rowDefaults() []bool { return c.defaults }
func (c *CopyCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	wireInput := c.op.CopyInput.Active.Load()
	if wireInput {
		ctx = c.op.Context()
	}
	if err := ctx.Err(); err != nil {
		if c.op.CopyInput.Held() && session.PendingProtocolUserCancel(ctx) {
			// User cancellation may arrive after Context selected its live
			// standard context. Refresh only the admitted decoder's view;
			// its owner retains deadlines, terminal and source authority.
			ctx = c.op.Context()
			err = ctx.Err()
		}
		if err != nil {
			return false, c.Diagnostic(err)
		}
	}
	c.column = ""
	if err := c.in.Header(); err != nil {
		return false, c.Diagnostic(err)
	}
	present, err := c.in.ReadLine()
	if err != nil || !present {
		return false, c.Diagnostic(err)
	}
	if wireInput {
		ctx = c.op.Context()
	}
	if err = c.in.Split(true); err != nil {
		return false, c.Diagnostic(err)
	}
	fields := c.in.Fields()
	if len(fields) > len(dst) {
		return false, c.Diagnostic(copyfmt.LineError("extra data after last expected column", ""))
	}
	copy(c.defaults, c.omitted)
	for i, target := range c.binding.Targets {
		column := c.binding.Relation.Columns[target]
		if i >= len(fields) {
			return false, c.Diagnostic(copyfmt.LineError(fmt.Sprintf("missing data for column \"%s\"", column.Name), ""))
		}
		field := fields[i]
		dst[i] = sqltype.Null(column.Usage.Type)
		if c.in.FieldNull(i) {
			c.column, c.nullInput = column.Name, true
			if err := column.Usage.Type.CheckDomainInput(ctx, dst[i], c.input()); err != nil {
				return false, c.Diagnostic(err)
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
		dst[i], err = column.Usage.Type.InputWithEnvironment(ctx, c.value, column.Usage.Typmod, c.input())
		if err != nil {
			return false, c.Diagnostic(err)
		}
		c.column = ""
	}
	if err := c.op.PendingCopyInputError(); err != nil {
		return false, c.Diagnostic(err)
	}
	return true, nil
}

// Diagnostic attaches the COPY line and column context to err.
func (c *CopyCursor) Diagnostic(err error) error {
	if err == nil {
		return nil
	}
	// Map only this operation's SQL-owned cancellation before attaching the
	// active COPY input context; ordinary caller context and I/O errors retain
	// their identity, and relation-opening errors never enter this cursor.
	err = c.host.CopyCommandError(err)
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
