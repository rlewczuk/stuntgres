package enginetests

import (
	"encoding/base64"
	"strings"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSegReplayCodec(t *testing.T) {
	payload := `{"lower_bits":1065353216,"upper_bits":1065353216,"l_sigd":2,"u_sigd":3,"l_ext":0,"u_ext":0}`
	raw := `{"schema":"public","table":"t","cells":[{"column":"v","value":{"kind":"seg","seg":` + payload + `}}]}`
	decode := func(raw string) error {
		_, _, err := typedef.DecodeDomainReplay("stuntgres-domain-null-v1 " + base64.StdEncoding.EncodeToString([]byte(raw)))
		return err
	}
	if err := decode(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(raw, `"schema":"public"`, `"schema":"public","schema":"public"`, 1),
		strings.Replace(raw, `"l_sigd":2`, `"l_sigd":2,"l_sigd":2`, 1),
		strings.Replace(raw, `"l_sigd":2`, `"l_sigd":null`, 1),
		strings.Replace(raw, `"l_sigd":2,`, "", 1),
		strings.Replace(raw, `"lower_bits":1065353216`, `"lower_bits":4294967296`, 1),
		strings.Replace(raw, `"lower_bits":1065353216`, `"lower_bits":-1`, 1),
		strings.Replace(raw, `"lower_bits":1065353216`, `"lower_bits":1.5`, 1),
		strings.Replace(raw, `"l_ext":0`, `"l_ext":256`, 1),
		strings.Replace(raw, `"u_ext":0`, `"u_ext":0,"extra":0`, 1),
		strings.Replace(raw, "public", "\xff", 1), raw + raw,
	} {
		if err := decode(bad); err == nil {
			t.Fatal("invalid wire admitted", bad)
		}
	}
	for _, comment := range []string{"stuntgres-domain-null-v999 AA==", "stuntgres-domain-null-v1 !", "stuntgres-domain-null-v1"} {
		if _, found, err := typedef.DecodeDomainReplay(comment); !found || err == nil {
			t.Fatal("invalid marker", comment, found, err)
		}
	}
	d := sqltype.SegDatum{LowerBits: 1065353216, UpperBits: 1065353216, LowerDigits: 2, UpperDigits: 3}
	record := typedef.DomainReplayRecord{Schema: "public", Table: "t", Cells: []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "seg", Seg: &d}}}}
	directive, err := typedef.EncodeDomainReplay(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sql   string
		valid bool
	}{
		{directive + "INSERT INTO public.t VALUES ('1.0');", true},
		{directive + "INSERT INTO public.other VALUES ('1.0');", false},
		{directive + "SELECT 1; INSERT INTO public.t VALUES ('1.0');", false},
		{directive + "-- moved\nINSERT INTO public.t VALUES ('1.0');", false},
		{directive + directive + "INSERT INTO public.t VALUES ('1.0');", false},
		{directive + "INSERT INTO public.t VALUES ('1.0'),('1.0');", false},
		{directive, false},
	} {
		var records []typedef.PositionedDomainReplay
		statements, err := parser.ParseWithOptions(tc.sql, parser.Options{StandardConformingStrings: true, LineComment: func(comment string, pos parser.Position) error {
			r, found, err := typedef.DecodeDomainReplay(comment)
			if found && err == nil {
				records = append(records, typedef.PositionedDomainReplay{Record: r, End: pos.Offset + len(comment)})
			}
			return err
		}})
		if err != nil {
			t.Fatal(err)
		}
		pairs, err := typedef.PairDomainReplay(tc.sql, statements, records)
		if tc.valid && (err != nil || len(pairs) != 1) || !tc.valid && err == nil {
			t.Fatal("pair admission", tc.sql, err)
		}
	}
}
