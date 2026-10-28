package copyfmt

import (
	"context"
	"io"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// RowSource is the row demand of a COPY TO: Next fills one row of values and
// OutputEnvironment gives the text and binary output settings for it.
type RowSource interface {
	Next(context.Context, []sqltype.Value) (bool, error)
	OutputEnvironment() sqltype.OutputEnvironment
}

// WriteRows encodes every row of rows in o's format to w, one Write per line
// or tuple, and returns the row count. schema describes the row values. For
// binary output, checkSend resolves each column's send provider before the
// signature is written. completed, when nonnil, runs after each whole data-row
// write, never for framing or a failed/short write. On error the count is 0.
func WriteRows(ctx context.Context, w io.Writer, rows RowSource, schema []catalog.Column, o Options, checkSend bool, completed func()) (int64, error) {
	if o.Format == Binary {
		return writeBinary(ctx, w, rows, schema, checkSend, completed)
	}
	return writeText(ctx, w, rows, schema, o, completed)
}

// writeText writes text or CSV output, with the HEADER line first when set.
func writeText(ctx context.Context, w io.Writer, rows RowSource, schema []catalog.Column, o Options, completed func()) (count int64, err error) {
	values := make([]sqltype.Value, len(schema))
	buffer := make([]byte, 0, 4096)
	var encoded []byte
	nullText := o.Null
	if o.Transport {
		nullText, err = o.Conversion.Convert(ctx, o.Null)
		if err != nil {
			return 0, err
		}
	}
	if o.Header != 0 {
		for i, column := range schema {
			if i != 0 {
				buffer = append(buffer, o.Delimiter)
			}
			quoteNull := column.Name == o.Null
			text, next, encodeErr := o.encodedField(ctx, column.Name, encoded)
			encoded = next
			if encodeErr != nil {
				return 0, encodeErr
			}
			if o.Format == CSV {
				buffer = appendCSVEncoding(buffer, text, o, false, len(schema) == 1, quoteNull)
			} else {
				buffer = appendTextEncoding(buffer, text, o)
			}
		}
		buffer = append(buffer, '\n')
		if err = writeAll(w, buffer); err != nil {
			return 0, err
		}
	}
	for {
		present, nextErr := rows.Next(ctx, values)
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
				buffer = append(buffer, nullText...)
				continue
			}
			text, outputErr := value.Type().OutputWithEnvironment(ctx, value, output)
			if outputErr != nil {
				return 0, outputErr
			}
			quoteNull := text == o.Null
			text, encoded, err = o.encodedField(ctx, text, encoded)
			if err != nil {
				return 0, err
			}
			if o.Format == CSV {
				buffer = appendCSVEncoding(buffer, text, o, Forced(o.QuoteFields, i), len(schema) == 1, quoteNull)
			} else {
				buffer = appendTextEncoding(buffer, text, o)
			}
		}
		buffer = append(buffer, '\n')
		if err = writeAll(w, buffer); err != nil {
			return 0, err
		}
		count++
		if completed != nil {
			completed()
		}
	}
	return count, nil
}

// writeAll writes p in one call; a short count without an error is
// io.ErrShortWrite.
func writeAll(w io.Writer, p []byte) error {
	n, err := w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return err
}
