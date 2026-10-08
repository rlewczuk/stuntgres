package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
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

func TestStringCoreReviewDeparse(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	cases := stringCoreReviewDeparseCases()
	for _, tc := range cases {
		execSQL(t, db, "CREATE TABLE scd_"+tc.name+"(v TEXT,s TEXT,e TEXT,n INT,d "+tc.typ+" DEFAULT ("+tc.inputDefault+"),CONSTRAINT scc_"+tc.name+" CHECK("+tc.inputCheck+"))")
	}
	observe := func(phase string) {
		for _, tc := range cases {
			for _, mode := range []struct{ suffix, defaultSQL, checkSQL string }{
				{"", tc.defaultCompact, tc.checkCompact}, {",false", tc.defaultCompact, tc.checkCompact}, {",true", tc.defaultPretty, tc.checkPretty},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/prepared=%t", phase, tc.name, mode.suffix, prepared), func(t *testing.T) {
						t.Run("default", func(t *testing.T) {
							deparseText(t, db, prepared, "SELECT pg_get_expr(a.adbin,a.adrelid"+mode.suffix+") AS d FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='scd_"+tc.name+"'", mode.defaultSQL)
						})
						t.Run("check_expression", func(t *testing.T) {
							deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='scc_"+tc.name+"'", mode.checkSQL)
						})
						t.Run("check_constraint", func(t *testing.T) {
							deparseText(t, db, prepared, "SELECT pg_get_constraintdef(oid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='scc_"+tc.name+"'", "CHECK ("+mode.checkSQL+")")
						})
					})
				}
			}
		}
	}
	observe("initial")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	observe("restored")
	held, err := db.Query("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	execSQL(t, db, "ALTER TABLE scd_btrim1 RENAME v TO renamed")
	execSQL(t, db, "ALTER TABLE scd_btrim1 RENAME TO scd_renamed")
	execSQL(t, db, "CREATE TABLE scd_supplied(replacement TEXT,s TEXT,e TEXT,n INT,d TEXT)")
	for _, prepared := range []bool{false, true} {
		deparseText(t, db, prepared, "SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'", "CHECK (btrim(renamed) = e)")
		deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,'public.scd_supplied'::regclass,true) AS d FROM pg_catalog.pg_constraint WHERE conname='scc_btrim1'", "btrim(replacement) = e")
	}
	queryCoreRows(t, held, nil, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (btrim(v) = e)"}}, false)
	execSQL(t, db, "SET search_path=pg_catalog")
	for _, prepared := range []bool{false, true} {
		deparseText(t, db, prepared, "SELECT pg_catalog.pg_get_expr(a.adbin,a.adrelid,true) AS d FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='scd_norm_quoted'", `"normalize"('é'::text, 'NFD'::text)`)
	}
}

func TestStringCoreReviewStructural(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	type retained struct {
		rows  *sql.Rows
		types []*sql.ColumnType
		tc    stringCoreReviewDeparseCase
		check bool
		tree  string
	}
	var held []retained
	metadata := func(t *testing.T, types []*sql.ColumnType) {
		t.Helper()
		if len(types) != 1 || types[0].DatabaseTypeName() != "PG_NODE_TREE" || types[0].ScanType() != reflect.TypeFor[string]() {
			t.Fatal("structural metadata", types)
		}
	}
	read := func(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, tc stringCoreReviewDeparseCase, check bool) string {
		t.Helper()
		defer rows.Close()
		metadata(t, types)
		if !rows.Next() {
			t.Fatal("missing carrier", rows.Err())
		}
		var tree string
		if err := rows.Scan(&tree); err != nil {
			t.Fatal(err)
		}
		id := "25"
		if check || tc.typ == "BOOL" {
			id = "16"
		}
		if !strings.HasPrefix(tree, "{type "+id+" typmod -1 ") || !strings.Contains(tree, "String "+tc.provider) {
			t.Fatal("missing typed recursive string carrier", tc.name, tree)
		}
		if check && !strings.Contains(tree, "Var attnum 1") {
			t.Fatal("lost source operand", tree)
		}
		if tc.name == "nested" && (!strings.Contains(tree, "String normalize") || !strings.Contains(tree, "String btrim")) {
			t.Fatal("lost nested providers", tree)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra carrier", rows.Err())
		}
		metadata(t, types)
		return tree
	}
	for _, tc := range stringCoreReviewDeparseCases() {
		execSQL(t, db, "CREATE TABLE scd_"+tc.name+"(v TEXT,s TEXT,e TEXT,n INT,d "+tc.typ+" DEFAULT ("+tc.inputDefault+"),CONSTRAINT scc_"+tc.name+" CHECK("+tc.inputCheck+"))")
		for _, check := range []bool{false, true} {
			q := "SELECT a.adbin FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='scd_" + tc.name + "'"
			if check {
				q = "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='scc_" + tc.name + "'"
			}
			var before string
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/check=%t/prepared=%t", tc.name, check, prepared), func(t *testing.T) {
					var rows *sql.Rows
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if e != nil {
							t.Fatal(e)
						}
						defer s.Close()
						rows, err = s.Query()
					} else {
						rows, err = db.Query(q)
					}
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil {
						rows.Close()
						t.Fatal(err)
					}
					before = read(t, rows, types, tc, check)
				})
			}
			rows, err := db.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			metadata(t, types)
			held = append(held, retained{rows, types, tc, check, before})
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "ALTER TABLE scd_btrim1 RENAME v TO renamed")
	for _, h := range held {
		t.Run(h.tc.name+"/held", func(t *testing.T) {
			if tree := read(t, h.rows, h.types, h.tc, h.check); tree != h.tree {
				t.Fatal("held carrier changed after publication replacement", tree, h.tree)
			}
			metadata(t, h.types)
		})
	}
}
