package typedef

import (
	"encoding/base64"
	"strings"
	"testing"

	"stuntgres/internal/parser"
)

func TestDomainDefaultReplayPairingInternal(t *testing.T) {
	record := DomainReplayRecord{Schema: "public", Table: "t", Cells: []DomainReplayCell{{Column: "v", Value: &DomainReplayNode{Kind: "null"}}}}
	directive, err := EncodeDomainReplay(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		text  string
		valid bool
	}{
		{"-- é\n" + directive + "INSERT INTO public.t(v) VALUES(NULL);", true},
		{directive + "INSERT INTO public.other(v) VALUES(NULL);", false},
		{directive + "SELECT 1; INSERT INTO public.t(v) VALUES(NULL);", false},
		{directive + "-- intervening\nINSERT INTO public.t(v) VALUES(NULL);", false},
		{directive + directive + "INSERT INTO public.t(v) VALUES(NULL);", false},
		{directive + "INSERT INTO public.t(v) VALUES(NULL),(NULL);", false},
		{directive + "INSERT INTO public.t(v) VALUES(NULL) RETURNING v;", false},
		{directive, false},
	} {
		var records []PositionedDomainReplay
		statements, err := parser.ParseWithOptions(tc.text, parser.Options{StandardConformingStrings: true, LineComment: func(comment string, pos parser.Position) error {
			record, found, err := DecodeDomainReplay(comment)
			if found && err == nil {
				records = append(records, PositionedDomainReplay{Record: record, End: pos.Offset + len(comment)})
			}
			return err
		}})
		if err != nil {
			t.Fatal(tc.text, err)
		}
		pairs, err := PairDomainReplay(tc.text, statements, records)
		if tc.valid && (err != nil || len(pairs) != 1) || !tc.valid && err == nil {
			t.Fatal(tc.text, pairs, err)
		}
	}
	valid := `{"schema":"public","table":"t","cells":[{"column":"v","value":{"kind":"null"}}]}`
	if _, found, err := DecodeDomainReplay(domainReplayMarker + " " + base64.StdEncoding.EncodeToString([]byte(valid))); err != nil || !found {
		t.Fatal("valid wire record", found, err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"schema":"public"`, `"schema":"public","schema":"public"`, 1),
		strings.Replace(valid, `"kind":"null"`, `"kind":"null","kind":"null"`, 1),
		strings.Replace(valid, `"kind":"null"`, `"kind":"null","unknown":true`, 1),
		strings.Replace(valid, `"schema"`, `"Schema"`, 1),
		`{"schema":"public","table":"t","cells":null}`,
		valid + valid,
		strings.Replace(valid, `"kind":"null"`, `"kind":"array","dimensions":[{"lower":1}],"elements":[]`, 1),
	} {
		if _, _, err := DecodeDomainReplay(domainReplayMarker + " " + base64.StdEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatal("malformed wire record admitted", raw)
		}
	}
}
