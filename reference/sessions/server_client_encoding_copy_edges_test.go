//go:build !noservertests

package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func clientEncodingStreamEdges(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	exec := func(q string) {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	zero := func() {
		var n int
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM ce_stream").Scan(&n); err != nil || n != 0 {
			t.Fatalf("failed COPY prefix %d %v", n, err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT 42").Scan(&n); err != nil || n != 42 {
			t.Fatalf("same owner %d %v", n, err)
		}
	}
	exec("SET client_encoding='LATIN1'")
	exec("TRUNCATE ce_stream")
	r, err := fixture.CopyFrom(p.Ctx, c, b.PG, `COPY ce_stream FROM STDIN (FORMAT csv, HEADER MATCH, NULL 'nil')`, &clientEncodingChunks{[][]byte{[]byte("v\n\xe9\nnil\n\"\"\n\"nil\"\n\"a,\"\"b\"\n")}})
	if err != nil || r != (stuntgres.CopyResult{Count: 5, CommandTag: "COPY 5"}) {
		t.Fatalf("CSV %+v %v", r, err)
	}
	var out bytes.Buffer
	r, err = clientEncodingStreamTo(p, c, b.PG, `COPY (SELECT v FROM ce_stream ORDER BY v NULLS FIRST) TO STDOUT (FORMAT csv, HEADER, NULL 'nil')`, &out)
	if err != nil || r.Count != 5 || r.CommandTag != "COPY 5" || out.String() != "v\nnil\n\n\"a,\"\"b\"\n\"nil\"\n\xe9\n" {
		t.Fatalf("CSV output %x %+v %v", out.Bytes(), r, err)
	}
	for _, v := range []struct{ encoding, raw, code, message, context string }{
		{"LATIN1", "ok\n\\351\n", "22021", `invalid byte sequence for encoding "UTF8": 0xe9`, "COPY ce_stream, line 2: \"\\351\""},
		{"SJIS", "ok\n\x82", "22021", `invalid byte sequence for encoding "SJIS": 0x82`, "COPY ce_stream, line 2"},
		{"SJIS", "ok\n\x82\x20\n", "22021", `invalid byte sequence for encoding "SJIS": 0x82 0x20`, "COPY ce_stream, line 2"},
		{"LATIN1", "ok\n\x00\n", "22021", `invalid byte sequence for encoding "LATIN1": 0x00`, "COPY ce_stream, line 2"},
		{"GB18030", "ok\n\x84\x31\xa5\x30\n", "22P05", `character with byte sequence 0x84 0x31 0xa5 0x30 in encoding "GB18030" has no equivalent in encoding "UTF8"`, "COPY ce_stream, line 2"},
	} {
		for _, ignore := range []string{"", ", ON_ERROR ignore"} {
			exec("TRUNCATE ce_stream")
			exec("SET client_encoding='" + v.encoding + "'")
			r, err = fixture.CopyFrom(p.Ctx, c, b.PG, "COPY ce_stream FROM STDIN (FORMAT text"+ignore+")", strings.NewReader(v.raw))
			if r != (stuntgres.CopyResult{}) {
				t.Fatalf("failed input published %+v", r)
			}
			want := fixture.ControlsReferenceDiagnostic(v.code, v.message)
			want.Context = v.context
			fixture.AssertDiagnostic(t, err, &want)
			zero()
		}
	}
	exec("TRUNCATE ce_stream")
	exec("SET client_encoding='SJIS'")
	r, err = fixture.CopyFrom(p.Ctx, c, b.PG, "COPY ce_stream FROM STDIN", bytes.NewReader([]byte("ok\n\\.\n\x82")))
	if err != nil || r.Count != 1 || r.CommandTag != "COPY 1" {
		t.Fatalf("end marker prefetched tail %+v %v", r, err)
	}
	exec("TRUNCATE ce_stream")
	exec("SET client_encoding='LATIN1'")
	exec(`INSERT INTO ce_stream VALUES('ok'),(U&'\20AC')`)
	out.Reset()
	r, err = clientEncodingStreamTo(p, c, b.PG, "COPY (SELECT v FROM ce_stream ORDER BY v) TO STDOUT", &out)
	if r != (stuntgres.CopyResult{}) || out.String() != "ok\n" {
		t.Fatalf("late output row/count %x %+v %v", out.Bytes(), r, err)
	}
	want := fixture.ControlsReferenceDiagnostic("22P05", clientEncodingEuroError)
	fixture.AssertDiagnostic(t, err, &want)
	for _, v := range clientEncodingCopyOptionCases {
		q := "COPY ce_stream TO STDOUT (" + v.option + ")"
		out.Reset()
		r, err = clientEncodingStreamTo(p, c, b.PG, q, &out)
		if r != (stuntgres.CopyResult{}) || out.Len() != 0 {
			t.Fatalf("option error published %x %+v", out.Bytes(), r)
		}
		want := fixture.ControlsReferenceDiagnostic(v.code, v.message)
		want.Position = clientEncodingCopyOptionPosition(q, v.positionToken)
		fixture.AssertDiagnostic(t, err, &want)
	}
	for _, v := range clientEncodingCopyOutputCases {
		out.Reset()
		r, err = clientEncodingStreamTo(p, c, b.PG, v.q, &out)
		if err != nil || r.Count != v.count || r.CommandTag != "COPY 1" || !bytes.Equal(out.Bytes(), v.want) {
			t.Fatalf("header/null/escaping %x want %x %+v %v", out.Bytes(), v.want, r, err)
		}
	}
	exec("CREATE TABLE ce_stream_binary(v text,b bytea,n int)")
	for _, option := range []string{"", ", ENCODING 'UTF8'", ", ENCODING 'SJIS'"} {
		exec("TRUNCATE ce_stream_binary")
		payload := serverCopyBinary([][]byte{{0xe9}, {0xff, 0, 0xe9}, {0, 0, 0, 42}})
		r, err = fixture.CopyFrom(p.Ctx, c, b.PG, "COPY ce_stream_binary FROM STDIN (FORMAT binary"+option+")", &clientEncodingChunks{[][]byte{payload[:20], payload[20:]}})
		if err != nil || r != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) {
			t.Fatalf("binary input %+v %v", r, err)
		}
		out.Reset()
		r, err = clientEncodingStreamTo(p, c, b.PG, "COPY ce_stream_binary TO STDOUT (FORMAT binary"+option+")", &out)
		if err != nil || r != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) || !bytes.Equal(out.Bytes(), payload) {
			t.Fatalf("binary output %x want %x %+v %v", out.Bytes(), payload, r, err)
		}
	}
}
