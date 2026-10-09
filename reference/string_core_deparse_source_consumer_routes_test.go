package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceStringCoreReview2DeparseSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			cases := stringCoreReview2Cases()
			for _, tc := range cases {
				stringCoreControlExec(t, db, "CREATE TABLE sc2_"+tc.name+"("+tc.decl+",CONSTRAINT sc2c_"+tc.name+" CHECK("+tc.check+"))")
			}
			observe := func(phase string) {
				for _, tc := range cases {
					for _, mode := range []struct{ suffix, want string }{{"", tc.compact}, {",false", tc.compact}, {",true", tc.pretty}} {
						for _, prepared := range []bool{false, true} {
							t.Run(fmt.Sprintf("%s/%s/%s/prepared=%t", phase, tc.name, mode.suffix, prepared), func(t *testing.T) {
								t.Run("expression", func(t *testing.T) {
									stringCoreControlText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_"+tc.name+"'", mode.want)
								})
								t.Run("constraint", func(t *testing.T) {
									stringCoreControlText(t, db, prepared, "SELECT pg_get_constraintdef(oid"+mode.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_"+tc.name+"'", "CHECK ("+mode.want+")")
								})
							})
						}
					}
				}
			}
			observe("initial")
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			observe("restored")
		})
	}
}

func TestReferenceStringCoreReview2ByteaGroupingSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE sc2_group(v BYTEA,s BYTEA)")
			stringCoreControlExec(t, db, "INSERT INTO sc2_group VALUES($1,$2),(NULL,NULL)", []byte("xxYxx"), []byte("x"))
			for _, trim := range []struct{ name, dir string }{{"btrim", "BOTH"}, {"ltrim", "LEADING"}, {"rtrim", "TRAILING"}} {
				ordinary, special := trim.name+"(v,s)", "trim("+trim.dir+" s FROM v)"
				for _, pair := range [][2]string{{ordinary, special}, {special, ordinary}} {
					q := "SELECT " + pair[0] + " AS value FROM sc2_group GROUP BY " + pair[1] + " ORDER BY 1"
					for _, prepared := range []bool{false, true} {
						expected := stringCoreReview2ByteaValue(trim.name)
						step := referenceSelect(q, []string{"value"}, []string{"BYTEA"}, [][]any{{string(expected)}, {nil}})
						step.nonContext = true
						if prepared {
							step = referencePrepared(step)
							step.checkScan = func(t *testing.T, rows *sql.Rows, index int) {
								var value []byte
								if err := rows.Scan(&value); err != nil {
									t.Fatal(err)
								}
								if index == 1 {
									if value != nil {
										t.Fatal("grouped NULL", value)
									}
								} else if !bytes.Equal(value, expected) {
									t.Fatal("grouped value", value)
								}
							}
						}
						referenceRunStep(t, p.ctx, db, backend.recorder, step)
					}
				}
			}
		})
	}
}
