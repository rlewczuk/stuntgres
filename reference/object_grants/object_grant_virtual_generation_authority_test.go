package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 ddl.sgml's generated-column privilege contract requires the current
// reader's EXECUTE, not the table/view owner's. rewriteHandler.c expands virtual
// reads to their row-dependent recipes; execExpr.c ExecInitFunc checks GetUserId
// when initializing a surviving call, before any row or strict-NULL evaluation.
// nodeResult.c ExecInitResult and nodeLimit.c ExecInitLimit initialize projections
// and children even when FALSE/LIMIT 0 later prevent tuple production. The exact
// optimizer footprints below are PG-first hypotheses, not observed oracle data.
func TestReferenceObjectGrantVirtualGenerationAuthority(t *testing.T) {
	objectGrantScenario(t, []string{"og_virtual_x"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		const target = "pg_catalog.json_array_length(json)"
		defer objectGrantRoutineCleanup(t, p, c, []string{target})
		run(fixture.Exec("CREATE SCHEMA og_virtual_x AUTHORIZATION og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("CREATE TABLE og_virtual_x.data(id INT,j JSON,note TEXT,h INT GENERATED ALWAYS AS (pg_catalog.json_array_length(j)) VIRTUAL,s INT GENERATED ALWAYS AS (pg_catalog.json_array_length(j)) STORED)", 0))
			run(fixture.Exec("INSERT INTO og_virtual_x.data(id,j,note) VALUES(1,'[1,2]','ordinary'),(2,NULL,'null input')", 2))
			run(fixture.Exec("CREATE TABLE og_virtual_x.empty_data(id INT,j JSON,h INT GENERATED ALWAYS AS (pg_catalog.json_array_length(j)) VIRTUAL)", 0))
			run(fixture.Exec("CREATE VIEW og_virtual_x.owner_view AS SELECT id,h FROM og_virtual_x.data", 0))
		})
		run(fixture.Exec("GRANT SELECT(id,h) ON og_virtual_x.data TO og_object_reader", 0))
		run(fixture.Exec("GRANT SELECT ON og_virtual_x.empty_data,og_virtual_x.owner_view TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT h FROM og_virtual_x.data", "schema", "og_virtual_x", prepared)
		})
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_virtual_x TO og_object_reader", 0))

		integer := func(q string, rows [][]any, args ...any) fixture.Step {
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, rows, args...)
			s.CheckMetadata = fixture.StringEncodingMetadata(b.PG, s.Columns, s.Types)
			return s
		}
		columnInteger := func(q string, rows [][]any, args ...any) fixture.Step {
			s := integer(q, rows, args...)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				t.Helper()
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				// Nullable generated-column projections retain the table/view
				// contract, unlike scalar expressions; pq reports it unknown.
				fixture.TableDDLColumnTypes(t, b.PG, columns, s.Columns, s.Types, []bool{true})
				length, lengthKnown := columns[0].Length()
				precision, scale, decimalKnown := columns[0].DecimalSize()
				if length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown {
					t.Fatalf("INT4 metadata length=%d/%v decimal=%d,%d/%v", length, lengthKnown, precision, scale, decimalKnown)
				}
			}
			return s
		}
		denial := func(s fixture.Step) fixture.Step {
			d := fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length"}, s.Args...)
			d.SelectRows, d.ErrorStage = true, "execute"
			return d
		}
		value := columnInteger("SELECT h AS v FROM og_virtual_x.data WHERE id=$1", [][]any{{int64(2)}}, int64(1))
		view := columnInteger("SELECT h AS v FROM og_virtual_x.owner_view WHERE id=$1", [][]any{{int64(2)}}, int64(1))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			// Selecting the generated column does not require SELECT on its input.
			run(value)
			fixture.RoleGrantPermissionDenied(run, "SELECT j FROM og_virtual_x.data", "table", "data")
			fixture.RoleGrantPermissionDenied(run, "SELECT note FROM og_virtual_x.data", "table", "data")
		})
		run(fixture.Exec("GRANT SELECT(j,note,s) ON og_virtual_x.data TO og_object_reader", 0))
		data := fixture.Select("SELECT j,j::TEXT AS source,note,s FROM og_virtual_x.data WHERE id=1", []string{"j", "source", "note", "s"}, []string{"JSON", "TEXT", "TEXT", "INT4"}, [][]any{{"[1,2]", "[1,2]", "ordinary", int64(2)}})
		stored := columnInteger("SELECT s AS v FROM og_virtual_x.data WHERE id=1", [][]any{{int64(2)}})

		type consumer struct {
			step fixture.Step
			x    bool
			held fixture.HeldStatement
		}
		consumers := []consumer{
			{step: value, x: true},
			{step: view, x: true},
			{step: columnInteger("SELECT h AS v FROM og_virtual_x.data WHERE id=2", [][]any{{nil}}), x: true},
			{step: columnInteger("SELECT h AS v FROM og_virtual_x.data WHERE id>99", nil), x: true},
			{step: columnInteger("SELECT h AS v FROM og_virtual_x.empty_data", nil), x: true},
			{step: columnInteger("SELECT h AS v FROM og_virtual_x.data WHERE FALSE", nil), x: true},
			{step: columnInteger("SELECT h AS v FROM og_virtual_x.data LIMIT 0", nil), x: true},
			{step: integer("SELECT CASE WHEN FALSE THEN h ELSE NULL::INT END AS v FROM og_virtual_x.data WHERE id=1", [][]any{{nil}})},
			{step: columnInteger("SELECT r.h AS v FROM og_virtual_x.data l LEFT JOIN og_virtual_x.data r ON r.id=l.id+100 WHERE l.id=1", [][]any{{nil}}), x: true},
			{step: stored},
			{step: data},
			{step: fixture.Select("SELECT count(*) AS n FROM og_virtual_x.data", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}})},
		}
		// Close before returning the physical connection, including failed steps.
		defer func() {
			for _, tc := range consumers {
				if tc.held.Statement != nil {
					tc.held.Statement.Close()
				}
			}
		}()
		// Prepare as the actual reader, warm every SAME handle with PUBLIC X,
		// and retain those handles through both revocations and the regrant.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for i := range consumers {
				stmt, err := c.PrepareContext(p.Ctx, consumers[i].step.Query)
				if err != nil {
					t.Fatal(err)
				}
				consumers[i].held = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: consumers[i].step.Query}
				fixture.RunStep(t, p.Ctx, consumers[i].held, b.Recorder, consumers[i].step)
				run(consumers[i].step)
			}
		})
		phase := func(authorized bool) {
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				for _, tc := range consumers {
					s := tc.step
					if tc.x && !authorized {
						s = denial(s)
					}
					run(s)
					fixture.RunStep(t, p.Ctx, tc.held, b.Recorder, s)
				}
				// A direct SQL call is an independent function-ACL control.
				s := integer("SELECT pg_catalog.json_array_length(j) AS v FROM og_virtual_x.data WHERE id=1", [][]any{{int64(2)}})
				if !authorized {
					s = denial(s)
				}
				run(s)
			})
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_other", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
		phase(false)
		fixture.RoleGrantActor(run, "og_object_other", func() { run(value); run(stored) })
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
		phase(true)

		// Reverse routine authority: ownership of the table and definer view
		// does not confer ordinary EXECUTE, and cannot replace reader authority.
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { run(denial(value)); run(stored); run(data) })
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(value); run(view) })
		// Reader loses base generated-column SELECT, not X or view SELECT.
		// The owner view still supplies relation authority, never function X.
		run(fixture.Exec("REVOKE SELECT(h) ON og_virtual_x.data FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT h AS v FROM og_virtual_x.data WHERE id=1", "table", "data")
			run(view)
			run(data)
		})
		run(fixture.Exec("GRANT SELECT(h) ON og_virtual_x.data TO og_object_reader", 0))
		// Fresh namespace lookup needs USAGE even while routine X is granted.
		run(fixture.Exec("REVOKE USAGE ON SCHEMA og_virtual_x FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT h FROM og_virtual_x.data", "schema", "og_virtual_x", prepared)
		})
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_virtual_x TO og_object_reader", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
		phase(false)
		run(fixture.Exec("DROP SCHEMA og_virtual_x CASCADE", 0))
	})
}
