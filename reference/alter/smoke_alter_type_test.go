package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeAlterTypeCheckFloatingSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					m, db, ctx, notices := fixture.MigrationAlterFixture(t, p, backend)
					run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, ctx, db, m.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(c REAL CONSTRAINT ck CHECK(c>'1.0000001' AND c<>NULL))", 0))
					run(fixture.Exec("INSERT INTO t VALUES(2),(NULL)", 2))
					action := fixture.Exec("ALTER TABLE t ALTER c TYPE DOUBLE PRECISION USING c", 0)
					action.Prepared = prepared
					run(action)
					reject := func() {
						run(fixture.ImplicitError("INSERT INTO t VALUES(1.00000011)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (1.00000011).", Schema: "public", Table: "t", Constraint: "ck"}))
					}
					accept := func() { s := fixture.Exec("INSERT INTO t VALUES(1.00000013)", 1); s.Prepared = prepared; run(s) }
					check := func(want [][]any) {
						rows, err := db.Query("SELECT c FROM t ORDER BY c")
						if err != nil {
							t.Fatal(err)
						}
						m.DropRows(rows, []string{"c"}, []string{"FLOAT8"}, want)
					}
					reject()
					check([][]any{{float64(2)}, {nil}})
					accept()
					check([][]any{{float64(1.00000013)}, {float64(2)}, {nil}})
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if strings.Contains(dump.String(), "$1") || strings.Contains(dump.String(), "ALTER TABLE") {
							t.Fatal("private TYPE history leaked", dump.String())
						}
						if err := stuntgres.Reset(ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						reject()
						check([][]any{{float64(1.00000013)}, {float64(2)}, {nil}})
						accept()
						check([][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {nil}})
					}
					if len(*notices) != 0 {
						t.Fatal(*notices)
					}
				})
			}
		})
	}
}
