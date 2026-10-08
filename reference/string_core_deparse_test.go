//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

type stringCoreReviewDeparseCase struct {
	name, inputDefault, inputCheck, typ, defaultCompact, defaultPretty, checkCompact, checkPretty string
}

// Exact independent PostgreSQL18.0 root observations: artifacts108 and110.
func stringCoreReviewDeparseCases() []stringCoreReviewDeparseCase {
	return []stringCoreReviewDeparseCase{
		{"btrim1", "btrim(' x ')", "btrim(v)=e", "TEXT", "btrim(' x '::text)", "btrim(' x '::text)", "(btrim(v) = e)", "btrim(v) = e"},
		{"btrim2", "btrim('xxYxx','x')", "btrim(v,s)=e", "TEXT", "btrim('xxYxx'::text, 'x'::text)", "btrim('xxYxx'::text, 'x'::text)", "(btrim(v, s) = e)", "btrim(v, s) = e"},
		{"ltrim1", "ltrim(' x ')", "ltrim(v)=e", "TEXT", "ltrim(' x '::text)", "ltrim(' x '::text)", "(ltrim(v) = e)", "ltrim(v) = e"},
		{"ltrim2", "ltrim('xxYxx','x')", "ltrim(v,s)=e", "TEXT", "ltrim('xxYxx'::text, 'x'::text)", "ltrim('xxYxx'::text, 'x'::text)", "(ltrim(v, s) = e)", "ltrim(v, s) = e"},
		{"rtrim1", "rtrim(' x ')", "rtrim(v)=e", "TEXT", "rtrim(' x '::text)", "rtrim(' x '::text)", "(rtrim(v) = e)", "rtrim(v) = e"},
		{"rtrim2", "rtrim('xxYxx','x')", "rtrim(v,s)=e", "TEXT", "rtrim('xxYxx'::text, 'x'::text)", "rtrim('xxYxx'::text, 'x'::text)", "(rtrim(v, s) = e)", "rtrim(v, s) = e"},
		{"trim_both", "trim(both 'x' from 'xxYxx')", "trim(both s from v)=e", "TEXT", "TRIM(BOTH 'x'::text FROM 'xxYxx'::text)", "TRIM(BOTH 'x'::text FROM 'xxYxx'::text)", "(TRIM(BOTH s FROM v) = e)", "TRIM(BOTH s FROM v) = e"},
		{"trim_leading", "trim(leading 'x' from 'xxYxx')", "trim(leading s from v)=e", "TEXT", "TRIM(LEADING 'x'::text FROM 'xxYxx'::text)", "TRIM(LEADING 'x'::text FROM 'xxYxx'::text)", "(TRIM(LEADING s FROM v) = e)", "TRIM(LEADING s FROM v) = e"},
		{"trim_trailing", "trim(trailing 'x' from 'xxYxx')", "trim(trailing s from v)=e", "TEXT", "TRIM(TRAILING 'x'::text FROM 'xxYxx'::text)", "TRIM(TRAILING 'x'::text FROM 'xxYxx'::text)", "(TRIM(TRAILING s FROM v) = e)", "TRIM(TRAILING s FROM v) = e"},
		{"norm_default", "normalize('é')", "normalize(v)=e", "TEXT", "NORMALIZE('é'::text)", "NORMALIZE('é'::text)", "(NORMALIZE(v) = e)", "NORMALIZE(v) = e"},
		{"norm_nfc", "normalize('é',NFC)", "normalize(v,NFC)=e", "TEXT", "NORMALIZE('é'::text, NFC)", "NORMALIZE('é'::text, NFC)", "(NORMALIZE(v, NFC) = e)", "NORMALIZE(v, NFC) = e"},
		{"norm_nfd", "normalize('é',NFD)", "normalize(v,NFD)=e", "TEXT", "NORMALIZE('é'::text, NFD)", "NORMALIZE('é'::text, NFD)", "(NORMALIZE(v, NFD) = e)", "NORMALIZE(v, NFD) = e"},
		{"norm_nfkc", "normalize('ﬃ',NFKC)", "normalize(v,NFKC)=e", "TEXT", "NORMALIZE('ﬃ'::text, NFKC)", "NORMALIZE('ﬃ'::text, NFKC)", "(NORMALIZE(v, NFKC) = e)", "NORMALIZE(v, NFKC) = e"},
		{"norm_nfkd", "normalize('é',NFKD)", "normalize(v,NFKD)=e", "TEXT", "NORMALIZE('é'::text, NFKD)", "NORMALIZE('é'::text, NFKD)", "(NORMALIZE(v, NFKD) = e)", "NORMALIZE(v, NFKD) = e"},
		{"norm_quoted", `"normalize"('é','NFD')`, `"normalize"(v,s)=e`, "TEXT", `"normalize"('é'::text, 'NFD'::text)`, `"normalize"('é'::text, 'NFD'::text)`, `("normalize"(v, s) = e)`, `"normalize"(v, s) = e`},
		{"norm_qualified", "pg_catalog.normalize('é','NFD')", "pg_catalog.normalize(v,s)=e", "TEXT", `"normalize"('é'::text, 'NFD'::text)`, `"normalize"('é'::text, 'NFD'::text)`, `("normalize"(v, s) = e)`, `"normalize"(v, s) = e`},
		{"predicate_default", "'é' IS NORMALIZED", "v IS NORMALIZED", "BOOL", "('é'::text IS NORMALIZED)", "('é'::text IS NORMALIZED)", "(v IS NORMALIZED)", "(v IS NORMALIZED)"},
		{"predicate_nfc", "'é' IS NFC NORMALIZED", "v IS NFC NORMALIZED", "BOOL", "('é'::text IS NFC NORMALIZED)", "('é'::text IS NFC NORMALIZED)", "(v IS NFC NORMALIZED)", "(v IS NFC NORMALIZED)"},
		{"predicate_nfd", "'é' IS NFD NORMALIZED", "v IS NFD NORMALIZED", "BOOL", "('é'::text IS NFD NORMALIZED)", "('é'::text IS NFD NORMALIZED)", "(v IS NFD NORMALIZED)", "(v IS NFD NORMALIZED)"},
		{"predicate_nfkc", "'é' IS NFKC NORMALIZED", "v IS NFKC NORMALIZED", "BOOL", "('é'::text IS NFKC NORMALIZED)", "('é'::text IS NFKC NORMALIZED)", "(v IS NFKC NORMALIZED)", "(v IS NFKC NORMALIZED)"},
		{"predicate_nfkd", "'é' IS NFKD NORMALIZED", "v IS NFKD NORMALIZED", "BOOL", "('é'::text IS NFKD NORMALIZED)", "('é'::text IS NFKD NORMALIZED)", "(v IS NFKD NORMALIZED)", "(v IS NFKD NORMALIZED)"},
		{"predicate_not", "'é' IS NOT NORMALIZED", "v IS NOT NORMALIZED", "BOOL", "(NOT ('é'::text IS NORMALIZED))", "NOT ('é'::text IS NORMALIZED)", "(NOT (v IS NORMALIZED))", "NOT (v IS NORMALIZED)"},
		{"is_normalized1", "is_normalized('é')", "is_normalized(v)", "BOOL", "is_normalized('é'::text)", "is_normalized('é'::text)", "is_normalized(v)", "is_normalized(v)"},
		{"is_normalized2", "is_normalized('é','NFD')", "is_normalized(v,s)", "BOOL", "is_normalized('é'::text, 'NFD'::text)", "is_normalized('é'::text, 'NFD'::text)", "is_normalized(v, s)", "is_normalized(v, s)"},
		{"concat", "'a'||'b'", "v||s=e", "TEXT", "('a'::text || 'b'::text)", "'a'::text || 'b'::text", "((v || s) = e)", "(v || s) = e"},
		{"concat_mixed", "'a'::text||42", "v||n=e", "TEXT", "('a'::text || 42)", "'a'::text || 42", "((v || n) = e)", "(v || n) = e"},
		{"concat_reverse", "42||'a'::text", "n||v=e", "TEXT", "(42 || 'a'::text)", "42 || 'a'::text", "((n || v) = e)", "(n || v) = e"},
		{"nested", "'['||normalize(btrim(' é '))||']'", "normalize(btrim(v))||(s||v)=e", "TEXT", "(('['::text || NORMALIZE(btrim(' é '::text))) || ']'::text)", "('['::text || NORMALIZE(btrim(' é '::text))) || ']'::text", "((NORMALIZE(btrim(v)) || (s || v)) = e)", "(NORMALIZE(btrim(v)) || (s || v)) = e"},
	}
}

func stringCoreReviewDeparseReference(t *testing.T, p *referencePair, oracle bool) {
	run := func(t *testing.T, s referenceStep) {
		t.Helper()
		if oracle {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	for _, tc := range stringCoreReviewDeparseCases() {
		t.Run(tc.name, func(t *testing.T) {
			run(t, referenceExec("CREATE TABLE scd_"+tc.name+"(v TEXT,s TEXT,e TEXT,n INT,d "+tc.typ+" DEFAULT ("+tc.inputDefault+"),CONSTRAINT scc_"+tc.name+" CHECK("+tc.inputCheck+"))", 0))
			for _, mode := range []struct{ suffix, defaultSQL, checkSQL string }{
				{"", tc.defaultCompact, tc.checkCompact}, {",false", tc.defaultCompact, tc.checkCompact}, {",true", tc.defaultPretty, tc.checkPretty},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/prepared=%t", mode.suffix, prepared), func(t *testing.T) {
						for _, tc := range []struct{ name, q, want string }{
							{"default", "SELECT pg_get_expr(a.adbin,a.adrelid" + mode.suffix + ") AS d FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='scd_" + tc.name + "'", mode.defaultSQL},
							{"check_expression", "SELECT pg_get_expr(conbin,conrelid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='scc_" + tc.name + "'", mode.checkSQL},
							{"check_constraint", "SELECT pg_get_constraintdef(oid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='scc_" + tc.name + "'", "CHECK (" + mode.checkSQL + ")"},
						} {
							t.Run(tc.name, func(t *testing.T) {
								s := referenceSelect(tc.q, []string{"d"}, []string{"TEXT"}, [][]any{{tc.want}})
								if prepared {
									s = referencePrepared(s)
								}
								run(t, s)
							})
						}
					})
				}
			}
		})
	}
	for _, q := range []string{"ALTER TABLE scd_btrim1 RENAME v TO renamed", "ALTER TABLE scd_btrim1 RENAME TO scd_renamed", "CREATE TABLE scd_supplied(replacement TEXT,s TEXT,e TEXT,n INT,d TEXT)", "SET search_path=pg_catalog"} {
		run(t, referenceExec(q, 0))
	}
	for _, tc := range []struct{ q, want string }{
		{"SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'", "CHECK (btrim(renamed) = e)"},
		{"SELECT pg_get_expr(conbin,'public.scd_supplied'::regclass,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'", "btrim(replacement) = e"},
		{"SELECT pg_catalog.pg_get_expr(a.adbin,a.adrelid,true) AS d FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='scd_norm_quoted'", `"normalize"('é'::text, 'NFD'::text)`},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceSelect(tc.q, []string{"d"}, []string{"TEXT"}, [][]any{{tc.want}})
			if prepared {
				s = referencePrepared(s)
			}
			run(t, s)
		}
	}
	run(t, referenceExec("SET search_path=public", 0))
}

func TestReferenceStringCoreReviewDeparse(t *testing.T) {
	stringCoreReviewDeparseReference(t, newReferencePair(t), false)
}
