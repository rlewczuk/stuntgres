package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"testing"
)

type stringCoreReviewDeparseCase struct {
	name, inputDefault, inputCheck, typ, defaultCompact, defaultPretty, checkCompact, checkPretty, provider string
}

// PostgreSQL18.0 observations in root capture artifacts108/110. In particular,
// ordinary calls and SQL-syntax calls are not interchangeable deparse goldens.
func stringCoreReviewDeparseCases() []stringCoreReviewDeparseCase {
	return []stringCoreReviewDeparseCase{
		{"btrim1", "btrim(' x ')", "btrim(v)=e", "TEXT", "btrim(' x '::text)", "btrim(' x '::text)", "(btrim(v) = e)", "btrim(v) = e", "btrim"},
		{"btrim2", "btrim('xxYxx','x')", "btrim(v,s)=e", "TEXT", "btrim('xxYxx'::text, 'x'::text)", "btrim('xxYxx'::text, 'x'::text)", "(btrim(v, s) = e)", "btrim(v, s) = e", "btrim"},
		{"ltrim1", "ltrim(' x ')", "ltrim(v)=e", "TEXT", "ltrim(' x '::text)", "ltrim(' x '::text)", "(ltrim(v) = e)", "ltrim(v) = e", "ltrim"},
		{"ltrim2", "ltrim('xxYxx','x')", "ltrim(v,s)=e", "TEXT", "ltrim('xxYxx'::text, 'x'::text)", "ltrim('xxYxx'::text, 'x'::text)", "(ltrim(v, s) = e)", "ltrim(v, s) = e", "ltrim"},
		{"rtrim1", "rtrim(' x ')", "rtrim(v)=e", "TEXT", "rtrim(' x '::text)", "rtrim(' x '::text)", "(rtrim(v) = e)", "rtrim(v) = e", "rtrim"},
		{"rtrim2", "rtrim('xxYxx','x')", "rtrim(v,s)=e", "TEXT", "rtrim('xxYxx'::text, 'x'::text)", "rtrim('xxYxx'::text, 'x'::text)", "(rtrim(v, s) = e)", "rtrim(v, s) = e", "rtrim"},
		{"trim_both", "trim(both 'x' from 'xxYxx')", "trim(both s from v)=e", "TEXT", "TRIM(BOTH 'x'::text FROM 'xxYxx'::text)", "TRIM(BOTH 'x'::text FROM 'xxYxx'::text)", "(TRIM(BOTH s FROM v) = e)", "TRIM(BOTH s FROM v) = e", "btrim"},
		{"trim_leading", "trim(leading 'x' from 'xxYxx')", "trim(leading s from v)=e", "TEXT", "TRIM(LEADING 'x'::text FROM 'xxYxx'::text)", "TRIM(LEADING 'x'::text FROM 'xxYxx'::text)", "(TRIM(LEADING s FROM v) = e)", "TRIM(LEADING s FROM v) = e", "ltrim"},
		{"trim_trailing", "trim(trailing 'x' from 'xxYxx')", "trim(trailing s from v)=e", "TEXT", "TRIM(TRAILING 'x'::text FROM 'xxYxx'::text)", "TRIM(TRAILING 'x'::text FROM 'xxYxx'::text)", "(TRIM(TRAILING s FROM v) = e)", "TRIM(TRAILING s FROM v) = e", "rtrim"},
		{"norm_default", "normalize('é')", "normalize(v)=e", "TEXT", "NORMALIZE('é'::text)", "NORMALIZE('é'::text)", "(NORMALIZE(v) = e)", "NORMALIZE(v) = e", "normalize"},
		{"norm_nfc", "normalize('é',NFC)", "normalize(v,NFC)=e", "TEXT", "NORMALIZE('é'::text, NFC)", "NORMALIZE('é'::text, NFC)", "(NORMALIZE(v, NFC) = e)", "NORMALIZE(v, NFC) = e", "normalize"},
		{"norm_nfd", "normalize('é',NFD)", "normalize(v,NFD)=e", "TEXT", "NORMALIZE('é'::text, NFD)", "NORMALIZE('é'::text, NFD)", "(NORMALIZE(v, NFD) = e)", "NORMALIZE(v, NFD) = e", "normalize"},
		{"norm_nfkc", "normalize('ﬃ',NFKC)", "normalize(v,NFKC)=e", "TEXT", "NORMALIZE('ﬃ'::text, NFKC)", "NORMALIZE('ﬃ'::text, NFKC)", "(NORMALIZE(v, NFKC) = e)", "NORMALIZE(v, NFKC) = e", "normalize"},
		{"norm_nfkd", "normalize('é',NFKD)", "normalize(v,NFKD)=e", "TEXT", "NORMALIZE('é'::text, NFKD)", "NORMALIZE('é'::text, NFKD)", "(NORMALIZE(v, NFKD) = e)", "NORMALIZE(v, NFKD) = e", "normalize"},
		{"norm_quoted", `"normalize"('é','NFD')`, `"normalize"(v,s)=e`, "TEXT", `"normalize"('é'::text, 'NFD'::text)`, `"normalize"('é'::text, 'NFD'::text)`, `("normalize"(v, s) = e)`, `"normalize"(v, s) = e`, "normalize"},
		{"norm_qualified", "pg_catalog.normalize('é','NFD')", "pg_catalog.normalize(v,s)=e", "TEXT", `"normalize"('é'::text, 'NFD'::text)`, `"normalize"('é'::text, 'NFD'::text)`, `("normalize"(v, s) = e)`, `"normalize"(v, s) = e`, "normalize"},
		{"predicate_default", "'é' IS NORMALIZED", "v IS NORMALIZED", "BOOL", "('é'::text IS NORMALIZED)", "('é'::text IS NORMALIZED)", "(v IS NORMALIZED)", "(v IS NORMALIZED)", "is_normalized"},
		{"predicate_nfc", "'é' IS NFC NORMALIZED", "v IS NFC NORMALIZED", "BOOL", "('é'::text IS NFC NORMALIZED)", "('é'::text IS NFC NORMALIZED)", "(v IS NFC NORMALIZED)", "(v IS NFC NORMALIZED)", "is_normalized"},
		{"predicate_nfd", "'é' IS NFD NORMALIZED", "v IS NFD NORMALIZED", "BOOL", "('é'::text IS NFD NORMALIZED)", "('é'::text IS NFD NORMALIZED)", "(v IS NFD NORMALIZED)", "(v IS NFD NORMALIZED)", "is_normalized"},
		{"predicate_nfkc", "'é' IS NFKC NORMALIZED", "v IS NFKC NORMALIZED", "BOOL", "('é'::text IS NFKC NORMALIZED)", "('é'::text IS NFKC NORMALIZED)", "(v IS NFKC NORMALIZED)", "(v IS NFKC NORMALIZED)", "is_normalized"},
		{"predicate_nfkd", "'é' IS NFKD NORMALIZED", "v IS NFKD NORMALIZED", "BOOL", "('é'::text IS NFKD NORMALIZED)", "('é'::text IS NFKD NORMALIZED)", "(v IS NFKD NORMALIZED)", "(v IS NFKD NORMALIZED)", "is_normalized"},
		{"predicate_not", "'é' IS NOT NORMALIZED", "v IS NOT NORMALIZED", "BOOL", "(NOT ('é'::text IS NORMALIZED))", "NOT ('é'::text IS NORMALIZED)", "(NOT (v IS NORMALIZED))", "NOT (v IS NORMALIZED)", "is_normalized"},
		{"is_normalized1", "is_normalized('é')", "is_normalized(v)", "BOOL", "is_normalized('é'::text)", "is_normalized('é'::text)", "is_normalized(v)", "is_normalized(v)", "is_normalized"},
		{"is_normalized2", "is_normalized('é','NFD')", "is_normalized(v,s)", "BOOL", "is_normalized('é'::text, 'NFD'::text)", "is_normalized('é'::text, 'NFD'::text)", "is_normalized(v, s)", "is_normalized(v, s)", "is_normalized"},
		{"concat", "'a'||'b'", "v||s=e", "TEXT", "('a'::text || 'b'::text)", "'a'::text || 'b'::text", "((v || s) = e)", "(v || s) = e", "||"},
		{"concat_mixed", "'a'::text||42", "v||n=e", "TEXT", "('a'::text || 42)", "'a'::text || 42", "((v || n) = e)", "(v || n) = e", "||"},
		{"concat_reverse", "42||'a'::text", "n||v=e", "TEXT", "(42 || 'a'::text)", "42 || 'a'::text", "((n || v) = e)", "(n || v) = e", "||"},
		{"nested", "'['||normalize(btrim(' é '))||']'", "normalize(btrim(v))||(s||v)=e", "TEXT", "(('['::text || NORMALIZE(btrim(' é '::text))) || ']'::text)", "('['::text || NORMALIZE(btrim(' é '::text))) || ']'::text", "((NORMALIZE(btrim(v)) || (s || v)) = e)", "(NORMALIZE(btrim(v)) || (s || v)) = e", "||"},
	}
}

func TestReferenceStringCoreReviewDeparse(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			cases := stringCoreReviewDeparseCases()
			for _, tc := range cases {
				stringCoreControlExec(t, db, "CREATE TABLE scd_"+tc.name+"(v TEXT,s TEXT,e TEXT,n INT,d "+tc.typ+" DEFAULT ("+tc.inputDefault+"),CONSTRAINT scc_"+tc.name+" CHECK("+tc.inputCheck+"))")
			}
			observe := func(phase string) {
				for _, tc := range cases {
					for _, mode := range []struct{ suffix, defaultSQL, checkSQL string }{
						{"", tc.defaultCompact, tc.checkCompact}, {",false", tc.defaultCompact, tc.checkCompact}, {",true", tc.defaultPretty, tc.checkPretty},
					} {
						for _, prepared := range []bool{false, true} {
							t.Run(fmt.Sprintf("%s/%s/%s/prepared=%t", phase, tc.name, mode.suffix, prepared), func(t *testing.T) {
								t.Run("default", func(t *testing.T) {
									stringCoreControlText(t, db, prepared, "SELECT pg_get_expr(a.adbin,a.adrelid"+mode.suffix+") AS d FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='scd_"+tc.name+"'", mode.defaultSQL)
								})
								t.Run("check_expression", func(t *testing.T) {
									stringCoreControlText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='scc_"+tc.name+"'", mode.checkSQL)
								})
								t.Run("check_constraint", func(t *testing.T) {
									stringCoreControlText(t, db, prepared, "SELECT pg_get_constraintdef(oid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='scc_"+tc.name+"'", "CHECK ("+mode.checkSQL+")")
								})
							})
						}
					}
				}
			}
			observe("initial")
			if !backend.pg {
				controlDB := p.native
				var dump bytes.Buffer
				if err := stuntgres.Dump(context.Background(), controlDB, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(context.Background(), controlDB); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(context.Background(), controlDB, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			observe("restored")
			held, err := db.Query("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			stringCoreControlExec(t, db, "ALTER TABLE scd_btrim1 RENAME v TO renamed")
			stringCoreControlExec(t, db, "ALTER TABLE scd_btrim1 RENAME TO scd_renamed")
			stringCoreControlExec(t, db, "CREATE TABLE scd_supplied(replacement TEXT,s TEXT,e TEXT,n INT,d TEXT)")
			for _, prepared := range []bool{false, true} {
				stringCoreControlText(t, db, prepared, "SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'", "CHECK (btrim(renamed) = e)")
				stringCoreControlText(t, db, prepared, "SELECT pg_get_expr(conbin,'public.scd_supplied'::regclass,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'", "btrim(replacement) = e")
			}
			stringCoreRetainedRows(t, held, nil, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (btrim(v) = e)"}}, false)
			stringCoreControlExec(t, db, "SET search_path=pg_catalog")
			for _, prepared := range []bool{false, true} {
				stringCoreControlText(t, db, prepared, "SELECT pg_catalog.pg_get_expr(a.adbin,a.adrelid,true) AS d FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='scd_norm_quoted'", `"normalize"('é'::text, 'NFD'::text)`)
			}
		})
	}
}
