package routine_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
)

func TestPLCheckedReplayMetadata(t *testing.T) {
	header := `CREATE FUNCTION public.checked(x pg_catalog.int4) RETURNS pg_catalog.int4 LANGUAGE replay_language `
	record := routine.PLCheckedReplay{Header: header, Owner: "reader"}
	comment, err := record.Comment()
	if err != nil {
		t.Fatal(err)
	}
	decoded, recognized, err := routine.DecodePLChecked(strings.TrimSpace(strings.TrimPrefix(comment, "-- ")))
	if err != nil || !recognized || decoded != record {
		t.Fatal("valid checked admission did not round trip", decoded, err)
	}
	create := header + "AS 'BEGIN RETURN x+1; END';\n"
	transfer := "ALTER FUNCTION public.checked(pg_catalog.int4) OWNER TO reader;\n"
	for _, tc := range []struct {
		name, script string
		entries      int
		good         bool
	}{
		{"valid", comment + create + transfer, 1, true},
		{"changed_body_still_requires_compilation", comment + header + "AS 'BEGIN RETURN 1;';\n" + transfer, 1, true},
		{"orphan", comment, 1, false},
		{"duplicate", comment + create + transfer, 2, false},
		{"intervening_statement", comment + "SELECT 1;\n" + create + transfer, 1, false},
		{"missing_transfer", comment + create, 1, false},
		{"wrong_owner", comment + create + strings.Replace(transfer, "reader", "other", 1), 1, false},
		{"wrong_transfer_identity", comment + create + strings.Replace(transfer, "public.checked", "public.other", 1), 1, false},
		{"wrong_transfer_signature", comment + create + strings.Replace(transfer, "int4", "text", 1), 1, false},
		{"wrong_create_identity", comment + strings.Replace(create, "public.checked", "public.other", 1) + transfer, 1, false},
		{"wrong_create_kind", comment + "CREATE PROCEDURE public.checked(x pg_catalog.int4) LANGUAGE replay_language AS 'BEGIN END';\n" + transfer, 1, false},
		{"wrong_create_signature", comment + strings.Replace(create, "x pg_catalog.int4", "x pg_catalog.text", 1) + transfer, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := parser.Parse(tc.script)
			if err != nil {
				t.Fatal(err)
			}
			entries := make([]routine.PositionedPLChecked, tc.entries)
			for i := range entries {
				entries[i] = routine.PositionedPLChecked{Record: record, End: len(comment)}
			}
			pairs, err := routine.PairPLChecked(tc.script, statements, entries)
			if tc.good && (err != nil || len(pairs) != 1) || !tc.good && err == nil {
				t.Fatal("checked admission pairing", pairs, err)
			}
		})
	}
	payload := func(raw string) string {
		return "stuntgres-pl-checked-v1 " + base64.StdEncoding.EncodeToString([]byte(raw))
	}
	for _, text := range []string{
		"stuntgres-pl-checked-v2 AA==", "stuntgres-pl-checked-v1 !!", "stuntgres-pl-checked-v1",
		payload(`{"header":null,"owner":"reader"}`),
		payload(`{"header":"CREATE FUNCTION public.f() RETURNS integer LANGUAGE sql ","owner":"reader","owner":"other"}`),
		payload(`{"header":"CREATE FUNCTION public.f() RETURNS integer LANGUAGE sql ","owner":"reader","extra":true}`),
		payload(`{"header":"SELECT 1 ","owner":"reader"}`),
		payload(`{"header":"CREATE FUNCTION f() RETURNS integer LANGUAGE sql ","owner":"reader"}`),
		payload(`{"header":"CREATE FUNCTION public.f() RETURNS integer LANGUAGE sql ","owner":""}`),
	} {
		if _, recognized, err := routine.DecodePLChecked(text); !recognized || err == nil {
			t.Fatal("malformed checked admission accepted", text, err)
		}
	}
}
