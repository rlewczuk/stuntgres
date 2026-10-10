package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceGroupingSetsLifecycle(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		groupingSetsLifecycle(t, p, b, c, run)
	})
}

func groupingSetsLifecycle(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
	t.Helper()
	run(referenceExec("CREATE VIEW gs_view AS SELECT a,b,GROUPING(a,b) AS g,count(*) AS n,sum(v) AS s FROM gs GROUP BY ROLLUP(a,b)", 0))
	run(referenceExec("ALTER TABLE gs RENAME TO gs_renamed", 0))
	run(referenceExec("ALTER TABLE gs_renamed RENAME COLUMN a TO key", 0))
	q := "SELECT a,b,g AS grouping,n AS count,s AS sum FROM gs_view"
	s := groupingSetsLevel(q, false)
	groupingSetsBoth(run, s)
	var definition string
	if err := c.QueryRowContext(p.ctx, "SELECT pg_get_viewdef('gs_view'::regclass,true)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	run(referenceExec("CREATE VIEW gs_replay AS "+definition, 0))
	replay := groupingSetsLevel("SELECT a,b,g AS grouping,n AS count,s AS sum FROM gs_replay", false)
	groupingSetsBoth(run, replay)
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	referenceRunStep(t, p.ctx, held, b.recorder, s)
	var dump bytes.Buffer
	if !b.pg {
		old, err := c.QueryContext(p.ctx, "SELECT a,b,g,n,s FROM gs_view ORDER BY g,a NULLS FIRST,b NULLS FIRST")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		before, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, held, b.recorder, s)
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
		golden := groupingSetsRollupRows()
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
	run(referenceExec("INSERT INTO gs_renamed VALUES(1,10,7)", 1))
	changed := s
	changed.rows = groupingSetsRollupRows()
	changed.rows[0] = []any{int64(1), int64(10), int64(0), int64(2), int64(9)}
	changed.rows[3] = []any{int64(1), nil, int64(1), int64(3), int64(12)}
	changed.rows[5] = []any{nil, nil, int64(3), int64(4), int64(17)}
	referenceRunStep(t, p.ctx, held, b.recorder, changed)
	if !b.pg {
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore: %v", err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE TABLE partial(v INT); SELECT FROM")); err == nil {
			t.Fatal("malformed restore succeeded")
		}
		referenceRunStep(t, p.ctx, held, b.recorder, changed)
		foreign := newReferenceFixture(t, false, true)
		if err := stuntgres.Restore(foreign.ctx, foreign.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, s)
		referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, replay)
	}
}

func TestReferenceGroupingSetsTypedStoredDefinitions(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TYPE gs_color AS ENUM('red','blue')", 0))
		run(referenceExec("CREATE DOMAIN gs_domain AS INT", 0))
		run(referenceExec("CREATE TABLE gs_typed(color gs_color,d gs_domain)", 0))
		run(referenceExec("INSERT INTO gs_typed VALUES('red',1),('red',1),('blue',2)", 3))
		run(referenceExec("CREATE VIEW gs_typed_view AS SELECT color::text AS color,d::int AS d,GROUPING(color,d) AS g,count(*) AS n FROM gs_typed GROUP BY DISTINCT GROUPING SETS((color,d),(color),(),(color,d))", 0))
		s := groupingSetsBag("SELECT color,d,g,n FROM gs_typed_view", []string{"color", "d", "g", "n"}, []string{"TEXT", "INT4", "INT4", "INT8"}, [][]any{{"red", int64(1), int64(0), int64(2)}, {"blue", int64(2), int64(0), int64(1)}, {"red", nil, int64(1), int64(2)}, {"blue", nil, int64(1), int64(1)}, {nil, nil, int64(3), int64(3)}})
		groupingSetsBoth(run, s)
		run(referenceExec("CREATE VIEW gs_ordinal_view AS SELECT 7 AS k,count(*) AS n FROM gs_typed GROUP BY GROUPING SETS(GROUPING SETS((1),()))", 0))
		ordinal := groupingSetsBag("SELECT k,n FROM gs_ordinal_view", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(3)}, {nil, int64(3)}})
		groupingSetsBoth(run, ordinal)
		run(referenceExec("ALTER TABLE gs_typed RENAME TO gs_typed_renamed", 0))
		run(referenceExec("ALTER TABLE gs_typed_renamed RENAME COLUMN color TO shade", 0))
		for _, name := range []string{"gs_typed_view", "gs_ordinal_view"} {
			var definition string
			if err := c.QueryRowContext(p.ctx, "SELECT pg_get_viewdef($1::regclass,true)", name).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("CREATE VIEW "+name+"_replay AS "+definition, 0))
		}
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			foreign := newReferenceFixture(t, false, true)
			for range 2 {
				if err := stuntgres.Restore(foreign.ctx, foreign.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			for _, step := range []referenceStep{s, ordinal} {
				referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, step)
				step.query += "_replay"
				referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referencePrepared(step))
			}
		} else {
			for _, step := range []referenceStep{s, ordinal} {
				step.query += "_replay"
				groupingSetsBoth(run, step)
			}
		}
	})
}

func TestReferenceGroupingSetsFeatureSmoke(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		groupingSetsBoth(run, groupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY ROLLUP(a,b)", false))
		groupingSetsBoth(run, groupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY CUBE(a,b)", true))
		for _, modifier := range []string{"ALL ", "DISTINCT "} {
			rows := [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}
			if modifier == "ALL " {
				rows = append(rows, []any{int64(1), int64(0), int64(2)}, []any{nil, int64(0), int64(1)})
			}
			groupingSetsBoth(run, groupingSetsBag("SELECT a,GROUPING(a),count(*) FROM gs GROUP BY "+modifier+"GROUPING SETS((a),(a),())", []string{"a", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, rows))
		}
		groupingSetsBoth(run, referenceSelect("SELECT count(*),sum(v) FROM gs_empty GROUP BY ()", []string{"count", "sum"}, []string{"INT8", "INT8"}, [][]any{{int64(0), nil}}))
		groupingSetsBoth(run, referenceSelect("SELECT EXISTS(SELECT 1 FROM gs_empty GROUP BY ()) AS present", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		groupingSetsAnalysisError(run, groupingSetsError("SELECT GROUPING(a+1) FROM gs GROUP BY a", "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "a+1"))
		groupingSetsMetadata(t, b, run)
		groupingSetsInsertAtomicity(t, run)
		groupingSetsLifecycle(t, p, b, c, run)
	})
}
