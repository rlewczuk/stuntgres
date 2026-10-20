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
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const copyBinarySignature = "PGCOPY\n\xff\r\n\x00"

type copyBinaryCursor struct {
	scope             *StatementScope
	binding           analyze.Binding
	reader            *bufio.Reader
	scratch           [4096]byte
	payload           []byte
	defaults, omitted []bool
	number            int64
	column            string
	header, ended     bool
}

func newCopyBinaryCursor(s *StatementScope, b analyze.Binding, r io.Reader) *copyBinaryCursor {
	c := &copyBinaryCursor{scope: s, binding: b, reader: bufio.NewReader(copyReader{s, r}), defaults: make([]bool, len(b.Relation.Columns)), omitted: make([]bool, len(b.Relation.Columns))}
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
func (c *copyBinaryCursor) readFixed(n int) ([]byte, error) {
	_, err := io.ReadFull(c.reader, c.scratch[:n])
	return c.scratch[:n], err
}
func binaryEOF(err error) bool { return err == io.EOF || err == io.ErrUnexpectedEOF }
func (c *copyBinaryCursor) headerInput() error {
	p, err := c.readFixed(len(copyBinarySignature))
	if err != nil {
		if !binaryEOF(err) {
			return err
		}
		return sqlError("22P04", "COPY file signature not recognized")
	}
	if string(p) != copyBinarySignature {
		return sqlError("22P04", "COPY file signature not recognized")
	}
	p, err = c.readFixed(4)
	if err != nil {
		if !binaryEOF(err) {
			return err
		}
		return sqlError("22P04", "invalid COPY file header (missing flags)")
	}
	flags := binary.BigEndian.Uint32(p)
	if flags&0x10000 != 0 {
		return sqlError("22P04", "invalid COPY file header (WITH OIDS)")
	}
	if flags&0xffff0000 != 0 {
		return sqlError("22P04", "unrecognized critical flags in COPY file header")
	}
	p, err = c.readFixed(4)
	if err != nil {
		if !binaryEOF(err) {
			return err
		}
		return sqlError("22P04", "invalid COPY file header (missing length)")
	}
	length := int32(binary.BigEndian.Uint32(p))
	if length < 0 {
		return sqlError("22P04", "invalid COPY file header (missing length)")
	}
	for length > 0 {
		n := min(int(length), len(c.scratch))
		if _, err = c.readFixed(n); err != nil {
			if !binaryEOF(err) {
				return err
			}
			return sqlError("22P04", "invalid COPY file header (wrong length)")
		}
		length -= int32(n)
	}
	return nil
}
func (c *copyBinaryCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	wireInput := c.scope.op.copyInput.active.Load()
	if wireInput {
		ctx = c.scope.op.Context()
	}
	if err := ctx.Err(); err != nil {
		if c.header && c.scope.op.copyInput.held() && pendingProtocolUserCancel(ctx) {
			// Refresh a context captured just before user cancellation only
			// after binary startup has admitted the row-decoding phase.
			ctx = c.scope.op.Context()
			err = ctx.Err()
		}
		if err != nil {
			return false, c.diagnostic(err)
		}
	}
	if c.ended {
		return false, nil
	}
	if !c.header {
		if err := c.headerInput(); err != nil {
			return false, c.diagnostic(err)
		}
		c.header = true
	}
	c.number++
	c.column = ""
	p, err := c.readFixed(2)
	if err != nil {
		if binaryEOF(err) {
			c.ended = true
			return false, nil
		}
		return false, c.diagnostic(err)
	}
	count := int16(binary.BigEndian.Uint16(p))
	if count == -1 {
		c.ended = true
		_, err := c.reader.ReadByte()
		if err == io.EOF {
			return false, nil
		}
		if err == nil {
			err = sqlError("22P04", "received copy data after EOF marker")
		}
		return false, c.diagnostic(err)
	}
	if int(count) != len(c.binding.Targets) {
		return false, c.diagnostic(sqlError("22P04", fmt.Sprintf("row field count is %d, expected %d", count, len(c.binding.Targets))))
	}
	copy(c.defaults, c.omitted)
	for i, target := range c.binding.Targets {
		column := c.binding.Relation.Columns[target]
		c.column = column.Name
		p, err = c.readFixed(4)
		if err != nil {
			if binaryEOF(err) {
				err = sqlError("22P04", "unexpected EOF in COPY data")
			}
			return false, c.diagnostic(err)
		}
		length := int32(binary.BigEndian.Uint32(p))
		if length == -1 {
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
		if length < -1 {
			return false, c.diagnostic(sqlError("22P04", "invalid field size"))
		}
		if length >= 1073741823 {
			return false, c.diagnostic(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "string buffer exceeds maximum allowed length (1073741823 bytes)", Detail: fmt.Sprintf("Cannot enlarge string buffer containing 0 bytes by %d more bytes.", length)})
		}
		// Grow with real bytes, not an attacker-controlled unfulfilled declaration.
		c.payload = c.payload[:0]
		for remaining := int(length); remaining > 0; {
			n := min(remaining, len(c.scratch))
			p, err = c.readFixed(n)
			if err != nil {
				if binaryEOF(err) {
					err = sqlError("22P04", "unexpected EOF in COPY data")
				}
				return false, c.diagnostic(err)
			}
			c.payload = append(c.payload, p...)
			remaining -= n
		}
		if wireInput {
			ctx = c.scope.op.Context()
		}
		dst[i], err = column.Usage.Type.BinaryInputWithEnvironment(ctx, c.payload, column.Usage.Typmod, c.scope.input)
		if err != nil {
			return false, c.diagnostic(err)
		}
		c.column = ""
	}
	if err := c.scope.op.pendingCopyInputError(); err != nil {
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
	if !errors.As(err, &record) || c.number == 0 {
		return err
	}
	prefix := fmt.Sprintf("COPY %s, line %d", c.binding.Relation.Name(), c.number)
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
	buffer = append(buffer, copyBinarySignature...)
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
