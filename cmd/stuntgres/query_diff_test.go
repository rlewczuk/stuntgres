package main

import (
	"encoding/base64"
	"fmt"
	"slices"
	"testing"

	"stuntgres/internal/sqlstate"
)

// diffResponse builds decoded backend messages the way query.log records them.
type diffResponse struct {
	messages []queryMessage
	formats  []int16
}

func (r *diffResponse) add(tag byte, body []byte) *diffResponse {
	name, decoded := decodeBackend(tag, body, &r.formats)
	r.messages = append(r.messages, queryMessage{Type: string(tag), Name: name, Data: base64.StdEncoding.EncodeToString(body), Decoded: decoded})
	return r
}

func (r *diffResponse) columns(types ...uint32) *diffResponse {
	body := appendInt16(nil, int16(len(types)))
	for i, typ := range types {
		body = appendString(body, fmt.Sprintf("c%d", i))
		body = appendInt32(body, 0)
		body = appendInt16(body, 0)
		body = appendInt32(body, typ)
		body = appendInt16(body, -1)
		body = appendInt32(body, 0xffffffff)
		body = appendInt16(body, 0)
	}
	return r.add('T', body)
}

func (r *diffResponse) row(values ...string) *diffResponse {
	body := appendInt16(nil, int16(len(values)))
	for _, value := range values {
		body = append(appendInt32(body, uint32(len(value))), value...)
	}
	return r.add('D', body)
}

func (r *diffResponse) complete(tag string) *diffResponse { return r.add('C', appendString(nil, tag)) }
func (r *diffResponse) ready() *diffResponse              { return r.add('Z', []byte{'I'}) }
func (r *diffResponse) diagnostic(tag byte, d sqlstate.Error) *diffResponse {
	return r.add(tag, diagnosticBody(d, "ERROR"))
}
func (r *diffResponse) notification(pid uint32, channel, payload string) *diffResponse {
	return r.add('A', appendString(appendString(appendInt32(nil, pid), channel), payload))
}

func TestServerQueryLogDiff(t *testing.T) {
	selectOne := func() *diffResponse { return (&diffResponse{}).columns(23).row("1").complete("SELECT 1").ready() }
	missing := sqlstate.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: "15", File: "parse_relation.c", Line: "1449", Routine: "parserOpenTable"}
	for _, tc := range []struct {
		name              string
		native, reference *diffResponse
		want              []string
	}{
		{"equal", selectOne(), selectOne(), nil},
		{"server_specific_fields_ignored",
			(&diffResponse{}).diagnostic('E', sqlstate.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}).notification(7, "ch", "x").ready(),
			(&diffResponse{}).diagnostic('E', missing).notification(901, "ch", "x").ready(), nil},
		{"value",
			selectOne(), (&diffResponse{}).columns(23).row("2").complete("SELECT 1").ready(),
			[]string{`#1 DataRow values[0]: stuntgres "1", reference "2"`}},
		{"column_type",
			(&diffResponse{}).columns(23, 25).row("1", "x").complete("SELECT 1").ready(),
			(&diffResponse{}).columns(23, 1043).row("1", "x").complete("SELECT 1").ready(),
			[]string{"#0 RowDescription columns[1].type_oid: stuntgres 25, reference 1043"}},
		{"error_instead_of_rows",
			selectOne(), (&diffResponse{}).diagnostic('E', missing).ready(),
			[]string{
				`stuntgres only #0 RowDescription {"columns":[{"attnum":0,"format":0,"name":"c0","table_oid":0,"type_len":-1,"type_oid":23,"typmod":-1}]}`,
				`stuntgres only #1 DataRow {"values":["1"]}`,
				"stuntgres only #2 CommandComplete SELECT 1",
				`reference only #0 ErrorResponse 42P01 relation "t" does not exist`,
			}},
		{"error_fields",
			(&diffResponse{}).diagnostic('E', sqlstate.Error{Code: "42703", Message: "m", Hint: "h"}).ready(),
			(&diffResponse{}).diagnostic('E', sqlstate.Error{Code: "42P01", Message: "m", Detail: "d"}).ready(),
			[]string{
				`#0 ErrorResponse code: stuntgres "42703", reference "42P01"`,
				`#0 ErrorResponse detail: stuntgres <absent>, reference "d"`,
				`#0 ErrorResponse hint: stuntgres "h", reference <absent>`,
			}},
		{"extra_notice_keeps_alignment",
			(&diffResponse{}).columns(23).row("1").row("2").complete("SELECT 2").ready(),
			(&diffResponse{}).columns(23).diagnostic('N', sqlstate.Error{Severity: "NOTICE", Code: "00000", Message: "hello"}).row("1").row("3").complete("SELECT 2").ready(),
			[]string{
				"reference only #1 NoticeResponse 00000 hello",
				`#2 (reference #3) DataRow values[0]: stuntgres "2", reference "3"`,
			}},
		{"row_count",
			(&diffResponse{}).columns(23).row("1").row("2").complete("SELECT 2").ready(),
			(&diffResponse{}).columns(23).row("1").complete("SELECT 1").ready(),
			[]string{
				`stuntgres only #2 DataRow {"values":["2"]}`,
				`#3 (reference #2) CommandComplete tag: stuntgres "SELECT 2", reference "SELECT 1"`,
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diffResponses(tc.native.messages, tc.reference.messages); !slices.Equal(got, tc.want) {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
	t.Run("capped", func(t *testing.T) {
		native, reference := (&diffResponse{}).columns(23), (&diffResponse{}).columns(23)
		for i := range 60 {
			native.row(fmt.Sprint(i))
			reference.row(fmt.Sprint(-i - 1))
		}
		got := diffResponses(native.ready().messages, reference.ready().messages)
		if len(got) != maxDiffLines+1 || got[0] != `#1 DataRow values[0]: stuntgres "0", reference "-1"` || got[maxDiffLines] != "… 10 more differences" {
			t.Fatalf("%d lines: %q ... %q", len(got), got[0], got[len(got)-1])
		}
	})
	t.Run("malformed_compares_data", func(t *testing.T) {
		native := (&diffResponse{}).add('C', []byte("no terminator")).ready()
		reference := (&diffResponse{}).add('C', []byte("other bytes")).ready()
		want := []string{`#0 CommandComplete data: stuntgres "bm8gdGVybWluYXRvcg==", reference "b3RoZXIgYnl0ZXM="`}
		if got := diffResponses(native.messages, reference.messages); !slices.Equal(got, want) {
			t.Fatalf("%q", got)
		}
	})
}
