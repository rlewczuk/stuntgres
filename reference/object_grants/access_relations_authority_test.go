package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func accessRelationsRoutineCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn, target string) {
	t.Helper()
	for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION " + target + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + target + " FROM ar_reader CASCADE"} {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Error("restore selected routine ACL", err)
		}
	}
}

func TestReferenceAccessRelationsRoutineAuthority(t *testing.T) {
	inventory := accessRelationsSignatures()
	for i, sig := range inventory {
		t.Run(sig.target(), func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				sibling := inventory[i^1]
				args := accessRelationsArgs(t, p, c, sig)
				other := accessRelationsArgs(t, p, c, sibling)
				for _, q := range []string{"GRANT SELECT ON ar_t,ar_s TO ar_reader"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("CREATE TABLE ar_sink(v BOOL)", 0))
				run(fixture.Exec("GRANT INSERT,SELECT ON ar_sink TO ar_reader", 0))
				defer accessRelationsRoutineCleanup(t, p, c, sig.target())
				held := accessExecutionHeld(t, p, c, sig.query())
				phase := func(denied bool) {
					fixture.RoleGrantActor(run, "ar_reader", func() {
						for _, token := range []string{"SELECT", "UPDATE"} {
							a := append([]any(nil), args...)
							a[len(a)-1] = token
							s := objectGrantAccessValue(sig.query(), token == "SELECT", a...)
							if denied {
								s = objectGrantAccessDenied(sig.query(), sig.name, true, a...)
							}
							accessRelationsRoutes(t, p, b, c, s)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							if denied {
								e := objectGrantAccessDenied(sig.query(), sig.name, false, a...)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, e)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(e))
							}
						}
						if denied {
							for _, prepared := range []bool{false, true} {
								s := objectGrantAccessDenied("INSERT INTO ar_sink "+sig.query(), sig.name, false, args...)
								s.Prepared = prepared
								run(s)
							}
							run(fixture.Select("SELECT count(*) AS n FROM ar_sink", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
						accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(sibling.query(), true, other...))
					})
				}
				phase(false)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig.target()+" FROM PUBLIC", 0))
				phase(true)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+sig.target()+" TO ar_reader", 0))
				phase(false)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig.target()+" FROM ar_reader", 0))
				phase(true)
			})
		})
	}
}
func TestReferenceAccessRelationsDemand(t *testing.T) {
	for _, sig := range accessRelationsSignatures() {
		t.Run(sig.target(), func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				args := accessRelationsArgs(t, p, c, sig)
				var definitions, parameters, columns, nulls []string
				for i, typ := range sig.types {
					col := fmt.Sprintf("v%d", i+1)
					definitions = append(definitions, col+" "+typ)
					parameters = append(parameters, fmt.Sprintf("$%d::%s", i+1, typ))
					columns = append(columns, col)
					nulls = append(nulls, "NULL")
				}
				run(fixture.Exec("CREATE TABLE ar_inputs(id INT,"+strings.Join(definitions, ",")+")", 0))
				run(fixture.Exec("INSERT INTO ar_inputs VALUES(1,"+strings.Join(parameters, ",")+"),(2,"+strings.Join(nulls, ",")+")", 2, args...))
				run(fixture.Exec("GRANT SELECT ON ar_t,ar_s,ar_inputs TO ar_reader", 0))
				runtime := sig.name + "(" + strings.Join(columns, ",") + ")"
				var held []fixture.HeldStatement
				for _, predicate := range []string{"id=1", "id=2", "id=99", "FALSE"} {
					held = append(held, accessExecutionHeld(t, p, c, "SELECT "+runtime+" AS v FROM ar_inputs WHERE "+predicate))
				}
				defer accessRelationsRoutineCleanup(t, p, c, sig.target())
				phase := func(denied bool) {
					fixture.RoleGrantActor(run, "ar_reader", func() {
						wants := [][][]any{{{true}}, {{nil}}, nil, nil}
						for i, h := range held {
							s := fixture.Select(h.SQL, []string{"v"}, []string{"BOOL"}, wants[i])
							if denied {
								s = objectGrantAccessDenied(h.SQL, sig.name, true)
							}
							fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
							accessRelationsRoutes(t, p, b, c, s)
						}
						literal := append([]string(nil), parameters...)
						literal[0] = "NULL::" + sig.types[0]
						// Replace every other formal with a constant, leaving no unused parameters.
						for i := 1; i < len(literal); i++ {
							literal[i] = "NULL::" + sig.types[i]
						}
						run(objectGrantAccessValue("SELECT "+sig.name+"("+strings.Join(literal, ",")+") AS v", nil))
						a := append([]any(nil), args...)
						a[0] = nil
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(objectGrantAccessValue(sig.query(), nil, a...)))
						run(objectGrantAccessValue("SELECT CASE WHEN FALSE THEN "+runtime+" ELSE FALSE END AS v FROM ar_inputs WHERE id=1", false))
					})
				}
				demanded := append([]string(nil), columns...)
				demanded[len(demanded)-1] = "(1/0)::text"
				childQuery := "SELECT " + sig.name + "(" + strings.Join(demanded, ",") + ") AS v FROM ar_inputs WHERE id=2"
				for _, prepared := range []bool{false, true} {
					s := accessRelationsError(childQuery, "22012", "division by zero")
					s.Prepared = prepared
					run(s)
				}
				phase(false)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig.target()+" FROM PUBLIC", 0))
				phase(true)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+sig.target()+" TO ar_reader", 0))
				phase(false)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig.target()+" FROM ar_reader", 0))
				phase(true)
			})
		})
	}
}
func TestReferenceAccessRelationsLifecycle(t *testing.T) {
	for _, phase := range []string{"transaction", "role", "rename", "view", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				q := "SELECT has_column_privilege('ar_reader','ar_t','a','SELECT') AS v"
				held := accessExecutionHeld(t, p, c, q)
				check := func(want bool) { fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(q, want)) }
				check(false)
				switch phase {
				case "transaction":
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("GRANT SELECT(a) ON ar_t TO ar_reader", 0))
					check(true)
					run(fixture.Exec("SAVEPOINT ar_save", 0))
					run(fixture.Exec("REVOKE SELECT(a) ON ar_t FROM ar_reader", 0))
					check(false)
					run(fixture.Exec("ROLLBACK TO SAVEPOINT ar_save", 0))
					check(true)
					run(fixture.Exec("ROLLBACK", 0))
					check(false)
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("GRANT SELECT(a) ON ar_t TO ar_reader", 0))
					run(fixture.Exec("COMMIT", 0))
					check(true)
				case "role":
					implicit := accessExecutionHeld(t, p, c, "SELECT has_column_privilege('ar_t','a','SELECT') AS v")
					run(fixture.Exec("GRANT SELECT(a) ON ar_t TO ar_group", 0))
					run(fixture.Exec("GRANT ar_group TO ar_reader", 0))
					check(true)
					fixture.RoleGrantActor(run, "ar_reader", func() { fixture.RunStep(t, p.Ctx, implicit, b.Recorder, objectGrantAccessValue(implicit.SQL, true)) })
					run(fixture.Exec("REVOKE ar_group FROM ar_reader", 0))
					check(false)
					var oldRole int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='ar_reader'").Scan(&oldRole); err != nil {
						t.Fatal(err)
					}
					numeric := accessExecutionHeld(t, p, c, "SELECT has_column_privilege($1::oid,'ar_t','a','SELECT') AS v")
					run(fixture.Exec("DROP ROLE ar_reader", 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, accessRelationsError(q, "42704", `role "ar_reader" does not exist`))
					run(fixture.Exec("CREATE ROLE ar_reader", 0))
					run(fixture.Exec("GRANT SELECT(a) ON ar_t TO ar_reader", 0))
					check(true)
					fixture.RunStep(t, p.Ctx, numeric, b.Recorder, objectGrantAccessValue(numeric.SQL, false, oldRole))
				case "rename":
					run(fixture.Exec("GRANT SELECT(a) ON ar_t TO ar_reader", 0))
					check(true)
					run(fixture.Exec("ALTER TABLE ar_t RENAME COLUMN a TO aa", 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, accessRelationsError(q, "42703", `column "a" of relation "ar_t" does not exist`))
				case "view":
					run(fixture.Exec("CREATE VIEW ar_view AS SELECT has_table_privilege('ar_t','SELECT') AS v", 0))
					run(fixture.Exec("ALTER VIEW ar_view OWNER TO ar_owner", 0))
					run(fixture.Exec("GRANT SELECT ON ar_view TO ar_reader", 0))
					fixture.RoleGrantActor(run, "ar_reader", func() { run(objectGrantAccessValue("SELECT v FROM ar_view", false)) })
					run(fixture.Exec("GRANT SELECT ON ar_t TO ar_reader", 0))
					fixture.RoleGrantActor(run, "ar_reader", func() { run(objectGrantAccessValue("SELECT v FROM ar_view", true)) })
					run(fixture.Exec("CREATE TABLE ar_out(v BOOL)", 0))
					run(fixture.Exec("INSERT INTO ar_out SELECT has_column_privilege('ar_reader','ar_t','a','SELECT')", 1))
					run(objectGrantAccessValue("SELECT v FROM ar_out", true))
				case "cancel":
					if !b.PG {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						rows, err := held.Statement.QueryContext(ctx)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled cursor")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					}
					check(false)
				}
			})
		})
	}
}
