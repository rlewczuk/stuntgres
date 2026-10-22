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
// signature is written. On error the count is 0.
func WriteRows(ctx context.Context, w io.Writer, rows RowSource, schema []catalog.Column, o Options, checkSend bool) (int64, error) {
	if o.Format == Binary {
		return writeBinary(ctx, w, rows, schema, checkSend)
	}
	return writeText(ctx, w, rows, schema, o)
}

// writeText writes text or CSV output, with the HEADER line first when set.
func writeText(ctx context.Context, w io.Writer, rows RowSource, schema []catalog.Column, o Options) (count int64, err error) {
	values := make([]sqltype.Value, len(schema))
	buffer := make([]byte, 0, 4096)
	if o.Header != 0 {
		for i, column := range schema {
			if i != 0 {
				buffer = append(buffer, o.Delimiter)
			}
			if o.Format == CSV {
				buffer = AppendCSV(buffer, column.Name, o, false, len(schema) == 1)
			} else {
				buffer = AppendText(buffer, column.Name, o.Delimiter)
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
				buffer = append(buffer, o.Null...)
				continue
			}
			text, outputErr := value.Type().OutputWithEnvironment(ctx, value, output)
			if outputErr != nil {
				return 0, outputErr
			}
			if o.Format == CSV {
				buffer = AppendCSV(buffer, text, o, Forced(o.QuoteFields, i), len(schema) == 1)
			} else {
				buffer = AppendText(buffer, text, o.Delimiter)
			}
		}
		buffer = append(buffer, '\n')
		if err = writeAll(w, buffer); err != nil {
			return 0, err
		}
		count++
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
