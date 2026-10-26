package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func strictNullHold(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
}

func TestReferenceObjectGrantStrictNullHeld(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{strictNullNextval, strictNullSample, "pg_catalog.currval(regclass)", "pg_catalog.lastval()"})
		strictNullSetup(run)
		for _, name := range []string{"og_strict_held_literal", "og_strict_held_parameter", "og_strict_fresh_parameter", "og_strict_held_volatile", "og_strict_fresh_volatile"} {
			strictNullSequence(run, name)
		}
		literal := "SELECT array_sample(NULL::integer[],nextval('og_strict_held_literal')::integer) AS v"
		parameter := "SELECT array_sample($1::integer[],nextval('og_strict_held_parameter')::integer) AS v"
		freshParameter := "SELECT array_sample($1::integer[],nextval('og_strict_fresh_parameter')::integer) AS v"
		heldVolatile := "SELECT array_sample(ARRAY[7],nextval('og_strict_held_volatile')::integer) AS v"
		freshVolatile := "SELECT array_sample(ARRAY[7],nextval('og_strict_fresh_volatile')::integer) AS v"
		arithmetic := "SELECT array_sample(NULL::integer[],1/0) AS v"
		input := "SELECT array_sample(NULL::integer[],$1::integer) AS v"
		held := make(map[string]fixture.HeldStatement)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			if prepared {
				for _, q := range []string{literal, parameter, arithmetic, input, heldVolatile} {
					held[q] = strictNullHold(t, p, c, q)
				}
			}
		})
		defer func() {
			for _, h := range held {
				h.Statement.Close()
			}
		}()
		executeHeld := func(s fixture.Step) {
			t.Helper()
			if h, ok := held[s.Query]; ok {
				objectGrantStep(t, p.Ctx, h, b.Recorder, s)
			}
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			// Exactly three initial executions: do not silently assume a later
			// generic PostgreSQL plan specializes every NULL parameter frame.
			snapshots := make(map[string]strictNullSequenceState)
			for i, arg := range []any{nil, "{7}", nil} {
				var value any
				if arg != nil {
					value = "{7}"
				}
				run(strictNullValue(freshParameter, value, arg))
				executeHeld(strictNullValue(parameter, value, arg))
				for _, name := range []string{"og_strict_fresh_parameter", "og_strict_held_parameter"} {
					if name == "og_strict_held_parameter" && !prepared {
						continue
					}
					called, last := i != 0, int64(1)
					if !called {
						last = 41
					}
					if i == 2 {
						strictNullUnchanged(run, name, snapshots[name])
					}
					snapshots[name] = strictNullState(t, p, b, c, run, name, 1, called)
					strictNullSession(run, name, called, last)
				}
			}
			run(strictNullValue(literal, nil))
			executeHeld(strictNullValue(literal, nil))
			run(strictNullValue(freshVolatile, "{7}"))
			executeHeld(strictNullValue(heldVolatile, "{7}"))
			for _, name := range []string{"og_strict_fresh_volatile", "og_strict_held_volatile"} {
				if name == "og_strict_held_volatile" && !prepared {
					continue
				}
				strictNullState(t, p, b, c, run, name, 1, true)
				strictNullSession(run, name, true, 1)
			}
		})
		for _, target := range []string{strictNullNextval, strictNullSample} {
			for _, allowed := range []bool{false, true, false} {
				if allowed {
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				} else {
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC,og_object_reader", 0))
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(strictNullValue(literal, nil))
					executeHeld(strictNullValue(literal, nil))
					strictNullState(t, p, b, c, run, "og_strict_held_literal", 1, false)
					strictNullSession(run, "og_strict_held_literal", false, 1)
					run(strictNullFailure(arithmetic, "22012", "division by zero"))
					executeHeld(strictNullFailure(arithmetic, "22012", "division by zero"))
					bad := fixture.ImplicitError(input, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
					bad.SelectRows, bad.ErrorStage = true, "execute"
					run(bad)
					executeHeld(bad)
					run(strictNullValue(input, nil, int64(1)))
					executeHeld(strictNullValue(input, nil, int64(1)))
				})
				// Reset generator state before each positive singleton execution.
				// This does not replace or re-Prepare the original held handle.
				for _, pair := range []struct {
					q, name string
					isHeld  bool
				}{
					{freshParameter, "og_strict_fresh_parameter", false},
					{parameter, "og_strict_held_parameter", true},
					{freshVolatile, "og_strict_fresh_volatile", false},
					{heldVolatile, "og_strict_held_volatile", true},
				} {
					if pair.isHeld && !prepared {
						continue
					}
					run(fixture.Exec("ALTER SEQUENCE "+pair.name+" RESTART WITH 1", 0))
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						before := strictNullState(t, p, b, c, run, pair.name, 1, false)
						var args []any
						if pair.q == freshParameter || pair.q == parameter {
							args = []any{"{7}"}
						}
						s := strictNullValue(pair.q, "{7}", args...)
						if !allowed {
							fn := "nextval"
							if target == strictNullSample {
								fn = "array_sample"
							}
							s = strictNullFailure(pair.q, "42501", "permission denied for function "+fn, args...)
						}
						if pair.isHeld {
							executeHeld(s)
						} else {
							run(s)
						}
						if allowed {
							strictNullState(t, p, b, c, run, pair.name, 1, true)
						} else {
							strictNullUnchanged(run, pair.name, before)
						}
						strictNullSession(run, pair.name, true, 1)
					})
				}
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
		}
	})
}

func TestReferenceObjectGrantStrictNullStored(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{strictNullNextval, "pg_catalog.currval(regclass)", "pg_catalog.lastval()"})
		strictNullSetup(run)
		for _, name := range []string{"og_strict_view_effect", "og_strict_default_effect", "og_strict_row_effect"} {
			strictNullSequence(run, name)
		}
		run(fixture.Exec("CREATE TABLE og_strict_source(id INT,a INT[])", 0))
		run(fixture.Exec("INSERT INTO og_strict_source VALUES(1,NULL),(2,NULL)", 2))
		run(fixture.Exec("CREATE VIEW og_strict_literal_view AS SELECT array_sample(NULL::integer[],nextval('og_strict_view_effect')::integer) AS v", 0))
		run(fixture.Exec("CREATE VIEW og_strict_row_view AS SELECT id,array_sample(a,nextval('og_strict_row_effect')::integer) AS v FROM og_strict_source", 0))
		run(fixture.Exec("CREATE TABLE og_strict_defaults(id INT,v INT[] DEFAULT array_sample(NULL::integer[],nextval('og_strict_default_effect')::integer))", 0))
		run(fixture.Exec("CREATE TABLE og_strict_publication(id INT,v INT[])", 0))
		run(fixture.Exec("INSERT INTO og_strict_publication VALUES(99,'{9}')", 1))
		// The owner is distinct from the invoking reader; routine authority must
		// not accidentally become the view owner's authority.
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_other", 0))
		run(fixture.Exec("GRANT SELECT ON og_strict_source TO og_object_other", 0))
		run(fixture.Exec("ALTER VIEW og_strict_literal_view OWNER TO og_object_other", 0))
		run(fixture.Exec("ALTER VIEW og_strict_row_view OWNER TO og_object_other", 0))
		run(fixture.Exec("ALTER TABLE og_strict_defaults OWNER TO og_object_other", 0))
		run(fixture.Exec("GRANT SELECT ON og_strict_literal_view,og_strict_row_view,og_strict_defaults,og_strict_publication TO og_object_reader", 0))
		run(fixture.Exec("GRANT INSERT ON og_strict_defaults,og_strict_publication TO og_object_reader", 0))
		literal := "SELECT v FROM og_strict_literal_view"
		row := "SELECT v FROM og_strict_row_view ORDER BY id"
		insert := "INSERT INTO og_strict_defaults(id) VALUES($1::integer)"
		write := "INSERT INTO og_strict_publication SELECT id,v FROM og_strict_row_view ORDER BY id"
		held := make(map[string]fixture.HeldStatement)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			if prepared {
				for _, q := range []string{literal, row, insert, write} {
					held[q] = strictNullHold(t, p, c, q)
				}
			}
		})
		defer func() {
			for _, h := range held {
				h.Statement.Close()
			}
		}()
		execute := func(s fixture.Step) {
			t.Helper()
			run(s)
			if h, ok := held[s.Query]; ok {
				objectGrantStep(t, p.Ctx, h, b.Recorder, s)
			}
		}
		inserted := int64(0)
		consumed := int64(0)
		published := int64(1)
		var defaultRows [][]any
		for phase, allowed := range []bool{false, true, false} {
			if allowed {
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+strictNullNextval+" TO og_object_reader", 0))
			} else {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+strictNullNextval+" FROM PUBLIC,og_object_reader", 0))
			}
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				viewBefore := strictNullState(t, p, b, c, run, "og_strict_view_effect", 1, false)
				defaultBefore := strictNullState(t, p, b, c, run, "og_strict_default_effect", 1, false)
				rowLast := consumed
				if rowLast == 0 {
					rowLast = 1
				}
				rowBefore := strictNullState(t, p, b, c, run, "og_strict_row_effect", rowLast, consumed != 0)
				execute(strictNullValue(literal, nil))
				execute(fixture.Exec(insert, 1, int64(phase)))
				inserted++
				defaultRows = append(defaultRows, []any{int64(phase), nil})
				if prepared {
					inserted++
					defaultRows = append(defaultRows, []any{int64(phase), nil})
				}
				run(fixture.Select("SELECT count(*) AS n FROM og_strict_defaults WHERE v IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{inserted}}))
				run(fixture.Select("SELECT id,v FROM og_strict_defaults ORDER BY id", []string{"id", "v"}, []string{"INT4", "_INT4"}, defaultRows))
				strictNullUnchanged(run, "og_strict_view_effect", viewBefore)
				strictNullUnchanged(run, "og_strict_default_effect", defaultBefore)
				if allowed {
					execute(fixture.Select(row, []string{"v"}, []string{"_INT4"}, [][]any{{nil}, {nil}}))
					consumed += 2
					if prepared {
						consumed += 2
					}
					execute(fixture.Exec(write, 2))
					consumed += 2
					published += 2
					if prepared {
						consumed += 2
						published += 2
					}
					strictNullState(t, p, b, c, run, "og_strict_row_effect", consumed, true)
				} else {
					execute(strictNullFailure(row, "42501", "permission denied for function nextval"))
					s := strictNullFailure(write, "42501", "permission denied for function nextval")
					s.SelectRows = false
					execute(s)
					strictNullUnchanged(run, "og_strict_row_effect", rowBefore)
				}
				run(fixture.Select("SELECT count(*) AS n FROM og_strict_publication", []string{"n"}, []string{"INT8"}, [][]any{{published}}))
				publicationRows := [][]any{{int64(99), "{9}"}}
				if published > 1 {
					publicationRows = [][]any{{int64(1), nil}, {int64(2), nil}, {int64(99), "{9}"}}
					if prepared {
						publicationRows = [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), nil}, {int64(2), nil}, {int64(99), "{9}"}}
					}
				}
				run(fixture.Select("SELECT id,v FROM og_strict_publication ORDER BY id", []string{"id", "v"}, []string{"INT4", "_INT4"}, publicationRows))
				last := consumed
				if last == 0 {
					last = 41
				}
				strictNullSession(run, "og_strict_view_effect", false, last)
				strictNullSession(run, "og_strict_default_effect", false, last)
				strictNullSession(run, "og_strict_row_effect", consumed != 0, last)
			})
		}
	})
}
