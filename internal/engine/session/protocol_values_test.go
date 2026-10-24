package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// codecInput is a wire message body; the engine's protocol tests drive the
// staged consumers.
type codecInput struct {
	data     []byte
	position int
}

func (r *codecInput) Bytes(n int) ([]byte, error) {
	if n < 0 || n > len(r.data)-r.position {
		return nil, sqlError("08P01", "insufficient data left in message")
	}
	p := r.data[r.position : r.position+n]
	r.position += n
	return p, nil
}
func (r *codecInput) Uint16() (uint16, error) {
	p, err := r.Bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(p), nil
}
func (r *codecInput) Int32() (int32, error) {
	p, err := r.Bytes(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(p)), nil
}
func (r *codecInput) End() error {
	if r.position != len(r.data) {
		return sqlError("08P01", "invalid message format")
	}
	return nil
}

func codecText(t *testing.T) sqltype.Usage {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			return d.Usage()
		}
	}
	t.Fatal("missing text type")
	return sqltype.Usage{}
}

func TestProtocolCodecEncodeRow(t *testing.T) {
	usage := codecText(t)
	empty := sqltype.String(usage.Type, "")
	large := sqltype.String(usage.Type, string(bytes.Repeat([]byte{'x'}, 4096)))
	values := []sqltype.Value{empty, large, sqltype.Null(usage.Type)}
	fields, scratch, err := EncodeProtocolRow(context.Background(), values, []int16{0}, sqltype.OutputEnvironment{}, nil, make([]byte, 0, 1))
	if err != nil || len(fields) != 3 || fields[0].Null || len(fields[0].Bytes) != 0 || !bytes.Equal(fields[1].Bytes, bytes.Repeat([]byte{'x'}, 4096)) || !fields[2].Null {
		t.Fatal("scratch growth or NULL/empty identity lost", fields, err)
	}
	fields, scratch, err = EncodeProtocolRow(context.Background(), []sqltype.Value{large, empty}, []int16{1}, sqltype.OutputEnvironment{}, fields, scratch)
	if err != nil || fields[0].Null || len(fields[0].Bytes) != 4096 || fields[1].Null || len(fields[1].Bytes) != 0 || len(scratch) != 4096 {
		t.Fatal("reused output scratch lost field boundaries", err)
	}
}

func TestProtocolCodecResultFormats(t *testing.T) {
	// Invalid result selectors are used only when there is an actual column.
	input := &codecInput{data: []byte{0, 1, 0, 9}}
	if got, err := ProtocolResultFormats(input, 0); err != nil || len(got) != 0 {
		t.Fatal("unused result selector was evaluated", got, err)
	}
}

func TestProtocolCodecImportParameters(t *testing.T) {
	usage := codecText(t)
	// One text value "ab" and one NULL, both in text format.
	input := &codecInput{data: []byte{0, 0, 0, 2, 'a', 'b', 0xff, 0xff, 0xff, 0xff}}
	frame := make([]sqltype.Value, 2)
	var log ProtocolContext
	err := ImportProtocolParameters(context.Background(), sqltype.OutputEnvironment{}, input, nil, 2, []sqltype.Usage{usage, usage}, frame, sqltype.InputEnvironment{}, &log, ProtocolBindInput)
	if err != nil || input.End() != nil {
		t.Fatal("parameters not consumed", err)
	}
	if frame[0].IsNull() || !frame[1].IsNull() || len(log.Parameters) != 2 {
		t.Fatal("decoded frame or log lost a parameter", frame, log)
	}
	if p := log.Parameters[0]; !p.Decoded || string(p.Bytes) != "ab" || p.OID != uint32(sqltype.Text) || p.Null {
		t.Fatal("text parameter log lost its decoded value", p)
	}
	if p := log.Parameters[1]; !p.Decoded || !p.Null || p.Bytes != nil {
		t.Fatal("NULL parameter log kept bytes", p)
	}
}

func TestProtocolCodecHasRows(t *testing.T) {
	if !ProtocolHasRows(&parser.Select{}, nil) || ProtocolHasRows(&parser.CreateTable{}, nil) {
		t.Fatal("zero-column rows confused with NoData")
	}
}
