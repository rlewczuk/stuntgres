package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddConstraintFeatureSmoke(t *testing.T) {
	for _, kind := range addConstraintCases() {
		t.Run(kind.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'keep'),($1,'bad')", 2, kind.bad))
				cat, rels := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
				var dump []byte
				if !b.PG {
					dump = addConstraintDump(t, p)
				}
				run(fixture.ErrorDiagnostic("ALTER TABLE t ADD "+kind.clause, kind.build))
				addConstraintUnchanged(t, p, b, c, cat, rels, dump)
				if kind.kind == "c" || kind.kind == "n" || kind.kind == "f" {
					run(fixture.Exec("ALTER TABLE t ADD "+kind.clause+" NOT VALID", 0))
					held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, kind.write, kind.bad, "bad"))
					if !b.PG {
						dump = addConstraintDump(t, p)
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(dump, addConstraintDump(t, p)) {
							t.Fatal("smoke invalid roundtrip")
						}
						run(fixture.ErrorDiagnostic(held.SQL, kind.write, kind.bad, "bad"))
					}
					run(fixture.Exec("UPDATE t SET a=2 WHERE note='bad'", 1))
				} else {
					run(fixture.Exec("UPDATE t SET a=2 WHERE note='bad'", 1))
					run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD "+kind.clause, 0)))
				}
				held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 3, "new"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, kind.write, kind.bad, "bad"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 4, nil))
				run(addConstraintRows([]any{int64(1), "keep"}, []any{int64(2), "bad"}, []any{int64(3), "new"}, []any{int64(4), nil}))
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, "SELECT a,note FROM t ORDER BY a", []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{kind.kind != "n" && kind.kind != "p", true}, 4)
			})
		})
	}
	for _, tc := range []struct{ clause, message, position string }{
		{"UNIQUE(a)", "UNIQUE constraints cannot be marked NOT VALID", "29"},
		{"PRIMARY KEY(a)", "PRIMARY KEY constraints cannot be marked NOT VALID", "34"},
		{"EXCLUDE(a WITH =)", "EXCLUDE constraints cannot be marked NOT VALID", "37"},
	} {
		t.Run(tc.clause, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				addConstraintSetup(t, p, b, c)
				want := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
				want.Position = tc.position
				addConstraintError(t, p, c, "ALTER TABLE t ADD "+tc.clause+" NOT VALID", want, true, "prepare")
			})
		})
	}
}
