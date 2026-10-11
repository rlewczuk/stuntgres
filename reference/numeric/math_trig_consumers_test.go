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

func TestReferenceMathTrigConsumerAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{backend.DB, t}, backend.Recorder, s)
		}
		run(fixture.Exec("CREATE TABLE trig_source(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO trig_source VALUES(1,'0.5'),(2,'2')", 2))
		run(fixture.Exec("CREATE TABLE trig_target(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO trig_target VALUES(99,'7')", 1))
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{
				"INSERT INTO trig_target SELECT id,asind(d) FROM trig_source ORDER BY id",
				"UPDATE trig_source SET d=acosd(d)",
			} {
				failure := referenceMathTrigDomain(query)
				failure.SelectRows = false
				if prepared {
					failure = fixture.Prepared(failure)
				}
				run(failure)
				run(fixture.Select("SELECT id,d FROM trig_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(99), float64(7)}}))
				run(fixture.Select("SELECT id,d FROM trig_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0.5)}, {int64(2), float64(2)}}))
			}
		}
		run(fixture.Exec("UPDATE trig_source SET d='-0.5' WHERE id=2", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO trig_target SELECT id,asind(d) FROM trig_source ORDER BY id", 2)))
		run(fixture.Select("SELECT id,d FROM trig_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(30)}, {int64(2), float64(-30)}, {int64(99), float64(7)}}))
		run(fixture.Exec("INSERT INTO trig_target SELECT id,asind(d) FROM trig_source ORDER BY id", 2))
		run(fixture.Select("SELECT id,d FROM trig_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(30)}, {int64(1), float64(30)}, {int64(2), float64(-30)}, {int64(2), float64(-30)}, {int64(99), float64(7)}}))
		run(fixture.Prepared(fixture.Exec("UPDATE trig_source SET d=acosd(d)", 2)))
		run(fixture.Select("SELECT id,d FROM trig_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(60)}, {int64(2), float64(120)}}))
		// This direct successful consumer uses exact input1 rather than pinning
		// platform-sensitive atand(60/120) in a generic fixture equality check.
		run(fixture.Exec("UPDATE trig_source SET d=1", 2))
		run(fixture.Exec("UPDATE trig_source SET d=atand(d)", 2))
		run(fixture.Select("SELECT id,d FROM trig_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(45)}, {int64(2), float64(45)}}))
	})
}

func TestReferenceMathTrigAggregateConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE trig_aggregate(id INT,d DOUBLE PRECISION)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO trig_aggregate VALUES(1,'0.5'),(2,'-0.5'),(3,NULL)", 3))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT sum(asind(d)),count(atan2d(d,1)) FROM trig_aggregate", []string{"sum", "count"}, []string{"FLOAT8", "INT8"}, [][]any{{float64(0), int64(2)}}))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT id,asind(d) FROM trig_aggregate ORDER BY asind(d) NULLS LAST", []string{"id", "asind"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(2), float64(-30)}, {int64(1), float64(30)}, {int64(3), nil}}))
	})
}

func TestReferenceMathTrigCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		// Precancellation is a deterministic native adapter contract. A fast pq
		// request may complete before its watcher; it is not a SQL error oracle.
		if backend.PG {
			return
		}
		const query = "SELECT asind($1)"
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
				rows, err = stmt.QueryContext(ctx, 0.5)
			} else {
				rows, err = backend.DB.QueryContext(ctx, query, 0.5)
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
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, query}, backend.Recorder, fixture.Select(query, []string{"asind"}, []string{"FLOAT8"}, [][]any{{float64(30)}}, 0.5))
	})
}

// Stored-view IEEE cells use the existing feature-local predicate, never NaN
// DeepEqual or backend-generated expectations. This also proves consumed buffers.
func referenceMathTrigView(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	for _, tc := range []struct {
		id   string
		want float64
	}{
		{"1", 30}, {"2", math.Copysign(0, -1)}, {"3", math.NaN()},
	} {
		for _, name := range []string{"angle", "quadrant"} {
			want := tc.want
			if name == "quadrant" && tc.id == "1" {
				want = 135
			}
			query := "SELECT " + name + " AS result FROM trig_view WHERE id=" + tc.id
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{name + tc.id, query, "FLOAT8", want, 0}, prepared)
			}
		}
	}
	step := fixture.Select("SELECT angle,quadrant FROM trig_view WHERE id=4", []string{"angle", "quadrant"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{nil, nil}})
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, true) }
	referenceMathTrigStep(t, p, backend, step)
}

func TestReferenceMathTrigControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE trig_stored(id INT,d DOUBLE PRECISION,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO trig_stored VALUES(1,'0.5',1,-1),(2,'-0','-0',1),(3,'NaN','NaN',1),(4,NULL,NULL,1)", 4))
		run(fixture.Exec("CREATE VIEW trig_view AS SELECT id,asind(d) AS angle,atan2d(y,x) AS quadrant FROM trig_stored", 0))
		referenceMathTrigView(t, p, backend)
		if backend.PG {
			return
		}
		const query = "SELECT angle FROM trig_view WHERE id=1"
		stmt, err := backend.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained, err := p.Native.QueryContext(p.Ctx, "SELECT angle,quadrant FROM trig_view ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"angle", "quadrant"}) {
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
		referenceMathTrigView(t, p, backend)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, query}, backend.Recorder, fixture.Select(query, []string{"angle"}, []string{"FLOAT8"}, [][]any{{float64(30)}}))
		run(fixture.Exec("UPDATE trig_stored SET d='-0.5',y=-1,x=1 WHERE id=1", 1))
		referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{"new_reader", "SELECT angle AS result FROM trig_view WHERE id=1", "FLOAT8", -30, 0}, false)
		referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{"new_quadrant", "SELECT quadrant AS result FROM trig_view WHERE id=1", "FLOAT8", -45, 0}, true)
		referenceMathTrigMetadata(t, retained, false, true)
		for _, want := range [][2]any{{float64(30), float64(135)}, {math.Copysign(0, -1), math.Copysign(0, -1)}, {math.NaN(), math.NaN()}, {nil, nil}} {
			if !retained.Next() {
				t.Fatal("missing retained row", retained.Err())
			}
			var got [2]any
			if err := retained.Scan(&got[0], &got[1]); err != nil {
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
		referenceMathTrigView(t, p, backend)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(redump.Bytes(), emitted) {
			t.Fatal("stable replay", err)
		}
	})
	// Actual native-emitted SQL has no invented PostgreSQL control counterpart.
	// Discovery omits native, so this phase first runs in the completed dual mode.
	if p.Postgres != nil && emitted != nil {
		backend := fixture.Backend{Name: "postgres_replay", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP VIEW trig_view", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP TABLE trig_stored", 0))
		if _, err := backend.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual Dump replay", err)
		}
		referenceMathTrigView(t, p, backend)
	}
}

func TestReferenceMathTrigFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range []referenceScalarFloatCase{
			{"acos", "SELECT acos(-1) AS result", "FLOAT8", 3.141592653589793, 2},
			{"acosd", "SELECT acosd('-0.5') AS result", "FLOAT8", 120, 0},
			{"asin", "SELECT asin(1) AS result", "FLOAT8", 1.5707963267948966, 2},
			{"asind", "SELECT asind('0.5') AS result", "FLOAT8", 30, 0},
			{"atan", "SELECT atan(1) AS result", "FLOAT8", 0.7853981633974483, 2},
			{"atand", "SELECT atand(1) AS result", "FLOAT8", 45, 0},
			{"atan2", "SELECT atan2(1,-1) AS result", "FLOAT8", 2.356194490192345, 2},
			{"atan2d", "SELECT atan2d(1,-1) AS result", "FLOAT8", 135, 0},
		} {
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, tc, prepared)
			}
		}
		const query = "SELECT asind($1)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{backend.DB, stmt, query}
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"asind"}, []string{"FLOAT8"}, [][]any{{float64(30)}}, "0.5"))
		failure := referenceMathTrigDomain(query)
		failure.Args = []any{2}
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, failure)
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"asind"}, []string{"FLOAT8"}, [][]any{{nil}}, nil))
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"asind"}, []string{"FLOAT8"}, [][]any{{float64(-30)}}, "-0.5"))
	})
}
