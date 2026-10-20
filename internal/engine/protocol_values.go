package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/sqltype"
)

type ProtocolInput interface {
	Uint16() (uint16, error)
	Int32() (int32, error)
	Bytes(int) ([]byte, error)
	End() error
}
type ProtocolLogParameter struct {
	OID     uint32
	Type    string
	Format  int16
	Null    bool
	Decoded bool
	Bytes   []byte
}
type ProtocolContext struct {
	Source     string
	Parameters []ProtocolLogParameter
}
type ProtocolField struct {
	Null       bool
	Bytes      []byte
	start, end int
}

func protocolFormats(input ProtocolInput) ([]int16, error) {
	n, err := input.Uint16()
	if err != nil {
		return nil, err
	}
	var formats []int16
	for range n {
		f, err := input.Uint16()
		if err != nil {
			return nil, err
		}
		formats = append(formats, int16(f))
	}
	return formats, nil
}
func protocolFormat(formats []int16, i int) int16 {
	if len(formats) == 0 {
		return 0
	}
	if len(formats) == 1 {
		return formats[0]
	}
	return formats[i]
}
func protocolResultFormats(input ProtocolInput, columns int) ([]int16, error) {
	formats, err := protocolFormats(input)
	if err != nil {
		return nil, err
	}
	if len(formats) > 1 && len(formats) != columns {
		return nil, sqlError("08P01", fmt.Sprintf("bind message has %d result formats but query has %d columns", len(formats), columns))
	}
	if err = input.End(); err != nil {
		return nil, err
	}
	expanded := make([]int16, columns)
	for i := range expanded {
		f := protocolFormat(formats, i)
		if f != 0 && f != 1 {
			return nil, sqlError("22023", fmt.Sprintf("unsupported format code: %d", f))
		}
		expanded[i] = f
	}
	return expanded, nil
}

type protocolReceiveKind uint8

const (
	protocolBindInput protocolReceiveKind = iota
	protocolFunctionInput
)

func (s *StatementScope) protocolImport(input ProtocolInput, formats []int16, formal analyze.ParameterInfo, frame []sqltype.Value, env sqltype.InputEnvironment, log *ProtocolContext, kind protocolReceiveKind) error {
	for i := range formal.Count {
		n, err := input.Int32()
		if err != nil {
			return err
		}
		if n < -1 {
			if kind == protocolFunctionInput {
				return sqlError("08P01", fmt.Sprintf("invalid argument size %d in function call message", n))
			}
			return sqlError("08P01", "invalid message format")
		}
		parameter := ProtocolLogParameter{OID: uint32(formal.Types[i].Type.ID()), Type: formal.Types[i].Type.DiagnosticName(), Format: protocolFormat(formats, i), Null: n == -1}
		var raw []byte
		if n >= 0 {
			raw, err = input.Bytes(int(n))
			if err != nil {
				return err
			}
			parameter.Bytes = raw
		}
		log.Parameters = append(log.Parameters, parameter)
		parameterInput := env.ParameterInput(i+1, len(raw) == 0)
		var value sqltype.Value
		switch parameter.Format {
		case 0:
			if parameter.Null {
				value = sqltype.Null(formal.Types[i].Type)
				err = formal.Types[i].Type.CheckDomainInput(s.op.Context(), value, parameterInput)
			} else {
				value, err = formal.Types[i].Type.InputWithEnvironment(s.op.Context(), string(raw), -1, parameterInput)
			}
		case 1:
			if parameter.Null {
				if err = formal.Types[i].Type.CheckBinaryInput(); err == nil {
					value = sqltype.Null(formal.Types[i].Type)
					err = formal.Types[i].Type.CheckDomainInput(s.op.Context(), value, parameterInput)
				}
			} else {
				value, err = formal.Types[i].Type.BinaryInputWithEnvironment(s.op.Context(), raw, -1, parameterInput)
			}
		default:
			log.Parameters[i].Bytes = slices.Clone(raw)
			return sqlError("22023", fmt.Sprintf("unsupported format code: %d", parameter.Format))
		}
		if err != nil {
			log.Parameters[i].Bytes = slices.Clone(raw)
			return err
		}
		frame[i] = value
		entry := &log.Parameters[i]
		if value.IsNull() {
			entry.Decoded = true
			entry.Bytes = nil
			continue
		}
		text, outputErr := value.Type().OutputWithEnvironment(s.op.Context(), value, s.output)
		if outputErr == nil {
			entry.Decoded = true
			entry.Bytes = []byte(text)
		} else {
			entry.Bytes = slices.Clone(raw)
		}
	}
	return nil
}
func encodeProtocolRow(ctx context.Context, values []sqltype.Value, formats []int16, env sqltype.OutputEnvironment, fields []ProtocolField, scratch []byte) ([]ProtocolField, []byte, error) {
	if cap(fields) < len(values) {
		fields = make([]ProtocolField, len(values))
	} else {
		fields = fields[:len(values)]
	}
	scratch = scratch[:0]
	// Offsets avoid stale slices when a later field grows the reusable buffer.
	for i, v := range values {
		fields[i].Null = v.IsNull()
		fields[i].start = len(scratch)
		if v.IsNull() {
			fields[i].end = len(scratch)
			continue
		}
		var err error
		if protocolFormat(formats, i) == 1 {
			scratch, err = v.Type().AppendBinaryWithEnvironment(ctx, scratch, v, env)
		} else {
			var text string
			text, err = v.Type().OutputWithEnvironment(ctx, v, env)
			scratch = append(scratch, text...)
		}
		if err != nil {
			return fields, scratch, err
		}
		fields[i].end = len(scratch)
	}
	for i := range fields {
		fields[i].Bytes = scratch[fields[i].start:fields[i].end]
	}
	return fields, scratch, nil
}
