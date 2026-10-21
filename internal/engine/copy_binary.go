package engine

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// copyBinaryCursor executes binary COPY FROM: it converts the fields its
// binary reader frames into target values and attaches COPY diagnostics.
type copyBinaryCursor struct {
	scope             *StatementScope
	binding           analyze.Binding
	in                copyfmt.BinaryReader
	defaults, omitted []bool
	column            string
}

func newCopyBinaryCursor(s *StatementScope, b analyze.Binding, r io.Reader) *copyBinaryCursor {
	c := &copyBinaryCursor{scope: s, binding: b, in: copyfmt.NewBinaryReader(bufio.NewReader(copyReader{s, r})), defaults: make([]bool, len(b.Relation.Columns)), omitted: make([]bool, len(b.Relation.Columns))}
	for i := range c.omitted {
		c.omitted[i] = true
	}
	for _, i := range b.Targets {
		c.omitted[i] = false
	}
	return c
}
func (c *copyBinaryCursor) Schema() []catalog.Column { return nil }
func (c *copyBinaryCursor) Close() error             { return nil }
func (c *copyBinaryCursor) rowDefaults() []bool      { return c.defaults }
func (c *copyBinaryCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	wireInput := c.scope.op.CopyInput.Active.Load()
	if wireInput {
		ctx = c.scope.op.Context()
	}
	if err := ctx.Err(); err != nil {
		if c.in.HeaderDone() && c.scope.op.CopyInput.Held() && session.PendingProtocolUserCancel(ctx) {
			// Refresh a context captured just before user cancellation only
			// after binary startup has admitted the row-decoding phase.
			ctx = c.scope.op.Context()
			err = ctx.Err()
		}
		if err != nil {
			return false, c.diagnostic(err)
		}
	}
	if !c.in.HeaderDone() {
		if err := c.in.ReadHeader(); err != nil {
			return false, c.diagnostic(err)
		}
	}
	c.column = ""
	count, ok, err := c.in.Row()
	if err != nil {
		return false, c.diagnostic(err)
	}
	if !ok {
		return false, nil
	}
	if int(count) != len(c.binding.Targets) {
		return false, c.diagnostic(sqlError("22P04", fmt.Sprintf("row field count is %d, expected %d", count, len(c.binding.Targets))))
	}
	copy(c.defaults, c.omitted)
	for i, target := range c.binding.Targets {
		column := c.binding.Relation.Columns[target]
		c.column = column.Name
		payload, null, err := c.in.Field()
		if err != nil {
			return false, c.diagnostic(err)
		}
		if null {
			if wireInput {
				ctx = c.scope.op.Context()
				if column.Usage.Type.IsDomain() {
					if err := column.Usage.Type.CheckBinaryInput(); err != nil {
						return false, c.diagnostic(err)
					}
				}
			}
			dst[i] = sqltype.Null(column.Usage.Type)
			if err := column.Usage.Type.CheckDomainInput(ctx, dst[i], c.scope.input); err != nil {
				return false, c.diagnostic(err)
			}
			c.column = ""
			continue
		}
		if wireInput {
			ctx = c.scope.op.Context()
		}
		dst[i], err = column.Usage.Type.BinaryInputWithEnvironment(ctx, payload, column.Usage.Typmod, c.scope.input)
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
func (c *copyBinaryCursor) diagnostic(err error) error {
	if err == nil {
		return nil
	}
	err = c.scope.copyCommandError(err)
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
func (s *StatementScope) copyBinaryOutput(rows exec.RowResult, w io.Writer) (int64, error) {
	schema := rows.Schema()
	if len(schema) > math.MaxInt16 {
		return 0, sqlError("54000", "too many binary COPY columns")
	}
	if s.protocolOrZero().command || s.protocolOrZero().simple {
		// CopyOutResponse has already been sent. Resolve the declared send
		// providers before the binary signature or any row demand.
		for _, column := range schema {
			if err := column.Usage.Type.CheckBinaryOutput(); err != nil {
				return 0, err
			}
		}
	}
	values := make([]sqltype.Value, len(schema))
	buffer := make([]byte, 0, 4096)
	buffer = append(buffer, copyfmt.BinarySignature...)
	buffer = binary.BigEndian.AppendUint32(buffer, 0)
	buffer = binary.BigEndian.AppendUint32(buffer, 0)
	writer := copyWriter{s, w}
	write := func() error {
		n, err := writer.Write(buffer)
		if err == nil && n != len(buffer) {
			err = io.ErrShortWrite
		}
		return err
	}
	if err := write(); err != nil {
		return 0, err
	}
	count := int64(0)
	for {
		present, err := rows.Next(s.op.Context(), values)
		if err != nil {
			return 0, err
		}
		if !present {
			break
		}
		buffer = buffer[:0]
		buffer = binary.BigEndian.AppendUint16(buffer, uint16(len(values)))
		output := rows.OutputEnvironment()
		for _, v := range values {
			if v.IsNull() {
				buffer = binary.BigEndian.AppendUint32(buffer, math.MaxUint32)
				continue
			}
			start := len(buffer)
			buffer = binary.BigEndian.AppendUint32(buffer, 0)
			buffer, err = v.Type().AppendBinaryWithEnvironment(s.op.Context(), buffer, v, output)
			if err != nil {
				return 0, err
			}
			n := len(buffer) - start - 4
			if n > math.MaxInt32 {
				return 0, sqlError("54000", "binary value is too large")
			}
			binary.BigEndian.PutUint32(buffer[start:], uint32(n))
		}
		if err := write(); err != nil {
			return 0, err
		}
		count++
	}
	buffer = buffer[:0]
	buffer = binary.BigEndian.AppendUint16(buffer, math.MaxUint16)
	if err := write(); err != nil {
		return 0, err
	}
	return count, s.active()
}
