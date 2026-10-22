package copyfmt

import (
	"context"
	"encoding/binary"
	"io"
	"math"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// writeBinary writes the binary file header, one tuple per row and the -1
// trailer.
func writeBinary(ctx context.Context, w io.Writer, rows RowSource, schema []catalog.Column, checkSend bool) (int64, error) {
	if len(schema) > math.MaxInt16 {
		return 0, sqlError("54000", "too many binary COPY columns")
	}
	if checkSend {
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
	buffer = append(buffer, BinarySignature...)
	buffer = binary.BigEndian.AppendUint32(buffer, 0)
	buffer = binary.BigEndian.AppendUint32(buffer, 0)
	if err := writeAll(w, buffer); err != nil {
		return 0, err
	}
	count := int64(0)
	for {
		present, err := rows.Next(ctx, values)
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
			buffer, err = v.Type().AppendBinaryWithEnvironment(ctx, buffer, v, output)
			if err != nil {
				return 0, err
			}
			n := len(buffer) - start - 4
			if n > math.MaxInt32 {
				return 0, sqlError("54000", "binary value is too large")
			}
			binary.BigEndian.PutUint32(buffer[start:], uint32(n))
		}
		if err := writeAll(w, buffer); err != nil {
			return 0, err
		}
		count++
	}
	buffer = buffer[:0]
	buffer = binary.BigEndian.AppendUint16(buffer, math.MaxUint16)
	if err := writeAll(w, buffer); err != nil {
		return 0, err
	}
	return count, nil
}
