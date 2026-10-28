package session

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

// ProtocolInput reads a Bind or FunctionCall message body.
type ProtocolInput interface {
	Uint16() (uint16, error)
	Int32() (int32, error)
	Bytes(int) ([]byte, error)
	End() error
}
type ProtocolLogParameter struct {
	OID       uint32
	Type      string
	Format    int16
	Null      bool
	Decoded   bool
	TextInput bool // Client decoding succeeded and canonical text input began.
	Bytes     []byte
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

func ProtocolFormats(input ProtocolInput) ([]int16, error) {
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
func ProtocolFormat(formats []int16, i int) int16 {
	if len(formats) == 0 {
		return 0
	}
	if len(formats) == 1 {
		return formats[0]
	}
	return formats[i]
}
func ProtocolResultFormats(input ProtocolInput, columns int) ([]int16, error) {
	formats, err := ProtocolFormats(input)
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
		f := ProtocolFormat(formats, i)
		if f != 0 && f != 1 {
			return nil, sqlError("22023", fmt.Sprintf("unsupported format code: %d", f))
		}
		expanded[i] = f
	}
	return expanded, nil
}

type ProtocolReceiveKind uint8

const (
	ProtocolBindInput ProtocolReceiveKind = iota
	ProtocolFunctionInput
)

// ImportProtocolParameters decodes count parameters of the given types into
// frame and records each in log. It reads only its inputs and takes no lock.
func ImportProtocolParameters(ctx context.Context, output sqltype.OutputEnvironment, input ProtocolInput, formats []int16, count int, types []sqltype.Usage, frame []sqltype.Value, env sqltype.InputEnvironment, log *ProtocolContext, kind ProtocolReceiveKind) error {
	env.ByteTransport = true
	for i := range count {
		n, err := input.Int32()
		if err != nil {
			return err
		}
		if n < -1 {
			if kind == ProtocolFunctionInput {
				return sqlError("08P01", fmt.Sprintf("invalid argument size %d in function call message", n))
			}
			return sqlError("08P01", "invalid message format")
		}
		parameter := ProtocolLogParameter{OID: uint32(types[i].Type.ID()), Type: types[i].Type.DiagnosticName(), Format: ProtocolFormat(formats, i), Null: n == -1}
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
				value = sqltype.Null(types[i].Type)
				err = types[i].Type.CheckDomainInput(ctx, value, parameterInput)
			} else {
				var text string
				text, err = env.Transport.Decode(ctx, string(raw))
				if err == nil {
					log.Parameters[i].TextInput = true
					value, err = types[i].Type.InputWithEnvironment(ctx, text, -1, parameterInput)
				}
			}
		case 1:
			if parameter.Null {
				if err = types[i].Type.CheckBinaryInput(); err == nil {
					value = sqltype.Null(types[i].Type)
					err = types[i].Type.CheckDomainInput(ctx, value, parameterInput)
				}
			} else {
				value, err = types[i].Type.BinaryInputWithEnvironment(ctx, raw, -1, parameterInput)
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
		text, outputErr := value.Type().OutputWithEnvironment(ctx, value, output)
		if outputErr == nil {
			entry.Decoded = true
			entry.Bytes = []byte(text)
		} else {
			entry.Bytes = slices.Clone(raw)
		}
	}
	return nil
}

// EncodeProtocolRow encodes one result row into fields, reusing fields and
// scratch. It runs once per row.
func EncodeProtocolRow(ctx context.Context, values []sqltype.Value, formats []int16, env sqltype.OutputEnvironment, fields []ProtocolField, scratch []byte) ([]ProtocolField, []byte, error) {
	env.ByteTransport = true
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
		if ProtocolFormat(formats, i) == 1 {
			scratch, err = v.Type().AppendBinaryWithEnvironment(ctx, scratch, v, env)
		} else {
			var text string
			text, err = v.Type().OutputWithEnvironment(ctx, v, env)
			if err == nil {
				scratch, err = env.AppendTransport(ctx, scratch, text)
			}
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
