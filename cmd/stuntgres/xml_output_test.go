package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Exercises the active codec/notice/framing mechanics used by simple and portal
// sends. Reservation ownership is covered by TestClientEncodingNoticeOwnership.
func TestXMLServerActiveOutput(t *testing.T) {
	var d *sqltype.Descriptor
	for _, candidate := range sqltype.Builtins() {
		if candidate.ID() == sqltype.XML {
			d = candidate
			break
		}
	}
	if d == nil {
		t.Fatal("missing XML descriptor")
	}
	parse := func(text string) sqltype.Value {
		t.Helper()
		v, err := d.InputWithEnvironment(context.Background(), text, -1, sqltype.InputEnvironment{XMLMode: sqltype.XMLDocument})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	warning := parse(`<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`)
	untranslatable := parse("<a>中</a>")
	for _, tc := range []struct {
		name, encoding, tags string
		threshold            session.NoticeLevel
		values               []sqltype.Value
		code                 string
	}{
		{"active row", "UTF8", "ND", session.LevelWarning, []sqltype.Value{warning}, ""},
		{"warning before later failure", "LATIN1", "N", session.LevelWarning, []sqltype.Value{warning, untranslatable}, "22P05"},
		{"captured suppression", "LATIN1", "", session.LevelError, []sqltype.Value{warning, untranslatable}, "22P05"},
		{"NULL", "UTF8", "D", session.LevelWarning, []sqltype.Value{sqltype.Null(d)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := make([]catalog.Column, len(tc.values))
			for i := range schema {
				schema[i] = catalog.Column{Name: "x", Usage: d.Usage()}
			}
			rows := exec.NewFinalRows(schema, 1)
			rows.Values = tc.values
			in, out := sqltype.EncodingConversionHandler{}, sqltype.EncodingConversionHandler{}
			if tc.encoding == "LATIN1" {
				in, _ = sqltype.RegisteredEncodingConversion("iso8859_1_to_utf8")
				out, _ = sqltype.RegisteredEncodingConversion("utf8_to_iso8859_1")
			}
			policy, err := sqltype.NewClientEncodingPolicy(sqltype.EncodingID(tc.encoding), in, out)
			if err != nil {
				t.Fatal(err)
			}
			rows.Output.Transport = policy
			op := &session.Operation{NoticeThreshold: tc.threshold}
			conn := framingInput(nil)
			client := &client{wire: newWire(conn)}
			client.wire.encoding, client.wire.encodingReady = policy, true
			// Attach only to this send's copy, not retained presentation. The
			// admitted threshold belongs to the operation, not later settings.
			output := rows.OutputEnvironment()
			output.XMLNotice = func(d sqlstate.Error) { op.EmitNotice(session.LevelWarning, d) }
			fields, _, logical := session.EncodeProtocolRow(context.Background(), rows.Values, nil, output, nil, nil)
			noticeErr, transport := client.writeNotices(op)
			if noticeErr != nil {
				t.Fatal(noticeErr)
			}
			if logical == nil && transport == nil {
				var buffer []byte
				logical, transport = client.writeFields(fields, &buffer)
			}
			if transport != nil {
				t.Fatal(transport)
			}
			var diagnostic *sqlstate.Error
			if tc.code == "" {
				if logical != nil {
					t.Fatal(logical)
				}
			} else if !errors.As(logical, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatalf("logical=%v diagnostic=%#v", logical, diagnostic)
			}
			if rows.Output.XMLNotice != nil {
				t.Fatal("active output installed a retained sink")
			}
			if len(op.DetachNotices()) != 0 {
				t.Fatal("output notices were not drained")
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if err := client.wire.flush(); err != nil {
				t.Fatal(err)
			}
			data := conn.output.Bytes()
			var tags []byte
			for len(data) != 0 {
				if len(data) < 5 {
					t.Fatal("short frame")
				}
				n := int(binary.BigEndian.Uint32(data[1:5])) + 1
				if n < 5 || n > len(data) {
					t.Fatal("invalid frame length")
				}
				tags = append(tags, data[0])
				if data[0] == 'N' && (!bytes.Contains(data[:n], []byte("XX001")) || !bytes.Contains(data[:n], []byte("Space required."))) {
					t.Fatalf("notice payload=%q", data[:n])
				}
				data = data[n:]
			}
			if string(tags) != tc.tags {
				t.Fatalf("frame order=%q want=%q", tags, tc.tags)
			}
		})
	}
}
