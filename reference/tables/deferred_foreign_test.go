package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredForeignKeyTransactions(t *testing.T) {
	for _, repair := range []string{"INSERT INTO p VALUES(9)", "DELETE FROM c", "UPDATE c SET pid=1", ""} {
		for _, goCommit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/go=%t", repair, goCommit), func(t *testing.T) {
				fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
					var tx *sql.Tx
					var executor fixture.Executor = c
					if goCommit {
						var err error
						tx, err = c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						executor = tx
					} else {
						run(fixture.Exec("BEGIN", 0))
					}
					step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
					step(fixture.Exec("CREATE TABLE private_marker(v INT)", 0))
					step(fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1)", 1, int64(9))))
					step(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, [][]any{{int64(9)}}))
					if repair != "" {
						step(fixture.Exec(repair, 1))
					}
					want := fixture.ForeignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`)
					if goCommit {
						err := tx.Commit()
						if repair == "" {
							fixture.AssertDiagnostic(t, err, &want)
						} else {
							fixture.State(t, err, "")
						}
					} else {
						if repair == "" {
							step(fixture.ErrorDiagnostic("COMMIT", want))
						} else {
							step(fixture.Exec("COMMIT", 0))
						}
					}
					rows := [][]any(nil)
					if repair == "INSERT INTO p VALUES(9)" {
						rows = [][]any{{int64(9)}}
					}
					if repair == "UPDATE c SET pid=1" {
						rows = [][]any{{int64(1)}}
					}
					run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, rows))
					if repair == "" {
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='private_marker'", []string{"relname"}, []string{"NAME"}, nil))
						run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(9)", want))
					}
					run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
				})
			})
		}
	}
	for _, repair := range []string{"INSERT INTO p VALUES(1)", "UPDATE c SET pid=2", "DELETE FROM c"} {
		t.Run("parent/"+repair, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
				run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("DELETE FROM p WHERE id=1", 1))
				run(fixture.Prepared(fixture.Exec(repair, 1)))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("UPDATE p SET id=3 WHERE id=2", 1))
				run(fixture.Exec("UPDATE c SET pid=3 WHERE pid=2", map[bool]int64{true: 1, false: 0}[repair == "UPDATE c SET pid=2"]))
				run(fixture.Exec("COMMIT", 0))
			})
		})
	}
}

func TestReferenceDeferredForeignKeyActions(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
		t.Run(action, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p ON DELETE "+action+" DEFERRABLE INITIALLY DEFERRED)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
				run(fixture.Exec("INSERT INTO c VALUES(2)", 1))
				run(fixture.Exec("BEGIN", 0))
				if action == "RESTRICT" {
					run(fixture.ErrorDiagnostic("DELETE FROM p WHERE id=2", foreignActionsRestrict("p", "c", "fk", `Key (id)=(2) is referenced from table "c".`)))
					run(fixture.Exec("ROLLBACK", 0))
					return
				}
				run(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE id=2", 1)))
				rows := [][]any{{int64(2)}}
				switch action {
				case "CASCADE":
					rows = nil
				case "SET NULL":
					rows = [][]any{{nil}}
				case "SET DEFAULT":
					rows = [][]any{{int64(1)}}
				}
				run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, rows))
				if action == "NO ACTION" {
					run(fixture.Exec("INSERT INTO p VALUES(2)", 1))
				}
				run(fixture.Exec("COMMIT", 0))
			})
		})
	}
	for _, match := range []string{"SIMPLE", "FULL"} {
		t.Run(match, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
				run(fixture.Exec("CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH "+match+" INITIALLY DEFERRED)", 0))
				run(fixture.Exec("INSERT INTO c VALUES(NULL,NULL)", 1))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO c VALUES(1,NULL)", 1))
				if match == "FULL" {
					run(fixture.ErrorDiagnostic("COMMIT", fixture.ForeignKeyChild("c", "fk", "MATCH FULL does not allow mixing of null and nonnull key values.")))
				} else {
					run(fixture.Exec("COMMIT", 0))
				}
			})
		})
	}
	t.Run("mixed-immediate", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(a INT CONSTRAINT deferred_fk REFERENCES p INITIALLY DEFERRED,b INT CONSTRAINT immediate_fk REFERENCES p)", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(9,8)", fixture.ForeignKeyChild("c", "immediate_fk", `Key (b)=(8) is not present in table "p".`)))
			run(fixture.Exec("ROLLBACK", 0))
		})
	})
}

func TestReferenceDeferredConstraintEnforcement(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED NOT ENFORCED)", 0))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
		run(fixture.Exec("INSERT INTO c VALUES(8)", 1))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Select("SELECT conenforced,convalidated FROM pg_constraint WHERE conname='fk'", []string{"conenforced", "convalidated"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
		run(fixture.Select("SELECT count(*)::int FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='fk')", []string{"count"}, []string{"INT4"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE TABLE n(pid INT)", 0))
		run(fixture.Exec("INSERT INTO n VALUES(9)", 1))
		run(fixture.Exec("ALTER TABLE n ADD CONSTRAINT nf FOREIGN KEY(pid) REFERENCES p INITIALLY DEFERRED NOT VALID", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO n VALUES(8)", 1))
		run(fixture.Exec("DELETE FROM n WHERE pid=8", 1))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Select("SELECT conenforced,convalidated FROM pg_constraint WHERE conname='nf'", []string{"conenforced", "convalidated"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		run(fixture.ErrorDiagnostic("INSERT INTO n VALUES(8)", fixture.ForeignKeyChild("n", "nf", `Key (pid)=(8) is not present in table "p".`)))
	})
}
