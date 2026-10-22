package dml

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CopyBinaryCursor executes binary COPY FROM: it converts the fields its
// binary reader frames into target values and attaches COPY diagnostics. It
// reads the operation's COPY input state and the host's input environment at
// every use, as the rows arrive.
type CopyBinaryCursor struct {
	op                *session.Operation
	host              Host
	binding           analyze.Binding
	in                copyfmt.BinaryReader
	defaults, omitted []bool
	column            string
}

// NewCopyBinaryCursor is the binary COPY FROM cursor of b over in.
func NewCopyBinaryCursor(h Host, b analyze.Binding, in copyfmt.BinaryReader) *CopyBinaryCursor {
	c := &CopyBinaryCursor{op: h.Operation(), host: h, binding: b, in: in, defaults: make([]bool, len(b.Relation.Columns)), omitted: make([]bool, len(b.Relation.Columns))}
	for i := range c.omitted {
		c.omitted[i] = true
	}
	for _, i := range b.Targets {
		c.omitted[i] = false
	}
	return c
}
func (c *CopyBinaryCursor) Schema() []catalog.Column { return nil }
func (c *CopyBinaryCursor) Close() error             { return nil }
func (c *CopyBinaryCursor) input() sqltype.InputEnvironment {
	input, _ := c.host.Environments()
	return input
}
func (c *CopyBinaryCursor) rowDefaults() []bool { return c.defaults }
func (c *CopyBinaryCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	wireInput := c.op.CopyInput.Active.Load()
	if wireInput {
		ctx = c.op.Context()
	}
	if err := ctx.Err(); err != nil {
		if c.in.HeaderDone() && c.op.CopyInput.Held() && session.PendingProtocolUserCancel(ctx) {
			// Refresh a context captured just before user cancellation only
			// after binary startup has admitted the row-decoding phase.
			ctx = c.op.Context()
			err = ctx.Err()
		}
		if err != nil {
			return false, c.Diagnostic(err)
		}
	}
	if !c.in.HeaderDone() {
		if err := c.in.ReadHeader(); err != nil {
			return false, c.Diagnostic(err)
		}
	}
	c.column = ""
	count, ok, err := c.in.Row()
	if err != nil {
		return false, c.Diagnostic(err)
	}
	if !ok {
		return false, nil
	}
	if int(count) != len(c.binding.Targets) {
		return false, c.Diagnostic(sqlError("22P04", fmt.Sprintf("row field count is %d, expected %d", count, len(c.binding.Targets))))
	}
	copy(c.defaults, c.omitted)
	for i, target := range c.binding.Targets {
		column := c.binding.Relation.Columns[target]
		c.column = column.Name
		payload, null, err := c.in.Field()
		if err != nil {
			return false, c.Diagnostic(err)
		}
		if null {
			if wireInput {
				ctx = c.op.Context()
				if column.Usage.Type.IsDomain() {
					if err := column.Usage.Type.CheckBinaryInput(); err != nil {
						return false, c.Diagnostic(err)
					}
				}
			}
			dst[i] = sqltype.Null(column.Usage.Type)
			if err := column.Usage.Type.CheckDomainInput(ctx, dst[i], c.input()); err != nil {
				return false, c.Diagnostic(err)
			}
			c.column = ""
			continue
		}
		if wireInput {
			ctx = c.op.Context()
		}
		dst[i], err = column.Usage.Type.BinaryInputWithEnvironment(ctx, payload, column.Usage.Typmod, c.input())
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
func (c *CopyBinaryCursor) Diagnostic(err error) error {
	if err == nil {
		return nil
	}
	err = c.host.CopyCommandError(err)
	var record *sqlstate.Error
	if !errors.As(err, &record) || c.in.Number() == 0 {
		return err
	}
	prefix := fmt.Sprintf("COPY %s, line %d", c.binding.Relation.Name(), c.in.Number())
	if strings.Contains(record.Context, fmt.Sprintf("COPY %s, line ", c.binding.Relation.Name())) {
		return err
	}
	result := *record
	if c.column != "" {
		prefix += ", column " + c.column
	}
	if result.Context != "" {
		prefix = result.Context + "\n" + prefix
	}
	result.Context = prefix
	return &result
}
