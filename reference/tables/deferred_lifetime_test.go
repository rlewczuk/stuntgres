package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredConstraintPreparedLifetime(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
		q := "INSERT INTO c VALUES($1)"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		step := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
		}
		run(fixture.Exec("ALTER TABLE c RENAME CONSTRAINT fk TO renamed_fk", 0))
		run(fixture.Exec("ALTER TABLE c ALTER pid TYPE BIGINT", 0))
		run(fixture.Exec("BEGIN", 0))
		step(fixture.Exec(q, 1, int64(1)))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("COMMIT", 0))
		step(fixture.ErrorDiagnostic(q, fixture.ForeignKeyChild("c", "renamed_fk", `Key (pid)=(9) is not present in table "p".`), int64(9)))
		for _, prepared := range []bool{false, true} {
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT pid FROM c", []string{"pid"}, []string{"INT8"}, []bool{true}, 1)
		}
		run(fixture.Exec("DROP TABLE c", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT replacement_fk REFERENCES p DEFERRABLE)", 0))
		run(fixture.Exec("BEGIN", 0))
		step(fixture.ErrorDiagnostic(q, fixture.ForeignKeyChild("c", "replacement_fk", `Key (pid)=(9) is not present in table "p".`), int64(9)))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT conname,contype,condeferrable,condeferred FROM pg_constraint WHERE conrelid='c'::regclass", []string{"conname", "contype", "condeferrable", "condeferred"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{{"replacement_fk", "f", true, false}}))
		meta := fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conname='replacement_fk'", []any{"replacement_fk", true, false})
		meta.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, column := range columns {
				want := reflect.TypeFor[bool]()
				if i == 0 {
					want = reflect.TypeFor[string]()
					if b.PG {
						want = reflect.TypeFor[any]()
					}
				}
				if column.ScanType() != want {
					t.Fatal("catalog ScanType", i, column.ScanType(), want)
				}
				if b.PG {
					value, known := column.Nullable()
					if value || known {
						t.Fatal("pq nullability must remain unavailable", i, value, known)
					}
				}
			}
		}
		run(meta)
		run(fixture.Prepared(meta))
	})
}

func TestReferenceDeferredForeignKeyTriggerTiming(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
		t.Run(action, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p ON UPDATE "+action+" ON DELETE "+action+" INITIALLY DEFERRED)", 0))
				// Trigger names contain opaque OIDs. Slot ownership is independently
				// identified by relation and function, not allocator ordering.
				rows := [][]any{{true, true}, {true, true}}
				if action != "NO ACTION" {
					rows = [][]any{{false, false}, {false, false}}
				}
				run(fixture.Select("SELECT tgdeferrable,tginitdeferred FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='fk') AND tgrelid='p'::regclass ORDER BY tgname", []string{"tgdeferrable", "tginitdeferred"}, []string{"BOOL", "BOOL"}, rows))
				run(fixture.Select("SELECT tgdeferrable,tginitdeferred FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='fk') AND tgrelid='c'::regclass ORDER BY tgname", []string{"tgdeferrable", "tginitdeferred"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}}))
			})
		})
	}
	t.Run("event-time-firing", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
			run(fixture.Exec("ALTER TABLE c DISABLE TRIGGER ALL", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Exec("ALTER TABLE c ENABLE TRIGGER ALL", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(8)", fixture.ForeignKeyChild("c", "fk", `Key (pid)=(8) is not present in table "p".`)))
		})
	})
	t.Run("column-list-action", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
			run(fixture.Exec("CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p ON DELETE SET NULL(b) INITIALLY DEFERRED)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1,2)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(1,2)", 1))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DELETE FROM p", 1))
			run(fixture.Select("SELECT a,b FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}}))
			run(fixture.Exec("COMMIT", 0))
		})
	})
	t.Run("action-generated-deferred-index", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(id INT,pid INT CONSTRAINT fk REFERENCES p ON DELETE SET NULL INITIALLY DEFERRED,CONSTRAINT u UNIQUE NULLS NOT DISTINCT(pid) INITIALLY DEFERRED)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(1,1),(2,NULL)", 2))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DELETE FROM p", 1))
			run(fixture.Select("SELECT id,pid FROM c ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(2), nil}}))
			run(fixture.Exec("SAVEPOINT s", 0))
			run(fixture.ErrorDiagnostic("SET CONSTRAINTS u IMMEDIATE", fixture.DeferredUniqueConflict("c", "u", "pid", "null")))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.Exec("DELETE FROM c WHERE id=2", 1))
			run(fixture.Exec("COMMIT", 0))
		})
	})
}
