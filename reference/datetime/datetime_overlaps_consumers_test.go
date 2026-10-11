package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeOverlapsConsumers(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		expr := "(a,z) OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04')"
		run(fixture.Exec("CREATE TABLE ov_periods(id integer,a timestamp,z timestamp,hit boolean GENERATED ALWAYS AS ("+expr+") STORED,CONSTRAINT ov_order CHECK(NOT ((a,a) OVERLAPS (z,z))))", 0))
		run(fixture.Exec("CREATE INDEX ov_expr ON ov_periods (("+expr+"))", 0))
		run(fixture.Exec("INSERT INTO ov_periods(id,a,z) VALUES(1,TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03'),(2,TIMESTAMP '2001-01-04',TIMESTAMP '2001-01-05')", 2))
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT id,hit,"+expr+" AS calculated FROM ov_periods ORDER BY id", []string{"id", "hit", "calculated"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), false, false}}),
				fixture.Select("SELECT id FROM ov_periods WHERE "+expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT "+expr+" AS hit,count(*) AS n FROM ov_periods GROUP BY "+expr+" ORDER BY "+expr, []string{"hit", "n"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(1)}, {true, int64(1)}}),
				fixture.Select("SELECT l.id,r.id AS peer FROM ov_periods l JOIN ov_periods r ON (l.a,l.z) OVERLAPS (r.a,r.z) ORDER BY l.id,r.id", []string{"id", "peer"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
				fixture.Select("SELECT id FROM ov_periods l WHERE EXISTS (SELECT 1 FROM ov_periods r WHERE (l.a,l.z) OVERLAPS (r.a,r.z)) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			} {
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Exec("CREATE TABLE ov_defaults(id integer,hit boolean DEFAULT ((TIMESTAMP '2001-01-01',TIME '02:00') OVERLAPS (TIMESTAMP '2001-01-01',TIME '01:00')))", 0))
		run(fixture.Exec("INSERT INTO ov_defaults(id) VALUES(1)", 1))
		run(fixture.Select("SELECT hit FROM ov_defaults", []string{"hit"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("UPDATE ov_defaults SET hit=(TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-02') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-03') WHERE id=1", 1))
		run(fixture.Select("SELECT hit FROM ov_defaults", []string{"hit"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("CREATE VIEW ov_view AS SELECT id,"+expr+" AS hit FROM ov_periods", 0))
		run(fixture.Exec("ALTER TABLE ov_periods RENAME COLUMN a TO start_at", 0))
		run(fixture.Exec("ALTER TABLE ov_periods RENAME TO ov_renamed", 0))
		view := fixture.Select("SELECT id,hit FROM ov_view ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}})
		run(view)
		for _, typ := range []string{"timestamptz", "timetz"} {
			run(fixture.Exec("CREATE TABLE ov_immutable_"+typ+"(a "+typ+",z "+typ+",hit boolean GENERATED ALWAYS AS ((a,z) OVERLAPS (a,z)) STORED)", 0))
			run(fixture.Exec("CREATE INDEX ov_immutable_index_"+typ+" ON ov_immutable_"+typ+" (((a,z) OVERLAPS (a,z)))", 0))
		}
		for _, tc := range []struct{ query, message string }{
			{"CREATE TABLE ov_stable(a timestamptz,d interval,hit boolean GENERATED ALWAYS AS ((a,d) OVERLAPS (a,d)) STORED)", "generation expression is not immutable"},
			{"CREATE TABLE ov_date(a date,z date,hit boolean GENERATED ALWAYS AS ((a,z) OVERLAPS (a,z)) STORED)", "generation expression is not immutable"},
			{"CREATE INDEX ov_stable_index ON ov_renamed (((start_at::timestamptz,INTERVAL '1 day') OVERLAPS (start_at::timestamptz,INTERVAL '1 day')))", "functions in index expression must be marked IMMUTABLE"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic("42P17", tc.message))
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			})
		}
		// CHECK rejection occurs at execution, has relation fields, and is atomic.
		d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"ov_renamed\" violates check constraint \"ov_order\"")
		d.Detail = "Failing row contains (3, 2001-01-02 00:00:00, 2001-01-02 00:00:00, t)."
		d.Schema = "public"
		d.Table = "ov_renamed"
		d.Constraint = "ov_order"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO ov_renamed(id,start_at,z) VALUES(9,TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03'),(3,TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-02')", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(view)
		}
		if !b.PG {
			datetimeOverlapsControls(t, p, b, c, view)
		}
	})
}

func TestReferenceDatetimeOverlapsAtomicity(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ov_atomic(id integer,hit boolean)", 0))
		run(fixture.Exec("INSERT INTO ov_atomic VALUES(1,true)", 1))
		run(fixture.Exec("CREATE TABLE ov_source(id integer,a timestamp,d interval)", 0))
		run(fixture.Exec("INSERT INTO ov_source VALUES(2,TIMESTAMP '2001-01-01',INTERVAL '1 day'),(3,TIMESTAMP '294276-12-31',INTERVAL '1 day')", 2))
		overflow := fixture.ControlsReferenceDiagnostic("22008", "timestamp out of range")
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run("row_dependent_insert_select/"+route.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				s := fixture.ErrorDiagnostic("INSERT INTO ov_atomic SELECT id,(a,d) OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04') FROM ov_source ORDER BY id", overflow)
				s.Prepared = route.prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT id,hit FROM ov_atomic ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
			})
		}
		run(fixture.Exec("CREATE TABLE ov_default_error(id integer,hit boolean DEFAULT ((TIMESTAMP '294276-12-31',INTERVAL '1 day') OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04')))", 0))
		run(fixture.Exec("INSERT INTO ov_default_error VALUES(1,false)", 1))
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run("constant_default/"+route.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				// Pinned PostgreSQL retains the SQL wrapper Context for this constant default.
				d := fixture.ControlsReferenceDiagnostic("22008", "timestamp out of range")
				d.Context = "SQL function \"overlaps\" statement 1"
				s := fixture.ErrorDiagnostic("INSERT INTO ov_default_error(id) VALUES(2)", d)
				s.Prepared = route.prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT id,hit FROM ov_default_error ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), false}}))
			})
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := c.ExecContext(ctx, "INSERT INTO ov_atomic VALUES(4,(TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-04'))")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled insert result=%v err=%v", result, err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("cancellation fabricated SQLSTATE")
			}
		}
		run(fixture.Select("SELECT id FROM ov_atomic ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func datetimeOverlapsControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, view fixture.Step) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), view.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: c, rows: old}, b.Recorder, view)
	if meta[1].DatabaseTypeName() != "BOOL" {
		t.Fatal("retained BOOL descriptor lost")
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, view)
	peer, recorded, recorder := fixture.IntervalReferenceDestination(t, p, "overlaps-peer")
	if err := stuntgres.Restore(p.Ctx, peer, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, recorded, recorder, view)
	fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("INSERT INTO ov_defaults(id) VALUES(2)", 1))
	fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT hit FROM ov_defaults WHERE id=2", []string{"hit"}, []string{"BOOL"}, [][]any{{true}}))
	fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("INSERT INTO ov_renamed(id,start_at,z) VALUES(4,TIMESTAMP '2001-01-04',TIMESTAMP '2001-01-05')", 1))
	fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT hit FROM ov_renamed WHERE id=4", []string{"hit"}, []string{"BOOL"}, [][]any{{false}}))
	bad := "CREATE TABLE ov_bad(hit boolean); INSERT INTO ov_bad VALUES((TIMESTAMP '294276-12-31',INTERVAL '1 day') OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-02'));"
	d := fixture.ControlsReferenceDiagnostic("22008", "timestamp out of range")
	d.Context = "SQL function \"overlaps\" statement 1"
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad)), &d)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, view)
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual emitted SQL replay: %v\n%s", err, dump.String())
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, view)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}
