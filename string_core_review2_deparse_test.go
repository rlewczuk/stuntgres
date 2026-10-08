package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

type stringCoreReview2Case struct{ name, decl, check, compact, pretty string }

// Independent PostgreSQL18.0 observations from root artifact144, all74 probes.
// These templates are captured strings, not renderer-derived expectations.
func stringCoreReview2Cases() []stringCoreReview2Case {
	var cases []stringCoreReview2Case
	for _, typ := range []struct{ name, sql, display string }{{"char", "CHAR(4)", "character(4)"}, {"varchar", "VARCHAR(4)", "character varying(4)"}, {"name", "NAME", "name"}} {
		decl := "v " + typ.sql + ",s " + typ.sql + ",f " + typ.sql + ",e TEXT"
		for _, fn := range []string{"btrim", "ltrim", "rtrim"} {
			cases = append(cases,
				stringCoreReview2Case{fn + "1_" + typ.name, decl, fn + "(v)=e", "(" + fn + "((v)::text) = e)", fn + "(v::text) = e"},
				stringCoreReview2Case{fn + "2_" + typ.name, decl, fn + "(v,s)=e", "(" + fn + "((v)::text, (s)::text) = e)", fn + "(v::text, s::text) = e"})
		}
		cases = append(cases,
			stringCoreReview2Case{"norm1_" + typ.name, decl, "pg_catalog.normalize(v)=e", "(\"normalize\"((v)::text) = e)", "\"normalize\"(v::text) = e"},
			stringCoreReview2Case{"norm2_" + typ.name, decl, "pg_catalog.normalize(v,f)=e", "(\"normalize\"((v)::text, (f)::text) = e)", "\"normalize\"(v::text, f::text) = e"},
			stringCoreReview2Case{"pred1_" + typ.name, decl, "is_normalized(v)", "is_normalized((v)::text)", "is_normalized(v::text)"},
			stringCoreReview2Case{"pred2_" + typ.name, decl, "is_normalized(v,f)", "is_normalized((v)::text, (f)::text)", "is_normalized(v::text, f::text)"},
			stringCoreReview2Case{"special_norm1_" + typ.name, decl, "normalize(v)=e", "(NORMALIZE(v) = e)", "NORMALIZE(v) = e"},
			stringCoreReview2Case{"special_norm2_" + typ.name, decl, "normalize(v,NFD)=e", "(NORMALIZE(v, NFD) = e)", "NORMALIZE(v, NFD) = e"},
			stringCoreReview2Case{"special_pred1_" + typ.name, decl, "v IS NORMALIZED", "(v IS NORMALIZED)", "(v IS NORMALIZED)"},
			stringCoreReview2Case{"special_pred2_" + typ.name, decl, "v IS NFD NORMALIZED", "(v IS NFD NORMALIZED)", "(v IS NFD NORMALIZED)"},
			stringCoreReview2Case{"literal_source_" + typ.name, "v TEXT,e TEXT", "btrim(' x '::" + typ.sql + ")=e", "(btrim((' x '::" + typ.display + ")::text) = e)", "btrim(' x '::" + typ.display + "::text) = e"},
			stringCoreReview2Case{"literal_form_" + typ.name, "v TEXT,e TEXT", "pg_catalog.normalize(v,'NFD'::" + typ.sql + ")=e", "(\"normalize\"(v, ('NFD'::" + typ.display + ")::text) = e)", "\"normalize\"(v, 'NFD'::" + typ.display + "::text) = e"})
		for _, dir := range []string{"BOTH", "LEADING", "TRAILING"} {
			cases = append(cases, stringCoreReview2Case{"special_trim_" + strings.ToLower(dir) + "_" + typ.name, decl, "trim(" + dir + " s from v)=e", "(TRIM(" + dir + " s FROM v) = e)", "TRIM(" + dir + " s FROM v) = e"})
		}
	}
	for _, fn := range []string{"btrim", "ltrim", "rtrim", "pg_catalog.normalize", "is_normalized"} {
		name, printed := fn, fn
		if fn == "pg_catalog.normalize" {
			name = "norm"
			printed = "\"normalize\""
		}
		leftCompact, leftPretty := printed+"((v)::text, s)", printed+"(v::text, s)"
		rightCompact, rightPretty := printed+"(v, (s)::text)", printed+"(v, s::text)"
		check := fn + "(v,s)"
		if fn != "is_normalized" {
			check += "=e"
			leftCompact = "(" + leftCompact + " = e)"
			leftPretty += " = e"
			rightCompact = "(" + rightCompact + " = e)"
			rightPretty += " = e"
		}
		cases = append(cases, stringCoreReview2Case{name + "_char_text", "v CHAR(4),s TEXT,e TEXT", check, leftCompact, leftPretty}, stringCoreReview2Case{name + "_text_char", "v TEXT,s CHAR(4),e TEXT", check, rightCompact, rightPretty})
	}
	for _, trim := range []struct{ name, dir string }{{"btrim", "BOTH"}, {"ltrim", "LEADING"}, {"rtrim", "TRAILING"}} {
		decl := "v BYTEA,s BYTEA,e BYTEA"
		cases = append(cases,
			stringCoreReview2Case{"bytea_ordinary_" + trim.name, decl, trim.name + "(v,s)=e", "(" + trim.name + "(v, s) = e)", trim.name + "(v, s) = e"},
			stringCoreReview2Case{"bytea_special_" + trim.name, decl, "trim(" + trim.dir + " s from v)=e", "(TRIM(" + trim.dir + " s FROM v) = e)", "TRIM(" + trim.dir + " s FROM v) = e"})
	}
	return append(cases, stringCoreReview2Case{"bytea_nested", "v BYTEA,s BYTEA,e BYTEA", "btrim(trim(LEADING s from v),s)=e", "(btrim(TRIM(LEADING s FROM v), s) = e)", "btrim(TRIM(LEADING s FROM v), s) = e"})
}

func stringCoreReview2ByteaValue(name string) []byte {
	if strings.HasSuffix(name, "ltrim") {
		return []byte("Yxx")
	}
	if strings.HasSuffix(name, "rtrim") {
		return []byte("xxY")
	}
	return []byte("Y")
}

func TestStringCoreReview2Deparse(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	cases := stringCoreReview2Cases()
	for _, tc := range cases {
		execSQL(t, db, "CREATE TABLE sc2_"+tc.name+"("+tc.decl+",CONSTRAINT sc2c_"+tc.name+" CHECK("+tc.check+"))")
	}
	observe := func(phase string) {
		for _, tc := range cases {
			for _, mode := range []struct{ suffix, want string }{{"", tc.compact}, {",false", tc.compact}, {",true", tc.pretty}} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/prepared=%t", phase, tc.name, mode.suffix, prepared), func(t *testing.T) {
						t.Run("expression", func(t *testing.T) {
							deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_"+tc.name+"'", mode.want)
						})
						t.Run("constraint", func(t *testing.T) {
							deparseText(t, db, prepared, "SELECT pg_get_constraintdef(oid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_"+tc.name+"'", "CHECK ("+mode.want+")")
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
}

func TestStringCoreReview2ByteaLifecycle(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	type heldCarrier struct {
		rows       *sql.Rows
		name, tree string
	}
	var held []heldCarrier
	readTree := func(t *testing.T, rows *sql.Rows, name string) string {
		t.Helper()
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "PG_NODE_TREE" {
			t.Fatal("carrier metadata", types, err)
		}
		if !rows.Next() {
			t.Fatal("missing carrier", rows.Err())
		}
		var tree string
		if err := rows.Scan(&tree); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tree, "type 16") || !strings.Contains(tree, "Binary ") || !strings.Contains(tree, "Var attnum 1") || !strings.Contains(tree, "Var attnum 2") {
			t.Fatal("typed recursive BYTEA carrier", name, tree)
		}
		if name == "bytea_nested" && strings.Count(tree, "Binary ") != 2 {
			t.Fatal("missing nested binary child", tree)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("carrier cardinality", rows.Err())
		}
		return tree
	}
	var cases []stringCoreReview2Case
	for _, tc := range stringCoreReview2Cases() {
		if !strings.HasPrefix(tc.name, "bytea_") {
			continue
		}
		cases = append(cases, tc)
		execSQL(t, db, "CREATE TABLE sc2_"+tc.name+"("+tc.decl+",CONSTRAINT sc2c_"+tc.name+" CHECK("+tc.check+"))")
		execSQL(t, db, "INSERT INTO sc2_"+tc.name+" VALUES($1,$2,$3)", []byte("xxYxx"), []byte("x"), stringCoreReview2ByteaValue(tc.name))
		execSQL(t, db, "INSERT INTO sc2_"+tc.name+" VALUES(NULL,NULL,NULL)")
		q := "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='sc2c_" + tc.name + "'"
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		tree := readTree(t, rows, tc.name)
		rows, err = db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, heldCarrier{rows, tc.name, tree})
		t.Cleanup(func() { rows.Close() })
	}
	observe := func(phase string) {
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/prepared=%t", phase, tc.name, prepared), func(t *testing.T) {
					q := "SELECT " + strings.TrimSuffix(tc.check, "=e") + " AS value FROM sc2_" + tc.name + " ORDER BY e"
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
					defer rows.Close()
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "BYTEA" {
						t.Fatal("BYTEA metadata", types, err)
					}
					if !rows.Next() {
						t.Fatal(rows.Err())
					}
					var got []byte
					if err := rows.Scan(&got); err != nil || !bytes.Equal(got, stringCoreReview2ByteaValue(tc.name)) {
						t.Fatal("byte-set result", got, err)
					}
					if !rows.Next() {
						t.Fatal("missing NULL", rows.Err())
					}
					var null any
					if err := rows.Scan(&null); err != nil || null != nil {
						t.Fatal("strict NULL", null, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("runtime cardinality", rows.Err())
					}
					treeQ := "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='sc2c_" + tc.name + "'"
					if prepared {
						s, e := db.Prepare(treeQ)
						if e != nil {
							t.Fatal(e)
						}
						defer s.Close()
						rows, err = s.Query()
					} else {
						rows, err = db.Query(treeQ)
					}
					if err != nil {
						t.Fatal(err)
					}
					readTree(t, rows, tc.name)
					for _, mode := range []struct{ suffix, want string }{{"", tc.compact}, {",false", tc.compact}, {",true", tc.pretty}} {
						t.Run(mode.suffix, func(t *testing.T) {
							deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_"+tc.name+"'", mode.want)
						})
					}
				})
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
	for _, h := range held {
		if got := readTree(t, h.rows, h.name); got != h.tree {
			t.Fatal("held carrier changed across replacement", h.name, got, h.tree)
		}
	}
}

func TestStringCoreReview2ByteaGrouping(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE sc2_group(v BYTEA,s BYTEA)")
	execSQL(t, db, "INSERT INTO sc2_group VALUES($1,$2),(NULL,NULL)", []byte("xxYxx"), []byte("x"))
	for _, trim := range []struct{ name, dir string }{{"btrim", "BOTH"}, {"ltrim", "LEADING"}, {"rtrim", "TRAILING"}} {
		ordinary, special := trim.name+"(v,s)", "trim("+trim.dir+" s FROM v)"
		for _, pair := range [][2]string{{ordinary, special}, {special, ordinary}} {
			q := "SELECT " + pair[0] + " AS value FROM sc2_group GROUP BY " + pair[1] + " ORDER BY 1"
			want := [][]any{{stringCoreReview2ByteaValue(trim.name)}, {nil}}
			wantTuples(t, db, q, want)
			s, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.Query()
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			for _, expected := range want {
				if !rows.Next() {
					t.Fatal("missing grouped row", rows.Err())
				}
				var value []byte
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				if expected[0] == nil {
					if value != nil {
						t.Fatal("grouped NULL", value)
					}
				} else if !bytes.Equal(value, expected[0].([]byte)) {
					t.Fatal("grouped value", value)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("group cardinality", rows.Err())
			}
			rows.Close()
			s.Close()
		}
	}
}
