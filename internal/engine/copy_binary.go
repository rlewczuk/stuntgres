package engine

import (
	"encoding/binary"
	"io"
	"math"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

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
