package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func earthQueryNullDemandSetup(t *testing.T, p *fixture.Pair, c fixture.Executor) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE earth_query_null_input(id integer,p earth,r float8)",
		"INSERT INTO earth_query_null_input VALUES (1,NULL,'5e-324'::float8),(2,NULL,0),(3,'(6378168)',NULL),(4,NULL,NULL),(5,'(6378168)',0),(6,'(6378168)','5e-324'::float8)",
		"CREATE VIEW earth_query_null_view AS SELECT id,earth_box(p,r) AS b FROM earth_query_null_input WHERE id=1",
		"CREATE VIEW earth_query_null_literal_view AS SELECT id,earth_box(p,'5e-324'::float8) AS b FROM earth_query_null_input WHERE id=1",
		"CREATE VIEW earth_query_null_safe_view AS SELECT id,earth_box(p,r) AS b FROM earth_query_null_input WHERE id>=2 AND id<=5",
		"CREATE TABLE earth_query_null_default(id integer,b cube DEFAULT earth_box(NULL::earth,'5e-324'::float8))",
		"INSERT INTO earth_query_null_default VALUES (1,'(6378168)')",
		"CREATE TABLE earth_query_null_safe_default(id integer,b cube DEFAULT earth_box(NULL::earth,0),c cube DEFAULT earth_box('(6378168)'::earth,NULL::float8))",
		"INSERT INTO earth_query_null_safe_default(id) VALUES (1)",
	} {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func earthQueryNullDemandSafe(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, box string) {
	t.Helper()
	want := [][]any{{int64(2), nil}, {int64(3), nil}, {int64(4), nil}, {int64(5), "(6378168)"}}
	for _, q := range []string{
		"SELECT id," + box + "(p,r) AS b FROM earth_query_null_input WHERE id>=2 AND id<=5 ORDER BY id",
		"SELECT id,b FROM earth_query_null_safe_view ORDER BY id",
	} {
		earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, q, []string{"id", "b"}, []string{"INT4", "CUBE"}, want))
	}
	for _, radius := range []string{"0", "NULL::float8"} {
		q := "SELECT " + box + "(p," + radius + ") AS b FROM earth_query_null_input WHERE id=1"
		earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{nil}}))
	}
	checkBoth := func(s fixture.Step) {
		for _, route := range []string{"direct", "fresh"} {
			t.Run("query="+s.Query+"/"+route, func(t *testing.T) {
				step := s
				if route == "fresh" {
					step = fixture.Prepared(step)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			})
		}
	}
	checkBoth(cubeConstructorSelect(b, "SELECT id,b,c FROM earth_query_null_safe_default ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "CUBE", "CUBE"}, [][]any{{int64(1), nil, nil}}))
	checkBoth(cubeConstructorSelect(b, "INSERT INTO earth_query_null_safe_default(id) VALUES (2) RETURNING b,c", []string{"b", "c"}, []string{"CUBE", "CUBE"}, [][]any{{nil, nil}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM earth_query_null_safe_default WHERE id=2", 2))
	checkBoth(cubeConstructorSelect(b, "SELECT id,b FROM earth_query_null_default ORDER BY id", []string{"id", "b"}, []string{"INT4", "CUBE"}, [][]any{{int64(1), "(6378168)"}}))
}

// tests-10 settled both explicit stable modes, not PostgreSQL's automatic
// cost-based choice. Keep every ordered step and the same twice-held handle.
func earthQueryNullDemandParameters(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, box, helper string, generic bool) {
	t.Helper()
	helperName := helper[strings.LastIndexByte(helper, '.')+1:]
	q := "SELECT " + box + "($1::earth,$2::float8) AS b"
	first := cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{nil}}, nil, "5e-324")
	if generic {
		first = fixture.ErrorDiagnostic(q, earthQueryUnderflow(helperName, "", ""), nil, "5e-324")
		first.SelectRows, first.ErrorStage = true, "execute"
	}
	steps := []fixture.Step{first}
	for _, args := range [][]any{{nil, "0"}, {"(6378168)", nil}, {nil, nil}} {
		steps = append(steps, cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{nil}}, args...))
	}
	steps = append(steps, cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}, "(6378168)", "0"))
	earthQueryRoutes(t, p, b, c, steps...)

	q = "SELECT " + box + "($1::earth,r) AS b FROM earth_query_null_input WHERE id=1"
	column := cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{nil}}, nil)
	if generic {
		column = fixture.ErrorDiagnostic(q, earthQueryUnderflow(helperName, "", ""), nil)
		column.SelectRows, column.ErrorStage = true, "execute"
	}
	// Five identical fixed-column steps twice keep all ten held executions.
	earthQueryRoutes(t, p, b, c, column, column, column, column, column)
}

func earthQueryNullDemandModes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, box, helper string) {
	t.Helper()
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		t.Run(mode, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET plan_cache_mode="+mode, 0))
			earthQueryNullDemandParameters(t, p, b, c, box, helper, mode == "force_generic_plan")
		})
	}
	t.Run("same-held-mode-changes", func(t *testing.T) {
		q := "SELECT " + box + "($1::earth,$2::float8) AS b"
		h := earthHeld(t, p, c, q)
		defer h.Statement.Close()
		helperName := helper[strings.LastIndexByte(helper, '.')+1:]
		for _, mode := range []string{"force_custom_plan", "force_generic_plan", "force_custom_plan"} {
			t.Run(mode, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET plan_cache_mode="+mode, 0))
				s := cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{nil}}, nil, "5e-324")
				if mode == "force_generic_plan" {
					s = fixture.ErrorDiagnostic(q, earthQueryUnderflow(helperName, "", ""), nil, "5e-324")
					s.SelectRows, s.ErrorStage = true, "execute"
				}
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}, "(6378168)", "0"))
			})
		}
	})
}

// Complete diagnostics and stages were settled by tests-7 on pinned 18.6.
func earthQueryNullDemandVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, box, helper string) {
	t.Helper()
	// The row/literal Context matrix was settled in the custom route. The
	// parameter sequences below separately exercise BOTH stable modes.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET plan_cache_mode=force_custom_plan", 0))
	defer c.ExecContext(p.Ctx, "RESET plan_cache_mode")
	boxName := box[strings.LastIndexByte(box, '.')+1:]
	helperName := helper[strings.LastIndexByte(helper, '.')+1:]
	for index, tc := range []struct {
		q, wrapper  string
		args, valid []any
	}{
		{"SELECT " + box + "(p,r) AS b FROM earth_query_null_input WHERE id=$1", "", []any{int64(1)}, []any{int64(2)}},
		{"SELECT " + box + "(p,r) AS b FROM earth_query_null_input WHERE id=$1", "", []any{int64(6)}, []any{int64(2)}},
		{"SELECT " + box + "(p,'5e-324'::float8) AS b FROM earth_query_null_input WHERE id=$1", "during inlining", []any{int64(1)}, nil},
		{"SELECT " + box + "(p,'5e-324'::float8) AS b FROM earth_query_null_input WHERE id=$1", "during inlining", []any{int64(6)}, nil},
		{"SELECT " + box + "(p,$1::float8) AS b FROM earth_query_null_input WHERE id=1", "during inlining", []any{"5e-324"}, []any{"0"}},
		{"SELECT b FROM earth_query_null_view WHERE id=$1", "", []any{int64(1)}, nil},
		{"SELECT b FROM earth_query_null_literal_view WHERE id=$1", "during inlining", []any{int64(1)}, nil},
		{"INSERT INTO earth_query_null_default(id) VALUES (2) RETURNING b", "during inlining", nil, nil},
		{"SELECT " + box + "(NULL::earth,'5e-324'::float8) AS b", "during inlining", nil, nil},
	} {
		for _, route := range []string{"direct", "fresh", "held"} {
			t.Run(fmt.Sprintf("error-%d/query=%s/%s", index+1, tc.q, route), func(t *testing.T) {
				earthCoordinateError(t, p, b, c, tc.q, route, tc.args, earthQueryUnderflow(helperName, boxName, tc.wrapper), false, tc.valid)
			})
		}
	}
	t.Run("safe-neighbors", func(t *testing.T) {
		earthQueryNullDemandSafe(t, p, b, c, box)
	})
	earthQueryNullDemandModes(t, p, b, c, box, helper)
}

func TestReferenceEarthQueryNullDemand(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "earth_query_null_moved")
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthQueryNullDemandSetup(t, p, c)
		t.Run("canonical", func(t *testing.T) {
			earthQueryNullDemandVerify(t, p, b, c, "earth_box", "gc_to_sec")
		})
		for _, q := range []string{
			"CREATE SCHEMA earth_query_null_moved",
			"ALTER FUNCTION earth_box(earth,float8) RENAME TO box_now",
			"ALTER FUNCTION gc_to_sec(float8) RENAME TO chord_now",
			"ALTER FUNCTION box_now(earth,float8) SET SCHEMA earth_query_null_moved",
			"ALTER FUNCTION chord_now(float8) SET SCHEMA earth_query_null_moved",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		box, helper := "earth_query_null_moved.box_now", "earth_query_null_moved.chord_now"
		t.Run("renamed-moved", func(t *testing.T) {
			earthQueryNullDemandVerify(t, p, b, c, box, helper)
		})
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT id,b FROM earth_query_null_safe_view ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata := cubeMetadata(t, false, retained, []string{"id", "b"}, []bool{true, true})
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("NULL-demand same-driver Restore", err)
		}
		t.Run("same-driver-restore", func(t *testing.T) {
			earthQueryNullDemandVerify(t, p, b, c, box, helper)
		})
		cubeConsumeRetained(t, retained, [][]any{{int64(2), nil}, {int64(3), nil}, {int64(4), nil}, {int64(5), "(6378168)"}})
		cubeColumnMetadata(t, false, metadata, []string{"id", "b"}, []bool{true, true})
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE earth_query_null_allocator(v integer)", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("NULL-demand fresh-driver Restore", err)
		}
		tc, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tc.Close()
		tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
		t.Run("fresh-driver-restore", func(t *testing.T) {
			earthQueryNullDemandVerify(t, target, tb, tc, box, helper)
		})
		held, err := tc.QueryContext(target.Ctx, "SELECT id,b FROM earth_query_null_safe_view ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		bad := dump.String() + "\nINSERT INTO earth_query_null_default(id) VALUES (99);\n"
		want := earthQueryUnderflow("chord_now", "box_now", "during inlining")
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
		t.Run("failed-replay-target", func(t *testing.T) {
			earthQueryNullDemandVerify(t, target, tb, tc, box, helper)
		})
		cubeConsumeRetained(t, held, [][]any{{int64(2), nil}, {int64(3), nil}, {int64(4), nil}, {int64(5), "(6378168)"}})
		var after bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), after.Bytes()) {
			t.Fatal("failed NULL-demand replay changed catalog/data/defaults", after.String())
		}
		if p.Postgres != nil {
			reset := "DROP EXTENSION IF EXISTS earthdistance CASCADE; DROP EXTENSION IF EXISTS cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP SCHEMA IF EXISTS earth_query_null_moved CASCADE"
			if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("NULL-demand actual emitted SQL replay", err)
			}
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			pc, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("postgres-emitted-sql-replay", func(t *testing.T) {
				earthQueryNullDemandVerify(t, p, pg, pc, box, helper)
			})
			pc.Close()
			if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
				t.Fatal(err)
			}
		}
	})
}
