package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func referenceMathConstantsStoredRows(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	s := fixture.Select("SELECT id,p,c,literal FROM constants_stored_view ORDER BY id", []string{"id", "p", "c", "literal"}, []string{"INT4", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{int64(1), 3.141592653589793, float64(1), 3.141592653589793}, {int64(2), 3.141592653589793, nil, 3.141592653589793}})
	referenceMathTrigStep(t, p, backend, s)
}

func TestReferenceMathConstantsControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE constants_stored(id INT,d DOUBLE PRECISION,p DOUBLE PRECISION GENERATED ALWAYS AS (pi()) STORED,c DOUBLE PRECISION GENERATED ALWAYS AS (cbrt(d)) STORED,CONSTRAINT stored_pi_positive CHECK (pi()>0))", 0))
		run(fixture.Exec("CREATE INDEX stored_pi_index ON constants_stored(pi())", 0))
		run(fixture.Exec("INSERT INTO constants_stored(id,d) VALUES(1,1),(2,NULL)", 2))
		run(fixture.Exec("CREATE VIEW constants_stored_view AS SELECT id,p,c,pi() AS literal FROM constants_stored", 0))
		referenceMathConstantsStoredRows(t, p, backend)
		if backend.PG {
			return
		}
		retained, err := p.Native.QueryContext(p.Ctx, "SELECT p,c FROM constants_stored_view ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil || len(metadata) != 2 || metadata[0].DatabaseTypeName() != "FLOAT8" || metadata[1].DatabaseTypeName() != "FLOAT8" {
			t.Fatal(metadata, err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if !strings.Contains(strings.ToLower(dump.String()), "pg_catalog.pi()") {
			t.Fatal("Dump lost stored zero-arity pi expression", dump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		referenceMathConstantsStoredRows(t, p, backend)
		run(fixture.Exec("INSERT INTO constants_stored(id,d) VALUES(3,8)", 1))
		run(fixture.Select("SELECT p,c,literal FROM constants_stored_view WHERE id=3", []string{"p", "c", "literal"}, []string{"FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{3.141592653589793, float64(2), 3.141592653589793}}))
		for _, want := range []any{float64(1), nil} {
			if !retained.Next() {
				t.Fatal("retained missing row", retained.Err())
			}
			var pi float64
			var cube any
			if err := retained.Scan(&pi, &cube); err != nil || pi != 3.141592653589793 || cube != want {
				t.Fatal(pi, cube, err)
			}
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained EOF", retained.Err())
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		if metadata[0].DatabaseTypeName() != "FLOAT8" || metadata[1].DatabaseTypeName() != "FLOAT8" {
			t.Fatal("retained metadata changed")
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("math-constants-restored")
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		var pi, cube, literal float64
		if err := fresh.QueryRowContext(p.Ctx, "SELECT p,c,literal FROM constants_stored_view WHERE id=1").Scan(&pi, &cube, &literal); err != nil || pi != 3.141592653589793 || cube != 1 || literal != pi {
			t.Fatal(pi, cube, literal, err)
		}
		if _, err := fresh.ExecContext(p.Ctx, "INSERT INTO constants_stored(id,d) VALUES(3,8)"); err != nil {
			t.Fatal(err)
		}
		if err := fresh.QueryRowContext(p.Ctx, "SELECT p,c,literal FROM constants_stored_view WHERE id=3").Scan(&pi, &cube, &literal); err != nil || pi != 3.141592653589793 || cube != 2 || literal != pi {
			t.Fatal(pi, cube, literal, err)
		}
	})
	if p.Postgres != nil && emitted != nil {
		backend := fixture.Backend{Name: "postgres_replay", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP VIEW constants_stored_view", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP TABLE constants_stored", 0))
		if _, err := backend.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual emitted Dump replay", err)
		}
		referenceMathConstantsStoredRows(t, p, backend)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO constants_stored(id,d) VALUES(3,8)", 1))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT p,c,literal FROM constants_stored_view WHERE id=3", []string{"p", "c", "literal"}, []string{"FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{3.141592653589793, float64(2), 3.141592653589793}}))
	}
}

func TestReferenceMathConstantsFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range []referenceScalarFloatCase{
			{"cbrt", "SELECT cbrt(-8) AS result", "FLOAT8", -2, 2},
			{"degrees", "SELECT degrees('0.5') AS result", "FLOAT8", 28.64788975654116, 2},
			{"radians", "SELECT radians(45) AS result", "FLOAT8", .7853981633974483, 2},
			{"pi", "SELECT pi() AS result", "FLOAT8", 3.141592653589793, 0},
			{"erf", "SELECT erf(1) AS result", "FLOAT8", .8427007929497149, 2},
			{"erfc", "SELECT erfc(1) AS result", "FLOAT8", .15729920705028513, 2},
			{"gamma", "SELECT gamma(6) AS result", "FLOAT8", 120, 2},
			{"exp", "SELECT exp('1'::float8) AS result", "FLOAT8", 2.718281828459045, 2},
		} {
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, tc, prepared)
			}
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE VIEW constants_smoke_view AS SELECT pi() AS p", 0))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT p FROM constants_smoke_view", []string{"p"}, []string{"FLOAT8"}, [][]any{{3.141592653589793}}))
	})
}
