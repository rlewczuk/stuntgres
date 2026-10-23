package reference_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Match the existing circle fixtures: ordinary circle output exposes all three
// fields. Do not add an unrelated circle::text cast or use circle input, which
// rejects a radius that the constructor and binary representation can retain.
func castNegativeCircleStep(backend fixture.Backend, q string, names, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, rows, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "CIRCLE" {
			return false
		}
		if value == nil {
			return true
		}
		if backend.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("circle PostgreSQL raw %T, want []byte", value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("circle native raw %T, want string", value)
		}
		return true
	}
	return s
}

func TestReferenceCastDefinitionNegativeCircleBinary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			castDefinitionCleanup(t, p, backend, [2]string{"line", "circle"})
			run(fixture.Exec("CREATE CAST (line AS circle) WITHOUT FUNCTION", 0))
			q := "SELECT ($1::line)::circle AS v,radius(($1::line)::circle) AS r"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, tc := range []struct{ in, out, radius any }{
				{"{1,2,-3}", "<(1,2),-3>", float64(-3)},
				{"{1,2,3}", "<(1,2),3>", float64(3)},
				{nil, nil, nil},
			} {
				s := castNegativeCircleStep(backend, q, []string{"v", "r"}, []string{"CIRCLE", "FLOAT8"}, [][]any{{tc.out, tc.radius}}, tc.in)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			}
			// A typed literal independently exercises the exact review witness.
			run(castNegativeCircleStep(backend, "SELECT ('{1,2,-3}'::line)::circle AS v", []string{"v"}, []string{"CIRCLE"}, [][]any{{"<(1,2),-3>"}}))
			for _, ddl := range []string{
				"CREATE TYPE negative_circle_payload AS (c circle)",
				"CREATE TABLE negative_circle_saved(id integer,v circle,a negative_circle_payload[],b negative_circle_payload[])",
				"CREATE VIEW negative_circle_retained AS SELECT ('{1,2,-3}'::line)::circle AS v",
				"CREATE TABLE negative_circle_default(v circle DEFAULT (('{1,2,-3}'::line)::circle))",
			} {
				run(fixture.Exec(ddl, 0))
			}
			run(fixture.Exec("INSERT INTO negative_circle_saved VALUES (1,('{1,2,-3}'::line)::circle,array_cat(array_fill(ROW(('{1,2,-3}'::line)::circle)::negative_circle_payload,ARRAY[1],ARRAY[0]),array_fill(ROW(NULL::circle)::negative_circle_payload,ARRAY[1])),array_fill(ROW(('{1,2,-3}'::line)::circle)::negative_circle_payload,ARRAY[2,2],ARRAY[-2,3]))", 1))
			run(fixture.Exec("DROP CAST (line AS circle)", 0))
			checks := func(b fixture.Backend) []fixture.Step {
				return []fixture.Step{
					castNegativeCircleStep(b, "SELECT v,radius(v) AS r FROM negative_circle_retained", []string{"v", "r"}, []string{"CIRCLE", "FLOAT8"}, [][]any{{"<(1,2),-3>", float64(-3)}}),
					castNegativeCircleStep(b, "SELECT v,radius(v) AS r,array_lower(a,1) AS lo,array_upper(a,1) AS hi,(a[0]).c AS leaf,radius((a[0]).c) AS leaf_radius,(a[1]).c AS null_leaf FROM negative_circle_saved ORDER BY id", []string{"v", "r", "lo", "hi", "leaf", "leaf_radius", "null_leaf"}, []string{"CIRCLE", "FLOAT8", "INT4", "INT4", "CIRCLE", "FLOAT8", "CIRCLE"}, [][]any{{"<(1,2),-3>", float64(-3), int64(0), int64(1), "<(1,2),-3>", float64(-3), nil}}),
					castNegativeCircleStep(b, "SELECT array_lower(b,1) AS lo1,array_upper(b,1) AS hi1,array_lower(b,2) AS lo2,array_upper(b,2) AS hi2,(b[-2][3]).c AS first,radius((b[-1][4]).c) AS last_radius FROM negative_circle_saved", []string{"lo1", "hi1", "lo2", "hi2", "first", "last_radius"}, []string{"INT4", "INT4", "INT4", "INT4", "CIRCLE", "FLOAT8"}, [][]any{{int64(-2), int64(-1), int64(3), int64(4), "<(1,2),-3>", float64(-3)}}),
					fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='line'::regtype AND casttarget='circle'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
				}
			}
			for _, s := range checks(backend) {
				run(s)
				run(fixture.Prepared(s))
			}
			// Close the originating held object before replacing the catalog.
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatalf("negative circle Restore: %v\n%s", err, dump.String())
				}
				for _, s := range checks(backend) {
					run(fixture.Prepared(s))
				}
				if p.Postgres != nil {
					pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						if _, err := p.Postgres.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Error("negative circle PostgreSQL replay cleanup:", err)
						}
					}()
					castDefinitionCleanup(t, p, pg, [2]string{"line", "circle"})
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatalf("actual negative circle SQL PostgreSQL replay: %v\n%s", err, dump.String())
					}
					for _, s := range checks(pg) {
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
					}
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO negative_circle_default DEFAULT VALUES", 1))
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, castNegativeCircleStep(pg, "SELECT v,radius(v) AS r FROM negative_circle_default", []string{"v", "r"}, []string{"CIRCLE", "FLOAT8"}, [][]any{{"<(1,2),-3>", float64(-3)}}))
				}
			}
			run(fixture.Exec("INSERT INTO negative_circle_default DEFAULT VALUES", 1))
			run(castNegativeCircleStep(backend, "SELECT v,radius(v) AS r FROM negative_circle_default", []string{"v", "r"}, []string{"CIRCLE", "FLOAT8"}, [][]any{{"<(1,2),-3>", float64(-3)}}))
		})
	}
}
