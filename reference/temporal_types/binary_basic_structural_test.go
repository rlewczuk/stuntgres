package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryBasicStructuralReplay(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			db := p.Memory
			// These SQL expectations are independent literals, not output learned from
			// the native provider. The reference witness asserts the same spellings on PG.
			tree := func(id int, body string) string {
				return fmt.Sprintf("{type %d typmod -1 collation 0 derivation 0 node %s}", id, body)
			}
			v, s := tree(17, "Var attnum 1"), tree(17, "Var attnum 2")
			binary := func(id int, name string, args ...string) string {
				return tree(id, "Binary "+name+" "+strings.Join(args, " "))
			}
			cases := []struct{ name, expr, compact, pretty, subtree string }{
				{"len", "length(v)>0", "(length(v) > 0)", "length(v) > 0", binary(23, "length", v)},
				{"oct", "octet_length(v)>0", "(octet_length(v) > 0)", "octet_length(v) > 0", binary(23, "octet_length", v)},
				{"bit", "bit_length(v)>0", "(bit_length(v) > 0)", "bit_length(v) > 0", binary(23, "bit_length", v)},
				{"bt", "btrim(v,s)=e", "(btrim(v, s) = e)", "btrim(v, s) = e", binary(17, "btrim", v, s)},
				{"lt", "ltrim(v,s)=e", "(ltrim(v, s) = e)", "ltrim(v, s) = e", binary(17, "ltrim", v, s)},
				{"rt", "rtrim(v,s)=e", "(rtrim(v, s) = e)", "rtrim(v, s) = e", binary(17, "rtrim", v, s)},
				{"cat", "v||s=e", "((v || s) = e)", "(v || s) = e", binary(17, "||", v, s)},
				{"nested", "length(v||(s||v))>0", "(length((v || (s || v))) > 0)", "length(v || (s || v)) > 0", binary(23, "length", binary(17, "||", v, binary(17, "||", s, v)))},
			}
			for _, tc := range cases {
				referenceBinaryExec(t, p, "CREATE TABLE binary_"+tc.name+"(v BYTEA,s BYTEA,e BYTEA,CONSTRAINT binary_ck_"+tc.name+" CHECK("+tc.expr+"))", nonContext)
			}
			metadata := func(t *testing.T, types []*sql.ColumnType) {
				t.Helper()
				if len(types) != 2 || types[0].Name() != "conname" || types[1].Name() != "conbin" || types[0].DatabaseTypeName() != "NAME" || types[1].DatabaseTypeName() != "PG_NODE_TREE" || types[0].ScanType() != reflect.TypeFor[string]() || types[1].ScanType() != reflect.TypeFor[string]() {
					t.Fatal(types)
				}
			}
			structural := func(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, wantName, wantTree string) {
				t.Helper()
				// Inspect the exact descriptors captured while rows were open:
				// database/sql closes Rows automatically at EOF.
				metadata(t, types)
				if !rows.Next() {
					t.Fatal("missing structural row", rows.Err())
				}
				var name, text string
				if err := rows.Scan(&name, &text); err != nil {
					t.Fatal(err)
				}
				if name != wantName || !strings.HasPrefix(text, "{type 16 typmod -1 collation 0 derivation 0 node Compare ") || !strings.Contains(text, wantTree) {
					t.Fatalf("structural row %q %q; want name %q subtree %q", name, text, wantName, wantTree)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra or failed structural row", rows.Err())
				}
				metadata(t, types)
				rows.Close()
				metadata(t, types)
			}
			observe := func() {
				for _, tc := range cases {
					t.Run(tc.name+"/sql", func(t *testing.T) {
						for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
							for _, prepared := range []bool{false, true} {
								referenceBinaryDeparseText(t, p, prepared, "SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='binary_ck_"+tc.name+"'", mode.want, nonContext)
								referenceBinaryDeparseText(t, p, prepared, "SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='binary_ck_"+tc.name+"'", "CHECK ("+mode.want+")", nonContext)
							}
						}
					})
					t.Run(tc.name+"/tree", func(t *testing.T) {
						q := "SELECT conname,conbin FROM pg_catalog.pg_constraint WHERE conname='binary_ck_" + tc.name + "'"
						for _, prepared := range []bool{false, true} {
							var rows *sql.Rows
							var err error
							if prepared {
								stmt, e := db.Prepare(q)
								if e != nil {
									t.Fatal(e)
								}
								defer stmt.Close()
								rows, err = stmt.Query()
							} else {
								rows, err = db.Query(q)
							}
							if err != nil {
								t.Fatal(err)
							}
							defer rows.Close()
							types, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							structural(t, rows, types, "binary_ck_"+tc.name, tc.subtree)
						}
					})
				}
			}
			observe()
			// Retain immutable structural carriers and analyzed metadata across replacement.
			var retained []*sql.Rows
			var retainedTypes [][]*sql.ColumnType
			for _, tc := range cases {
				rows, err := db.Query("SELECT conname,conbin FROM pg_catalog.pg_constraint WHERE conname='binary_ck_" + tc.name + "'")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				retained = append(retained, rows)
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				metadata(t, types)
				retainedTypes = append(retainedTypes, types)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(context.Background(), p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(context.Background(), p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(context.Background(), p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			observe()
			for i, tc := range cases {
				t.Run(tc.name+"/retained", func(t *testing.T) {
					structural(t, retained[i], retainedTypes[i], "binary_ck_"+tc.name, tc.subtree)
				})
			}
			// Replay preserves executable selected checks, not just renderable metadata.
			referenceBinaryExec(t, p, `INSERT INTO binary_bt VALUES('\x00ff8041008042ff00','\xff8000','\x41008042')`, nonContext)
			selected := fixture.Select("SELECT v FROM binary_bt", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00"}})
			selected.NonContext = nonContext
			fixture.RunStep(t, p.Ctx, db, p.MemoryRecord, selected)
		})
	}
}

func referenceBinaryExec(t *testing.T, p *fixture.Pair, q string, nonContext bool) {
	t.Helper()
	count := int64(0)
	if strings.HasPrefix(q, "INSERT") {
		count = 1
	}
	step := fixture.Exec(q, count)
	step.NonContext = nonContext
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
}

func referenceBinaryDeparseText(t *testing.T, p *fixture.Pair, prepared bool, q string, want string, nonContext bool) {
	t.Helper()
	step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
	step.Prepared = prepared
	step.NonContext = nonContext
	step.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		ct, err := rows.ColumnTypes()
		if err != nil || len(ct) != 1 || ct[0].ScanType() != reflect.TypeFor[string]() {
			t.Fatal(ct, err)
		}
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
}
