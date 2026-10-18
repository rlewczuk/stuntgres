package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

type copyField struct {
	decoded                   []byte
	start, end                int
	quoted, isNull, isDefault bool
}
type copyCursor struct {
	scope                        *StatementScope
	binding                      binding
	options                      copyOptions
	reader                       *bufio.Reader
	line, decoded                []byte
	fields                       []copyField
	defaults, omitted            []bool
	number                       int64
	eol                          byte
	ended, headerDone, lineValid bool
	column, value                string
	nullInput                    bool
}

func newCopyCursor(scope *StatementScope, b binding, o copyOptions, r io.Reader) *copyCursor {
	c := &copyCursor{scope: scope, binding: b, options: o, reader: bufio.NewReader(copyReader{scope, r}), defaults: make([]bool, len(b.relation.Columns)), omitted: make([]bool, len(b.relation.Columns))}
	for i := range c.omitted {
		c.omitted[i] = true
	}
	for _, target := range b.targets {
		c.omitted[target] = false
	}
	return c
}
func (c *copyCursor) Schema() []ColumnDescriptor { return nil }
func (c *copyCursor) Close() error               { return nil }
func (c *copyCursor) rowDefaults() []bool        { return c.defaults }
func (c *copyCursor) lineError(message string, hint string) error {
	d := sqlError("22P04", message).(*sqlstate.Error)
	d.Hint = hint
	return d
}
func (c *copyCursor) readLine() (present bool, err error) {
	if c.options.format == copyFormatCSV {
		return c.readCSVLine()
	}
	if c.ended {
		return false, nil
	}
	c.line = c.line[:0]
	c.lineValid = false
	c.column = ""
	c.number++
	for {
		b, readErr := c.reader.ReadByte()
		if readErr != nil {
			if readErr != io.EOF {
				return false, readErr
			}
			c.ended = true
			present = len(c.line) > 0
			break
		}
		if b == '\\' {
			next, nextErr := c.reader.ReadByte()
			if nextErr != nil {
				if nextErr != io.EOF {
					return false, nextErr
				}
				c.ended = true
				present = true
				break
			}
			if next == '.' {
				end, endErr := c.reader.ReadByte()
				if endErr != nil && endErr != io.EOF {
					return false, endErr
				}
				style := byte(0)
				if end == '\n' {
					style = 'n'
				} else if end == '\r' {
					style = 'r'
					if peek, peekErr := c.reader.Peek(1); peekErr == nil && peek[0] == '\n' {
						c.reader.ReadByte()
						style = 'c'
					}
				}
				if len(c.line) != 0 || style == 0 {
					return false, c.lineError("end-of-copy marker is not alone on its line", "")
				}
				if c.eol != 0 && style != c.eol {
					return false, c.lineError("end-of-copy marker does not match previous newline style", "")
				}
				c.ended = true
				return false, nil
			}
			c.line = append(c.line, b, next)
			continue
		}
		if b == '\r' || b == '\n' {
			style := byte('n')
			if b == '\r' {
				style = 'r'
				if peek, peekErr := c.reader.Peek(1); peekErr == nil && peek[0] == '\n' {
					c.reader.ReadByte()
					style = 'c'
				} else if peekErr != nil && peekErr != io.EOF {
					return false, peekErr
				}
			}
			if c.eol != 0 && c.eol != style {
				if b == '\r' {
					return false, c.lineError("literal carriage return found in data", `Use "\r" to represent carriage return.`)
				}
				return false, c.lineError("literal newline found in data", `Use "\n" to represent newline.`)
			}
			c.eol = style
			present = true
			break
		}
		c.line = append(c.line, b)
	}
	if err = sqltype.ValidateTextInput(string(c.line)); err != nil {
		return false, err
	}
	c.lineValid = true
	return present, nil
}
func copyHex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
func decodeCopyField(dst, raw []byte) []byte {
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b != '\\' {
			dst = append(dst, b)
			continue
		}
		i++
		if i == len(raw) {
			break
		}
		b = raw[i]
		switch b {
		case 'b':
			b = '\b'
		case 'f':
			b = '\f'
		case 'n':
			b = '\n'
		case 'r':
			b = '\r'
		case 't':
			b = '\t'
		case 'v':
			b = '\v'
		case 'x':
			if i+1 < len(raw) {
				if h, ok := copyHex(raw[i+1]); ok {
					i++
					b = h
					if i+1 < len(raw) {
						if h, ok := copyHex(raw[i+1]); ok {
							i++
							b = b*16 + h
						}
					}
				}
			}
		default:
			if b >= '0' && b <= '7' {
				b -= '0'
				for n := 1; n < 3 && i+1 < len(raw) && raw[i+1] >= '0' && raw[i+1] <= '7'; n++ {
					i++
					b = b*8 + raw[i] - '0'
				}
			}
		}
		dst = append(dst, b)
	}
	return dst
}
func (c *copyCursor) split(defaultMarkers bool) error {
	if c.options.format == copyFormatCSV {
		return c.splitCSV(defaultMarkers)
	}
	c.fields = c.fields[:0]
	c.decoded = c.decoded[:0]
	start := 0
	for i := 0; i <= len(c.line); i++ {
		if i < len(c.line) && c.line[i] == '\\' {
			i++
			continue
		}
		if i < len(c.line) && c.line[i] != c.options.delimiter {
			continue
		}
		raw := c.line[start:i]
		begin := len(c.decoded)
		isNull := string(raw) == c.options.null
		isDefault := defaultMarkers && c.options.hasDefault && string(raw) == c.options.def
		if isDefault && len(c.fields) < len(c.binding.targets) {
			target := c.binding.targets[len(c.fields)]
			if len(c.binding.defaults) == 0 || c.binding.defaults[target] == nil {
				d := c.lineError("unexpected default marker in COPY data", "").(*sqlstate.Error)
				d.Detail = fmt.Sprintf("Column \"%s\" has no default value.", c.binding.relation.Columns[target].Name)
				return d
			}
		}
		if !isNull && !isDefault {
			c.decoded = decodeCopyField(c.decoded, raw)
		}
		// Offsets rather than slices survive growth of the reusable decode buffer.
		c.fields = append(c.fields, copyField{start: begin, end: len(c.decoded), isNull: isNull, isDefault: isDefault})
		start = i + 1
	}
	for i := range c.fields {
		field := &c.fields[i]
		field.decoded = c.decoded[field.start:field.end]
		if err := sqltype.ValidateTextInput(string(c.fields[i].decoded)); err != nil {
			return err
		}
	}
	if len(c.binding.targets) == 0 && len(c.line) == 0 {
		c.fields = c.fields[:0]
	}
	return nil
}
func (c *copyCursor) header() error {
	_, err := c.readLine()
	if err != nil {
		return err
	}
	if c.options.header != 2 {
		return nil
	}
	if err = c.split(false); err != nil {
		return err
	}
	if len(c.fields) != len(c.binding.targets) {
		return c.lineError(fmt.Sprintf("wrong number of fields in header line: got %d, expected %d", len(c.fields), len(c.binding.targets)), "")
	}
	for i, field := range c.fields {
		name := c.binding.relation.Columns[c.binding.targets[i]].Name
		if field.isNull {
			return c.lineError(fmt.Sprintf("column name mismatch in header line field %d: got null value (\"%s\"), expected \"%s\"", i+1, c.options.null, name), "")
		}
		if string(field.decoded) != name {
			return c.lineError(fmt.Sprintf("column name mismatch in header line field %d: got \"%s\", expected \"%s\"", i+1, string(field.decoded), name), "")
		}
	}
	return nil
}
func (c *copyCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	wireInput := c.scope.op.copyInput.active.Load()
	if wireInput {
		ctx = c.scope.op.Context()
	}
	if err := ctx.Err(); err != nil {
		return false, c.diagnostic(err)
	}
	if !c.headerDone {
		c.headerDone = true
		if c.options.header != 0 {
			if err := c.header(); err != nil {
				return false, c.diagnostic(err)
			}
		}
	}
	present, err := c.readLine()
	if err != nil || !present {
		return false, c.diagnostic(err)
	}
	if wireInput {
		ctx = c.scope.op.Context()
	}
	if err = c.split(true); err != nil {
		return false, c.diagnostic(err)
	}
	if len(c.fields) > len(dst) {
		return false, c.diagnostic(c.lineError("extra data after last expected column", ""))
	}
	copy(c.defaults, c.omitted)
	for i, target := range c.binding.targets {
		column := c.binding.relation.Columns[target]
		if i >= len(c.fields) {
			return false, c.diagnostic(c.lineError(fmt.Sprintf("missing data for column \"%s\"", column.Name), ""))
		}
		field := c.fields[i]
		dst[i] = sqltype.Null(column.Usage.Type)
		isNull := field.isNull
		if c.options.format == copyFormatCSV {
			if isNull && copyForced(c.options.notNullFields, i) {
				isNull = false
			} else if field.quoted && copyForced(c.options.nullFields, i) && string(field.decoded) == c.options.null {
				isNull = true
			}
		}
		if isNull {
			c.column, c.nullInput = column.Name, true
			if err := column.Usage.Type.CheckDomainInput(ctx, dst[i], c.scope.input); err != nil {
				return false, c.diagnostic(err)
			}
			c.column, c.nullInput = "", false
			continue
		}
		if field.isDefault {
			c.defaults[target] = true
			continue
		}
		c.column = column.Name
		c.nullInput = false
		c.value = string(field.decoded)
		dst[i], err = column.Usage.Type.InputWithEnvironment(ctx, c.value, column.Usage.Typmod, c.scope.input)
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
func copyContextValue(text string) string {
	if len(text) <= 100 {
		return text
	}
	end := 100
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "..."
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
	result.Context = fmt.Sprintf("COPY %s, line %d", c.binding.relation.name, c.number)
	if c.column != "" {
		if c.nullInput {
			result.Context += fmt.Sprintf(", column %s: null input", c.column)
		} else {
			result.Context += fmt.Sprintf(", column %s: \"%s\"", c.column, copyContextValue(c.value))
		}
	} else if c.lineValid && record.Code != "23505" {
		result.Context += fmt.Sprintf(": \"%s\"", copyContextValue(string(c.line)))
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
	if errors.As(err, &primary) || !(s.protocolCommand || s.protocolSimple) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		cancellation := context.Cause(s.op.Context())
		if cancellation == nil {
			cancellation = context.Cause(s.op.ctx)
		}
		var cause *sqlstate.Error
		if errors.As(cancellation, &cause) && cause != nil && (cause.Code == "57014" || cause.Severity == "FATAL") {
			return cause
		}
	}
	return err
}
func appendCopyText(dst []byte, text string, delimiter byte) []byte {
	for i := range len(text) {
		b := text[i]
		switch b {
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\v':
			dst = append(dst, '\\', 'v')
		default:
			if b == delimiter {
				dst = append(dst, '\\')
			}
			dst = append(dst, b)
		}
	}
	return dst
}
func (s *StatementScope) copyOutput(rows RowResult, o copyOptions, w io.Writer) (count int64, err error) {
	if o.format == copyFormatBinary {
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
	if o.header != 0 {
		for i, column := range schema {
			if i != 0 {
				buffer = append(buffer, o.delimiter)
			}
			if o.format == copyFormatCSV {
				buffer = appendCopyCSV(buffer, column.Name, o, false, len(schema) == 1)
			} else {
				buffer = appendCopyText(buffer, column.Name, o.delimiter)
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
				buffer = append(buffer, o.delimiter)
			}
			if value.IsNull() {
				buffer = append(buffer, o.null...)
				continue
			}
			text, outputErr := value.Type().OutputWithEnvironment(s.op.Context(), value, output)
			if outputErr != nil {
				return 0, outputErr
			}
			if o.format == copyFormatCSV {
				buffer = appendCopyCSV(buffer, text, o, copyForced(o.quoteFields, i), len(schema) == 1)
			} else {
				buffer = appendCopyText(buffer, text, o.delimiter)
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
