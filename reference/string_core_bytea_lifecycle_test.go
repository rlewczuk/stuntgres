package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceStringCoreReview2ByteaLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			type heldCarrier struct {
				rows       *sql.Rows
				name, tree string
			}
			var held []heldCarrier

			var cases []stringCoreReview2Case
			for _, tc := range stringCoreReview2Cases() {
				if !strings.HasPrefix(tc.name, "bytea_") {
					continue
				}
				cases = append(cases, tc)
				stringCoreControlExec(t, db, "CREATE TABLE sc2_"+tc.name+"("+tc.decl+",CONSTRAINT sc2c_"+tc.name+" CHECK("+tc.check+"))")
				stringCoreControlExec(t, db, "INSERT INTO sc2_"+tc.name+" VALUES($1,$2,$3)", []byte("xxYxx"), []byte("x"), stringCoreReview2ByteaValue(tc.name))
				stringCoreControlExec(t, db, "INSERT INTO sc2_"+tc.name+" VALUES(NULL,NULL,NULL)")
				q := "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='sc2c_" + tc.name + "'"
				rows, err := db.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				tree := stringCoreByteaConstraintTree(t, rows, tc.name, backend.pg)
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
							stringCoreByteaConstraintTree(t, rows, tc.name, backend.pg)
							for _, mode := range []struct{ suffix, want string }{{"", tc.compact}, {",false", tc.compact}, {",true", tc.pretty}} {
								t.Run(mode.suffix, func(t *testing.T) {
									stringCoreControlText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_"+tc.name+"'", mode.want)
								})
							}
						})
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
			for _, h := range held {
				if got := stringCoreByteaConstraintTree(t, h.rows, h.name, backend.pg); got != h.tree {
					t.Fatal("held carrier changed across replacement", h.name, got, h.tree)
				}
			}
		})
	}
}

func stringCoreByteaConstraintTree(t *testing.T, rows *sql.Rows, name string, postgres bool) string {
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
	if postgres {
		if !strings.Contains(tree, "OPEXPR") || !strings.Contains(tree, ":opresulttype 16") || !strings.Contains(tree, ":varattno 1") || !strings.Contains(tree, ":varattno 2") {
			t.Fatal("PG typed recursive BYTEA carrier", name, tree)
		}
	} else {
		if !strings.Contains(tree, "type 16") || !strings.Contains(tree, "Binary ") || !strings.Contains(tree, "Var attnum 1") || !strings.Contains(tree, "Var attnum 2") {
			t.Fatal("typed recursive BYTEA carrier", name, tree)
		}
		if name == "bytea_nested" && strings.Count(tree, "Binary ") != 2 {
			t.Fatal("missing nested binary child", tree)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("carrier cardinality", rows.Err())
	}
	return tree
}
