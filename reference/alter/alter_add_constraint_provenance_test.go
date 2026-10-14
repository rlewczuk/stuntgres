package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Released RI successor rules distinguish prior transaction-born rows from
// committed predecessors. NOT VALID skips admission-time historical rows;
// catalog-only projection is not a subsequent INSERT/UPDATE operation.
func TestReferenceAlterAddConstraintProvenance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run("private_orphan_invalid_add_type_and_future_writes/"+orderingRoute(prepared), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := provenanceSetup(t, p, b, c)
				insertSQL := "INSERT INTO t VALUES($1,$2)"
				updateSQL := "UPDATE t SET b=$1 WHERE a=$2"
				insert, update := addConstraintHeld(t, p, b, c, insertSQL), addConstraintHeld(t, p, b, c, updateSQL)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("INSERT INTO t VALUES(9,1)", 1))
				q := "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) NOT VALID"
				step := fixture.Exec(q, 0)
				step.Prepared = prepared
				run(step)
				definition := "FOREIGN KEY (a) REFERENCES p(id) NOT VALID"
				original := addConstraintInvalid(t, p, b, c, "t", "fk", definition)
				run(fixture.Exec("SAVEPOINT before_types", 0))
				step = fixture.Exec("ALTER TABLE t ALTER b TYPE BIGINT", 0)
				step.Prepared = prepared
				run(step)
				if got := addConstraintInvalid(t, p, b, c, "t", "fk", definition); got != original {
					t.Fatal("unrelated TYPE changed FK identity", original, got)
				}
				provenanceRows(t, p, b, c, "INT4", "INT8", [][]any{{int64(9), int64(1)}})
				step = fixture.Exec("ALTER TABLE t ALTER a TYPE BIGINT", 0)
				step.Prepared = prepared
				run(step)
				affected := addConstraintInvalid(t, p, b, c, "t", "fk", definition)
				if affected == original {
					t.Fatal("affected TYPE retained old FK definition identity", original)
				}
				provenanceRows(t, p, b, c, "INT8", "INT8", [][]any{{int64(9), int64(1)}})

				bad := addConstraintFK("t", "fk", "a", "9", "p")
				for _, action := range []string{"insert", "unchanged_key_update"} {
					run(fixture.Exec("SAVEPOINT future_write", 0))
					if action == "insert" {
						fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.ErrorDiagnostic(insertSQL, bad, 9, 2))
					} else {
						fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.ErrorDiagnostic(updateSQL, bad, 2, 9))
					}
					run(fixture.ErrorDiagnostic("SELECT a,b FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO SAVEPOINT future_write", 0))
					if got := addConstraintInvalid(t, p, b, c, "t", "fk", definition); got != affected {
						t.Fatal("failed future write changed FK identity", affected, got)
					}
					provenanceRows(t, p, b, c, "INT8", "INT8", [][]any{{int64(9), int64(1)}})
				}
				// A real parent repair admits both genuinely new successor paths;
				// it does not validate or promote the invalid catalog definition.
				run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
				fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateSQL, 1, 2, 9))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 9, 3))
				if got := addConstraintInvalid(t, p, b, c, "t", "fk", definition); got != affected {
					t.Fatal("repair promoted or replaced FK", affected, got)
				}
				provenanceRows(t, p, b, c, "INT8", "INT8", [][]any{{int64(9), int64(2)}, {int64(9), int64(3)}})
				run(fixture.Exec("ROLLBACK TO SAVEPOINT before_types", 0))
				if got := addConstraintInvalid(t, p, b, c, "t", "fk", definition); got != original {
					t.Fatal("savepoint did not restore original FK identity", original, got)
				}
				provenanceRows(t, p, b, c, "INT4", "INT4", [][]any{{int64(9), int64(1)}})
				run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Exec("COMMIT", 0))
				// The same unchanged-key UPDATE now has a committed predecessor:
				// it must succeed even though the old orphan remains unvalidated.
				fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateSQL, 1, 4, 9))
				addConstraintInvalid(t, p, b, c, "t", "fk", definition)
				provenanceRows(t, p, b, c, "INT4", "INT4", [][]any{{int64(9), int64(4)}})
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.ErrorDiagnostic(insertSQL, bad, 9, 5))
				provenanceRows(t, p, b, c, "INT4", "INT4", [][]any{{int64(9), int64(4)}})
			})
		})
		t.Run("private_orphan_valid_add_rejects/"+orderingRoute(prepared), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := provenanceSetup(t, p, b, c)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("INSERT INTO t VALUES(9,1)", 1))
				run(fixture.Exec("SAVEPOINT valid_add", 0))
				addConstraintError(t, p, c, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id)", addConstraintFK("t", "fk", "a", "9", "p"), prepared, "execute")
				run(fixture.Exec("ROLLBACK TO SAVEPOINT valid_add", 0))
				provenanceRows(t, p, b, c, "INT4", "INT4", [][]any{{int64(9), int64(1)}})
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
				run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
				step := fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id)", 0)
				step.Prepared = prepared
				run(step)
				run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (a) REFERENCES p(id)"}}))
				run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='fk'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(10,2)", addConstraintFK("t", "fk", "a", "10", "p")))
				provenanceRows(t, p, b, c, "INT4", "INT4", [][]any{{int64(9), int64(1)}})
			})
		})
		t.Run("valid_type_using_orphan_rejects/"+orderingRoute(prepared), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := provenanceSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id)", 0))
				before := addConstraintCatalog(t, p, c)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT valid_type", 0))
				addConstraintError(t, p, c, "ALTER TABLE t ALTER a TYPE BIGINT USING a+8", addConstraintFK("t", "fk", "a", "9", "p"), prepared, "execute")
				run(fixture.Exec("ROLLBACK TO SAVEPOINT valid_type", 0))
				if got := addConstraintCatalog(t, p, c); !reflect.DeepEqual(before, got) {
					t.Fatal("failed valid TYPE changed catalog", before, got)
				}
				provenanceRows(t, p, b, c, "INT4", "INT4", [][]any{{int64(1), int64(1)}})
				step := fixture.Exec("ALTER TABLE t ALTER a TYPE BIGINT", 0)
				step.Prepared = prepared
				run(step)
				run(fixture.Exec("COMMIT", 0))
				provenanceRows(t, p, b, c, "INT8", "INT4", [][]any{{int64(1), int64(1)}})
				run(fixture.ErrorDiagnostic("UPDATE t SET a=9", addConstraintFK("t", "fk", "a", "9", "p")))
				provenanceRows(t, p, b, c, "INT8", "INT4", [][]any{{int64(1), int64(1)}})
			})
		})
	}
}

func provenanceSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func(fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
	run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
	run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
	return run
}

func provenanceRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, aType, bType string, rows [][]any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a,b FROM t ORDER BY a,b", []string{"a", "b"}, []string{aType, bType}, rows))
}
