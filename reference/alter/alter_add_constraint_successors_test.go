package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// RI uses the immediate tuple's transaction birth, not its committed base.
func TestReferenceAlterAddConstraintSuccessorWitness(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(9,'old')", 1))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) NOT VALID", 0))
				oid := addConstraintInvalid(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID")
				held := addConstraintHeld(t, p, b, c, "UPDATE t SET note=$1")
				write := func(step fixture.Step) {
					if prepared {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
					} else {
						run(step)
					}
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT before_first", 0))
				write(fixture.Exec(held.SQL, 1, "first"))
				run(fixture.Exec("SAVEPOINT after_first", 0))
				write(fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", "fk", "a", "9", "p"), "second"))
				run(fixture.ErrorDiagnostic("SELECT a FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				run(fixture.Exec("ROLLBACK TO after_first", 0))
				run(addConstraintRows([]any{int64(9), "first"}))
				write(fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", "fk", "a", "9", "p"), "still-own"))
				run(fixture.Exec("ROLLBACK TO before_first", 0))
				run(addConstraintRows([]any{int64(9), "old"}))
				write(fixture.Exec(held.SQL, 1, "again-first"))
				run(fixture.Exec("COMMIT", 0))
				write(fixture.Exec(held.SQL, 1, "committed"))
				run(addConstraintRows([]any{int64(9), "committed"}))
				if got := addConstraintInvalid(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID"); got != oid {
					t.Fatal("predecessor recovery changed invalid identity", oid, got)
				}
			})
		})
	}
}

func TestReferenceAlterAddConstraintValidityAttributes(t *testing.T) {
	for _, kind := range addConstraintCases() {
		if kind.kind != "c" && kind.kind != "n" && kind.kind != "f" {
			continue
		}
		for _, prepared := range []bool{false, true} {
			t.Run(kind.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES($1,'old')", 1, kind.bad))
					suffix := " NOT VALID NOT VALID"
					name, definition := "ck", "CHECK ((a > 0)) NOT VALID"
					if kind.kind == "n" {
						name, definition = "nn", "NOT NULL a NOT VALID"
					}
					if kind.kind == "f" {
						name, definition = "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID"
					}
					if kind.kind == "c" {
						suffix = " NOT VALID NO INHERIT NOT VALID NO INHERIT"
						definition = "CHECK ((a > 0)) NO INHERIT NOT VALID"
					}
					step := fixture.Exec("ALTER TABLE t ADD "+kind.clause+suffix, 0)
					if prepared {
						step = fixture.Prepared(step)
					}
					run(step)
					addConstraintInvalid(t, p, b, c, "t", name, definition)
					held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, kind.write, kind.bad, "bad"))
					run(addConstraintRows([]any{kind.bad, "old"}))
				})
			})
		}
	}
}

// LIKE builds valid constraints for a new empty table, not copies of historical
// validation state. Existing-definition remaps and Restore have different rules.
func TestReferenceAlterAddConstraintFreshLikeValidity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(NULL,'null'),(-1,'bad')", 2))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID,ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0))
				nn := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID")
				ck := addConstraintInvalid(t, p, b, c, "t", "ck", "CHECK ((a > 0)) NOT VALID")
				step := fixture.Exec("CREATE TABLE fresh(LIKE t INCLUDING CONSTRAINTS)", 0)
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				run(fixture.Select("SELECT a,note FROM fresh", []string{"a", "note"}, []string{"INT4", "TEXT"}, nil))
				run(fixture.Select("SELECT contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='fresh'::regclass::oid ORDER BY contype", []string{"contype", "pg_get_constraintdef"}, []string{"CHAR", "TEXT"}, [][]any{{"c", "CHECK ((a > 0))"}, {"n", "NOT NULL a"}}))
				var newNN, newCK int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='fresh'::regclass::oid AND contype='n'").Scan(&newNN); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='fresh'::regclass::oid AND contype='c'").Scan(&newCK); err != nil {
					t.Fatal(err)
				}
				if newNN <= 0 || newCK <= 0 || newNN == nn || newCK == ck {
					t.Fatal("LIKE reused source logical identities")
				}
				if b.PG {
					run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='fresh'::regclass::oid ORDER BY contype", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}, {true}}))
				}
				held := addConstraintHeld(t, p, b, c, "INSERT INTO fresh VALUES($1,$2)")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, fixture.PrimaryKeyReferenceNull("fresh", "a", "null, bad"), nil, "bad"))
				bad := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "fresh" violates check constraint "ck"`)
				bad.Schema, bad.Table, bad.Constraint, bad.Detail = "public", "fresh", "ck", "Failing row contains (-1, bad)."
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, bad, int64(-1), "bad"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, int64(1), "good"))
				run(fixture.Select("SELECT a,note FROM fresh", []string{"a", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "good"}}))
				run(fixture.Select("SELECT a,note FROM t ORDER BY note", []string{"a", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "bad"}, {nil, "null"}}))
				if addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID") != nn || addConstraintInvalid(t, p, b, c, "t", "ck", "CHECK ((a > 0)) NOT VALID") != ck {
					t.Fatal("LIKE changed source validity/identity")
				}
			})
		})
	}
}
