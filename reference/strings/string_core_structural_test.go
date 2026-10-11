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

func TestReferenceStringCoreReviewStructural(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
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
				scan := reflect.TypeFor[string]()
				if backend.PG {
					scan = reflect.TypeFor[any]()
				}
				if len(types) != 1 || types[0].DatabaseTypeName() != "PG_NODE_TREE" || types[0].ScanType() != scan {
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
				if backend.PG {
					if !strings.Contains(tree, ":funcresulttype "+id) && !strings.Contains(tree, ":opresulttype "+id) && !strings.Contains(tree, "BOOLEXPR") {
						t.Fatal("PG typed structural carrier", tc.name, tree)
					}
					if check && !strings.Contains(tree, ":varattno 1") {
						t.Fatal("PG source operand", tree)
					}
				} else {
					if !strings.HasPrefix(tree, "{type "+id+" typmod -1 ") || !strings.Contains(tree, "String "+tc.provider) {
						t.Fatal("missing typed recursive string carrier", tc.name, tree)
					}
					if check && !strings.Contains(tree, "Var attnum 1") {
						t.Fatal("lost source operand", tree)
					}
					if tc.name == "nested" && (!strings.Contains(tree, "String normalize") || !strings.Contains(tree, "String btrim")) {
						t.Fatal("lost nested providers", tree)
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra carrier", rows.Err())
				}
				metadata(t, types)
				return tree
			}
			for _, tc := range stringCoreReviewDeparseCases() {
				fixture.StringCoreControlExec(t, db, "CREATE TABLE scd_"+tc.name+"(v TEXT,s TEXT,e TEXT,n INT,d "+tc.typ+" DEFAULT ("+tc.inputDefault+"),CONSTRAINT scc_"+tc.name+" CHECK("+tc.inputCheck+"))")
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
			if !backend.PG {
				controlDB := p.Native
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
			fixture.StringCoreControlExec(t, db, "ALTER TABLE scd_btrim1 RENAME v TO renamed")
			for _, h := range held {
				t.Run(h.tc.name+"/held", func(t *testing.T) {
					if tree := read(t, h.rows, h.types, h.tc, h.check); tree != h.tree {
						t.Fatal("held carrier changed after publication replacement", tree, h.tree)
					}
					metadata(t, h.types)
				})
			}
		})
	}
}
