package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceMathTrigDirectConsumerAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{backend.DB, t}, backend.Recorder, s)
		}
		run(fixture.Exec("CREATE TABLE direct_source(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO direct_source VALUES(1,30),(2,'Infinity')", 2))
		run(fixture.Exec("CREATE TABLE direct_target(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO direct_target VALUES(99,7)", 1))
		for _, name := range referenceMathTrigDirectNames {
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{
					"INSERT INTO direct_target SELECT id," + name + "(d) FROM direct_source ORDER BY id",
					"UPDATE direct_source SET d=" + name + "(d)",
				} {
					t.Run(query+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
						run := func(s fixture.Step) {
							fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{backend.DB, t}, backend.Recorder, s)
						}
						failure := referenceMathTrigDomain(query)
						failure.SelectRows, failure.Prepared = false, prepared
						run(failure)
						run(fixture.Select("SELECT id,d FROM direct_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(99), float64(7)}}))
						run(fixture.Select("SELECT id,d FROM direct_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(30)}, {int64(2), math.Inf(1)}}))
					})
				}
			}
		}
		run(fixture.Exec("UPDATE direct_source SET d=-30 WHERE id=2", 1))
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("INSERT INTO direct_target SELECT id,sind(d) FROM direct_source ORDER BY id", 2)
			step.Prepared = prepared
			run(step)
		}
		run(fixture.Select("SELECT id,d FROM direct_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(.5)}, {int64(1), float64(.5)}, {int64(2), float64(-.5)}, {int64(2), float64(-.5)}, {int64(99), float64(7)}}))
		run(fixture.Prepared(fixture.Exec("UPDATE direct_source SET d=sind(d)", 2)))
		run(fixture.Select("SELECT id,d FROM direct_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(.5)}, {int64(2), float64(-.5)}}))
		run(fixture.Exec("UPDATE direct_source SET d=60", 2))
		run(fixture.Exec("UPDATE direct_source SET d=cosd(d)", 2))
		run(fixture.Select("SELECT id,d FROM direct_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(.5)}, {int64(2), float64(.5)}}))
	})
}

func TestReferenceMathTrigDirectAggregateConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE direct_aggregate(id INT,d DOUBLE PRECISION)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO direct_aggregate VALUES(1,30),(2,-30),(3,NULL)", 3))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT sum(sind(d)),count(cosd(d)) FROM direct_aggregate", []string{"sum", "count"}, []string{"FLOAT8", "INT8"}, [][]any{{float64(0), int64(2)}}))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT id,sind(d) FROM direct_aggregate ORDER BY sind(d) NULLS LAST", []string{"id", "sind"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(2), float64(-.5)}, {int64(1), float64(.5)}, {int64(3), nil}}))
	})
}

func TestReferenceMathTrigDirectCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		// This adapter phase is intentionally native-only. PQ may finish a fast
		// request before its watcher; it is not a SQL error oracle.
		if backend.PG {
			return
		}
		const query = "SELECT sind($1)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				rows, err = stmt.QueryContext(ctx, 30)
			} else {
				rows, err = backend.DB.QueryContext(ctx, query, 30)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("canceled request returned rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatalf("ordinary cancellation acquired SQLSTATE: %v", err)
			}
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, query}, backend.Recorder, fixture.Select(query, []string{"sind"}, []string{"FLOAT8"}, [][]any{{float64(.5)}}, 30))
	})
}

func referenceMathTrigDirectView(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	for _, tc := range []struct {
		id, name string
		want     float64
	}{
		{"1", "cos", 1}, {"1", "cosd", 1}, {"1", "cot", math.Inf(1)}, {"1", "cotd", math.Inf(1)},
		{"1", "sin", 0}, {"1", "sind", 0}, {"1", "tan", 0}, {"1", "tand", 0},
		{"2", "cos", 1}, {"2", "cosd", 1}, {"2", "cot", math.Inf(-1)}, {"2", "cotd", math.Inf(-1)},
		{"2", "sin", math.Copysign(0, -1)}, {"2", "sind", math.Copysign(0, -1)}, {"2", "tan", math.Copysign(0, -1)}, {"2", "tand", 0},
	} {
		for _, prepared := range []bool{false, true} {
			referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{tc.name + tc.id, "SELECT " + tc.name + " AS result FROM direct_view WHERE id=" + tc.id, "FLOAT8", tc.want, 0}, prepared)
		}
	}
	for _, name := range referenceMathTrigDirectNames {
		for _, prepared := range []bool{false, true} {
			referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{name + "NaN", "SELECT " + name + " AS result FROM direct_view WHERE id=3", "FLOAT8", math.NaN(), 0}, prepared)
		}
		step := fixture.Select("SELECT "+name+" FROM direct_view WHERE id=4", []string{name}, []string{"FLOAT8"}, [][]any{{nil}})
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, true) }
		referenceMathTrigStep(t, p, backend, step)
	}
}

func TestReferenceMathTrigDirectControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE direct_stored(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO direct_stored VALUES(1,0),(2,'-0'),(3,'NaN'),(4,NULL)", 4))
		run(fixture.Exec("CREATE VIEW direct_view AS SELECT id,cos(d) AS cos,cosd(d) AS cosd,cot(d) AS cot,cotd(d) AS cotd,sin(d) AS sin,sind(d) AS sind,tan(d) AS tan,tand(d) AS tand FROM direct_stored", 0))
		referenceMathTrigDirectView(t, p, backend)
		if backend.PG {
			return
		}
		const query = "SELECT sind FROM direct_view WHERE id=1"
		stmt, err := backend.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained, err := p.Native.QueryContext(p.Ctx, "SELECT sin,sind,tand,cotd FROM direct_view ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"sin", "sind", "tand", "cotd"}) {
			t.Fatal(columns, err)
		}
		referenceMathTrigMetadata(t, retained, false, true)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		referenceMathTrigDirectView(t, p, backend)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, query}, backend.Recorder, fixture.Select(query, []string{"sind"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
		run(fixture.Exec("UPDATE direct_stored SET d=30 WHERE id=1", 1))
		referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{"new_reader", "SELECT sind AS result FROM direct_view WHERE id=1", "FLOAT8", .5, 0}, false)
		referenceMathTrigMetadata(t, retained, false, true)
		for _, want := range [][4]any{
			{float64(0), float64(0), float64(0), math.Inf(1)},
			{math.Copysign(0, -1), math.Copysign(0, -1), float64(0), math.Inf(-1)},
			{math.NaN(), math.NaN(), math.NaN(), math.NaN()}, {nil, nil, nil, nil},
		} {
			if !retained.Next() {
				t.Fatal("missing retained row", retained.Err())
			}
			var got [4]any
			if err := retained.Scan(&got[0], &got[1], &got[2], &got[3]); err != nil {
				t.Fatal(err)
			}
			for i := range got {
				if want[i] == nil {
					if got[i] != nil {
						t.Fatal(got)
					}
					continue
				}
				value, ok := got[i].(float64)
				if !ok || !referenceScalarFloatMatches(value, want[i].(float64), 0) {
					t.Fatalf("retained %v want %v", got, want)
				}
			}
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained end", retained.Err())
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		referenceMathTrigDirectView(t, p, backend)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(redump.Bytes(), emitted) {
			t.Fatal("stable replay", err)
		}
	})
	// Actual native-emitted SQL is replayed, not a fictitious PG Restore API.
	if p.Postgres != nil && emitted != nil {
		backend := fixture.Backend{Name: "postgres_replay", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP VIEW direct_view", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP TABLE direct_stored", 0))
		if _, err := backend.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual Dump replay", err)
		}
		referenceMathTrigDirectView(t, p, backend)
	}
}

func TestReferenceMathTrigDirectFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		referenceMathTrigDirectCases(t, p, backend, []referenceScalarFloatCase{
			{"cos", "SELECT cos(0) AS result", "FLOAT8", 1, 0},
			{"cosd", "SELECT cosd(60) AS result", "FLOAT8", .5, 0},
			{"cot", "SELECT cot('0.5') AS result", "FLOAT8", 1.830487721712452, 2},
			{"cotd", "SELECT cotd(45) AS result", "FLOAT8", 1, 0},
			{"sin", "SELECT sin(1) AS result", "FLOAT8", .8414709848078965, 2},
			{"sind", "SELECT sind(30) AS result", "FLOAT8", .5, 0},
			{"tan", "SELECT tan(1) AS result", "FLOAT8", 1.5574077246549023, 2},
			{"tand", "SELECT tand(45) AS result", "FLOAT8", 1, 0},
			{"pole", "SELECT tand(90) AS result", "FLOAT8", math.Inf(1), 0},
			{"negative_zero", "SELECT sind('-0') AS result", "FLOAT8", math.Copysign(0, -1), 0},
			{"normalized_zero", "SELECT tand('-0') AS result", "FLOAT8", 0, 0},
		})
		const query = "SELECT sind($1)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{backend.DB, stmt, query}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, backend.Recorder, s) }
		for _, input := range []any{int64(30), "30", float64(30)} {
			run(fixture.Select(query, []string{"sind"}, []string{"FLOAT8"}, [][]any{{float64(.5)}}, input))
		}
		failure := referenceMathTrigDomain(query)
		failure.Args = []any{"Infinity"}
		run(failure)
		failure = fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops")
		failure.SelectRows, failure.ErrorStage = true, "execute"
		run(failure)
		run(fixture.Select(query, []string{"sind"}, []string{"FLOAT8"}, [][]any{{nil}}, nil))
		run(fixture.Select(query, []string{"sind"}, []string{"FLOAT8"}, [][]any{{float64(-.5)}}, "-30"))
	})
}
