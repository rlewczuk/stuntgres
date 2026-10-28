package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredForeignKeyAlter(t *testing.T) {
	for _, tc := range []struct {
		clause               string
		deferrable, deferred bool
	}{
		{"DEFERRABLE", true, false}, {"INITIALLY DEFERRED", true, true}, {"INITIALLY IMMEDIATE", false, false}, {"NOT DEFERRABLE", false, false}, {"DEFERRABLE INITIALLY DEFERRED", true, true},
	} {
		t.Run("defaults/"+tc.clause, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
				var oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='fk'").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				q := "SELECT oid::bigint,tgdeferrable,tginitdeferred,tgenabled FROM pg_trigger WHERE tgconstraint=$1::oid ORDER BY oid"
				rows, err := c.QueryContext(p.Ctx, q, oid)
				if err != nil {
					t.Fatal(err)
				}
				var triggerIDs []int64
				for rows.Next() {
					var id int64
					var d, i bool
					var enabled string
					if err := rows.Scan(&id, &d, &i, &enabled); err != nil {
						t.Fatal(err)
					}
					triggerIDs = append(triggerIDs, id)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.Prepared(fixture.Exec("ALTER TABLE c ALTER CONSTRAINT fk "+tc.clause, 0)))
				run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conname='fk'", []any{"fk", tc.deferrable, tc.deferred}))
				want := make([][]any, len(triggerIDs))
				for i, id := range triggerIDs {
					want[i] = []any{id, tc.deferrable, tc.deferred, "O"}
				}
				run(fixture.Select(q, []string{"oid", "tgdeferrable", "tginitdeferred", "tgenabled"}, []string{"INT8", "BOOL", "BOOL", "CHAR"}, want, oid))
				run(fixture.Select("SELECT oid::bigint FROM pg_constraint WHERE conname='fk'", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conname='fk'", []any{"fk", true, true}))
				run(fixture.Exec("COMMIT", 0))
			})
		})
	}
	for _, valid := range []bool{false, true} {
		for _, clause := range []string{"DEFERRABLE INITIALLY DEFERRED", "NOT DEFERRABLE"} {
			t.Run(fmt.Sprintf("pending-child/%t/%s", valid, clause), func(t *testing.T) {
				fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
					if valid {
						run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
					run(fixture.Exec("SAVEPOINT s", 0))
					run(fixture.ErrorDiagnostic("ALTER TABLE c ALTER CONSTRAINT fk "+clause, fixture.ControlsReferenceDiagnostic("55006", `cannot ALTER TABLE "c" because it has pending trigger events`)))
					run(fixture.Exec("ROLLBACK TO s", 0))
					if !valid {
						run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					}
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					run(fixture.Exec("ALTER TABLE c ALTER CONSTRAINT fk "+clause, 0))
					run(fixture.Exec("COMMIT", 0))
				})
			})
		}
	}
	for _, override := range []string{"", "SET CONSTRAINTS fk DEFERRED", "SET CONSTRAINTS ALL IMMEDIATE"} {
		t.Run("override/"+override, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p DEFERRABLE)", 0))
				run(fixture.Exec("BEGIN", 0))
				if override != "" {
					run(fixture.Exec(override, 0))
				}
				run(fixture.Exec("ALTER TABLE c ALTER CONSTRAINT fk INITIALLY DEFERRED", 0))
				if override == "SET CONSTRAINTS ALL IMMEDIATE" {
					run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(9)", fixture.ForeignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`)))
					run(fixture.Exec("ROLLBACK", 0))
				} else {
					run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
					run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
					run(fixture.Exec("COMMIT", 0))
				}
			})
		})
	}
	t.Run("pending-parent", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DELETE FROM p", 1))
			// Event belongs to referenced p, not the altered child relation c.
			run(fixture.Exec("ALTER TABLE c ALTER CONSTRAINT fk NOT DEFERRABLE", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
			run(fixture.Exec("COMMIT", 0))
		})
	})
	t.Run("wrong-kind-and-missing", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v))", 0))
			run(fixture.ErrorDiagnostic("ALTER TABLE t ALTER CONSTRAINT u DEFERRABLE", fixture.ControlsReferenceDiagnostic("42809", `constraint "u" of relation "t" is not a foreign key constraint`)))
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ALTER CONSTRAINT missing DEFERRABLE", fixture.ControlsReferenceDiagnostic("42704", `constraint "missing" of relation "t" does not exist`))))
		})
	})
	t.Run("mixed-enforcement-atomicity", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p DEFERRABLE)", 0))
			run(fixture.Exec("ALTER TABLE c ALTER CONSTRAINT fk NOT ENFORCED, ALTER CONSTRAINT fk INITIALLY DEFERRED", 0))
			flags := fixture.Select("SELECT condeferrable,condeferred,conenforced,convalidated FROM pg_constraint WHERE conname='fk'", []string{"condeferrable", "condeferred", "conenforced", "convalidated"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false, false}})
			run(flags)
			run(fixture.ErrorDiagnostic("ALTER TABLE c ALTER CONSTRAINT fk NOT DEFERRABLE, ALTER CONSTRAINT missing ENFORCED", fixture.ControlsReferenceDiagnostic("42704", `constraint "missing" of relation "c" does not exist`)))
			run(flags)
			run(fixture.Exec("ALTER TABLE c ALTER CONSTRAINT fk ENFORCED, ALTER CONSTRAINT fk DEFERRABLE INITIALLY IMMEDIATE", 0))
			run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conname='fk'", []any{"fk", true, false}))
		})
	})
}
