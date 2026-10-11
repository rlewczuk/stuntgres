package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGroupingSetsLifecycle(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		groupingSetsLifecycle(t, p, b, c, run)
	})
}

func groupingSetsLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE VIEW gs_view AS SELECT a,b,GROUPING(a,b) AS g,count(*) AS n,sum(v) AS s FROM gs GROUP BY ROLLUP(a,b)", 0))
	run(fixture.Exec("ALTER TABLE gs RENAME TO gs_renamed", 0))
	run(fixture.Exec("ALTER TABLE gs_renamed RENAME COLUMN a TO key", 0))
	q := "SELECT a,b,g AS grouping,n AS count,s AS sum FROM gs_view"
	s := fixture.GroupingSetsLevel(q, false)
	fixture.GroupingSetsBoth(run, s)
	var definition string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('gs_view'::regclass,true)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("CREATE VIEW gs_replay AS "+definition, 0))
	replay := fixture.GroupingSetsLevel("SELECT a,b,g AS grouping,n AS count,s AS sum FROM gs_replay", false)
	fixture.GroupingSetsBoth(run, replay)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	var dump bytes.Buffer
	if !b.PG {
		old, err := c.QueryContext(p.Ctx, "SELECT a,b,g,n,s FROM gs_view ORDER BY g,a NULLS FIRST,b NULLS FIRST")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		before, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(replay)
		}
		after, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i := range before {
			if before[i].Name() != after[i].Name() || before[i].DatabaseTypeName() != after[i].DatabaseTypeName() {
				t.Fatal("retained schema changed")
			}
		}
		golden := fixture.GroupingSetsRollupRows()
		order := []int{2, 0, 1, 4, 3, 5}
		for _, i := range order {
			if !old.Next() {
				t.Fatalf("saved grouping row missing: %v", old.Err())
			}
			values := make([]any, 5)
			if err := old.Scan(&values[0], &values[1], &values[2], &values[3], &values[4]); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(values, golden[i]) {
				t.Fatalf("saved row %#v want %#v", values, golden[i])
			}
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("saved cursor remainder", old.Err())
		}
		old.Close()
	}
	run(fixture.Exec("INSERT INTO gs_renamed VALUES(1,10,7)", 1))
	changed := s
	changed.Rows = fixture.GroupingSetsRollupRows()
	changed.Rows[0] = []any{int64(1), int64(10), int64(0), int64(2), int64(9)}
	changed.Rows[3] = []any{int64(1), nil, int64(1), int64(3), int64(12)}
	changed.Rows[5] = []any{nil, nil, int64(3), int64(4), int64(17)}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, changed)
	if !b.PG {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore: %v", err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE partial(v INT); SELECT FROM")); err == nil {
			t.Fatal("malformed restore succeeded")
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, changed)
		foreign := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(foreign.Ctx, foreign.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, s)
		fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, replay)
	}
}

func TestReferenceGroupingSetsTypedStoredDefinitions(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE gs_color AS ENUM('red','blue')", 0))
		run(fixture.Exec("CREATE DOMAIN gs_domain AS INT", 0))
		run(fixture.Exec("CREATE TABLE gs_typed(color gs_color,d gs_domain)", 0))
		run(fixture.Exec("INSERT INTO gs_typed VALUES('red',1),('red',1),('blue',2)", 3))
		run(fixture.Exec("CREATE VIEW gs_typed_view AS SELECT color::text AS color,d::int AS d,GROUPING(color,d) AS g,count(*) AS n FROM gs_typed GROUP BY DISTINCT GROUPING SETS((color,d),(color),(),(color,d))", 0))
		s := fixture.GroupingSetsBag("SELECT color,d,g,n FROM gs_typed_view", []string{"color", "d", "g", "n"}, []string{"TEXT", "INT4", "INT4", "INT8"}, [][]any{{"red", int64(1), int64(0), int64(2)}, {"blue", int64(2), int64(0), int64(1)}, {"red", nil, int64(1), int64(2)}, {"blue", nil, int64(1), int64(1)}, {nil, nil, int64(3), int64(3)}})
		fixture.GroupingSetsBoth(run, s)
		run(fixture.Exec("CREATE VIEW gs_ordinal_view AS SELECT 7 AS k,count(*) AS n FROM gs_typed GROUP BY GROUPING SETS(GROUPING SETS((1),()))", 0))
		ordinal := fixture.GroupingSetsBag("SELECT k,n FROM gs_ordinal_view", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(3)}, {nil, int64(3)}})
		fixture.GroupingSetsBoth(run, ordinal)
		run(fixture.Exec("ALTER TABLE gs_typed RENAME TO gs_typed_renamed", 0))
		run(fixture.Exec("ALTER TABLE gs_typed_renamed RENAME COLUMN color TO shade", 0))
		for _, name := range []string{"gs_typed_view", "gs_ordinal_view"} {
			var definition string
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef($1::regclass,true)", name).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("CREATE VIEW "+name+"_replay AS "+definition, 0))
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			foreign := fixture.NewFixture(t, false, true)
			for range 2 {
				if err := stuntgres.Restore(foreign.Ctx, foreign.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			for _, step := range []fixture.Step{s, ordinal} {
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, step)
				step.Query += "_replay"
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Prepared(step))
			}
		} else {
			for _, step := range []fixture.Step{s, ordinal} {
				step.Query += "_replay"
				fixture.GroupingSetsBoth(run, step)
			}
		}
	})
}

func TestReferenceGroupingSetsFeatureSmoke(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY ROLLUP(a,b)", false))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY CUBE(a,b)", true))
		for _, modifier := range []string{"ALL ", "DISTINCT "} {
			rows := [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}
			if modifier == "ALL " {
				rows = append(rows, []any{int64(1), int64(0), int64(2)}, []any{nil, int64(0), int64(1)})
			}
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a,GROUPING(a),count(*) FROM gs GROUP BY "+modifier+"GROUPING SETS((a),(a),())", []string{"a", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, rows))
		}
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT count(*),sum(v) FROM gs_empty GROUP BY ()", []string{"count", "sum"}, []string{"INT8", "INT8"}, [][]any{{int64(0), nil}}))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT EXISTS(SELECT 1 FROM gs_empty GROUP BY ()) AS present", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError("SELECT GROUPING(a+1) FROM gs GROUP BY a", "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "a+1"))
		groupingSetsMetadata(t, b, run)
		groupingSetsInsertAtomicity(t, run)
		groupingSetsLifecycle(t, p, b, c, run)
	})
}
