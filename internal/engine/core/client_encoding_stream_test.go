package core_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestClientEncodingStream(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SET client_encoding='LATIN1'")
	input := func(id sqltype.ID, text string) sqltype.Value {
		d := enginetest.Usage(e, id).Type
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	text := enginetest.Usage(e, sqltype.Text).Type
	values := []sqltype.Value{input(sqltype.Text, "é"), input(sqltype.Text, strings.Repeat("ä", 8192)), input(sqltype.JSONB, `"é"`), input(1009, `{é,äö}`), input(sqltype.Bytea, `\xff00e9`), input(sqltype.Int4, "42"), sqltype.Null(text), input(sqltype.Text, "")}
	formats := []int16{0, 0, 1, 1, 1, 1, 0, 0}
	decode := func(raw string) []byte {
		p, err := hex.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	want := [][]byte{{0xe9}, bytes.Repeat([]byte{0xe4}, 8192), {1, '"', 0xe9, '"'}, decode("000000010000000000000019000000020000000100000001e900000002e4f6"), {0xff, 0, 0xe9}, {0, 0, 0, 42}, nil, {}}
	fields := make([]session.ProtocolField, 0, len(values))
	scratch := make([]byte, 0, 1)
	env := sqltype.OutputEnvironment{Transport: s.Settings.ClientEncoding()}
	for range 3 {
		var err error
		fields, scratch, err = session.EncodeProtocolRow(context.Background(), values, formats, env, fields, scratch)
		if err != nil || len(fields) != len(want) {
			t.Fatal("encoding row", err, len(fields))
		}
		offset := 0
		for i, field := range fields {
			if field.Null != (i == 6) || !bytes.Equal(field.Bytes, want[i]) {
				t.Fatalf("field%d null%t bytes%x want%x", i, field.Null, field.Bytes, want[i])
			}
			if len(field.Bytes) > 0 && unsafe.SliceData(field.Bytes) != unsafe.SliceData(scratch[offset:]) {
				t.Fatalf("field%d retained pre-growth scratch", i)
			}
			offset += len(want[i])
		}
		if offset != len(scratch) {
			t.Fatal("encoded byte count", offset, len(scratch))
		}
	}
	for _, c := range []struct {
		encoding, raw, code string
		id                  sqltype.ID
		textInput           bool
	}{
		{"GB18030", "\x84\x31\xa5\x30", "22P05", sqltype.Text, false},
		{"UTF8", "\xff", "22021", sqltype.Text, false},
		{"LATIN1", "oops", "22P02", sqltype.Int4, true},
		{"LATIN1", "", "22P02", sqltype.Int4, true},
		{"LATIN1", "\xe9", "", sqltype.Text, true},
	} {
		enginetest.SessionSQL(t, s, "SET client_encoding='"+c.encoding+"'")
		data := make([]byte, 4+len(c.raw))
		binary.BigEndian.PutUint32(data, uint32(len(c.raw)))
		copy(data[4:], c.raw)
		var log session.ProtocolContext
		frame := make([]sqltype.Value, 1)
		env := sqltype.InputEnvironment{Transport: s.Settings.ClientEncoding()}
		err := session.ImportProtocolParameters(context.Background(), sqltype.OutputEnvironment{}, &protocolUnitInput{data: data}, []int16{0}, 1, []sqltype.Usage{enginetest.Usage(e, c.id)}, frame, env, &log, session.ProtocolBindInput)
		var diagnostic *sqlstate.Error
		if c.code == "" {
			if err != nil {
				t.Fatal("valid text import", err)
			}
		} else if !errors.As(err, &diagnostic) || diagnostic.Code != c.code {
			t.Fatal("parameter input phase", c.encoding, c.raw, err)
		}
		if len(log.Parameters) != 1 || log.Parameters[0].TextInput != c.textInput || log.Parameters[0].Decoded != (c.code == "") {
			t.Fatal("parameter conversion/type-input phase conflated", c, log)
		}
		want := c.raw
		if c.code == "" {
			want = "é"
		}
		if len(c.raw) > 0 {
			data[4] ^= 0xff
		}
		if string(log.Parameters[0].Bytes) != want {
			t.Fatal("parameter log borrowed raw message or lost canonical text", c, log)
		}
	}
}
