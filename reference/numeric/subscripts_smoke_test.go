package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func subscriptsReadStored(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 || cols[0].Name() != "s" || cols[0].DatabaseTypeName() != "INT4" {
		t.Fatal("stored metadata", cols)
	}
	var got [][]any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		if _, ok := v.(int64); !ok {
			t.Fatalf("INT4 cell %T", v)
		}
		got = append(got, []any{v})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, seriesRows(-2, -1, 0)) {
		t.Fatal("stored indices", got)
	}
	if cols[0].Name() != "s" || cols[0].DatabaseTypeName() != "INT4" {
		t.Fatal("descriptor changed at EOF")
	}
}

func subscriptsControls(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE VIEW subscripts_control_view AS SELECT generate_subscripts('[-2:0]={10,NULL,30}'::integer[],1) AS s", 0))
			run(fixture.Exec("CREATE TABLE subscripts_control_data AS SELECT s FROM subscripts_control_view", 3))
			check := func() {
				for _, table := range []string{"subscripts_control_view", "subscripts_control_data"} {
					seriesBoth(t, p, b, c, fixture.Select("SELECT s FROM "+table+" ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(-2, -1, 0)))
				}
			}
			check()
			if b.PG {
				return
			}
			retained, err := p.Native.QueryContext(p.Ctx, "SELECT s FROM subscripts_control_view ORDER BY s")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
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
			check()
			run(fixture.Exec("UPDATE subscripts_control_data SET s=99", 3))
			subscriptsReadStored(t, retained)
			bad := "CREATE TABLE subscripts_partial(s integer); INSERT INTO subscripts_partial SELECT generate_subscripts('{1}'::integer[],1);"
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
			want := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT query sources are unsupported")
			fixture.AssertDiagnostic(t, err, &want)
			run(fixture.Select("SELECT s FROM subscripts_control_data", []string{"s"}, []string{"INT4"}, seriesRows(99, 99, 99)))
			missing := "SELECT * FROM subscripts_partial"
			run(fixture.ErrorDiagnostic(missing, seriesDiagnostic(missing, "subscripts_partial", "42P01", `relation "subscripts_partial" does not exist`, "")))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(emitted, again.Bytes()) {
				t.Fatalf("redump changed symbolic definition/materialized rows\noriginal:\n%s\nrestored:\n%s", emitted, again.Bytes())
			}
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("subscripts-restored")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"subscripts_control_view", "subscripts_control_data"} {
				rows, err := fresh.QueryContext(p.Ctx, "SELECT s FROM "+table+" ORDER BY s")
				if err != nil {
					t.Fatal(err)
				}
				subscriptsReadStored(t, rows)
			}
		})
	}
	// Replay actual native Dump bytes on PostgreSQL, not an invented control API.
	if p.Postgres != nil {
		if len(emitted) == 0 {
			t.Fatal("no actual native SQL dump for PG replay")
		}
		for _, q := range []string{"DROP VIEW subscripts_control_view", "DROP TABLE subscripts_control_data"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual native dump replay", err)
		}
		for _, table := range []string{"subscripts_control_view", "subscripts_control_data"} {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT s FROM "+table+" ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(-2, -1, 0)))
		}
	}
}

func TestReferenceSubscriptsControls(t *testing.T) { subscriptsControls(t) }

func TestReferenceSubscriptsLifecycle(t *testing.T) {
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Many bounded subscripts invocations, not a fabricated oversized array.
		const huge = "SELECT generate_subscripts('{1,2}'::integer[],generate_series(1,2147483647)) AS s"
		for _, prepared := range []bool{false, true} {
			if b.PG {
				seriesServerCancel(t, p, b, c, huge, prepared)
			} else {
				seriesNativeCancel(t, p, c, huge, prepared, true)
			}
		}
		seriesBoth(t, p, b, c, fixture.Select("SELECT generate_subscripts('{1,2}'::integer[],1) AS s ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2)))
		if b.PG {
			return
		}
		pre, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := c.QueryContext(pre, huge)
		if rows != nil {
			rows.Close()
			t.Fatal("precanceled published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("ordinary precancellation", err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		rows, err = c.QueryContext(ctx, "SELECT generate_subscripts('{1,2}'::integer[],1) AS s ORDER BY s")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var saved int64
		if err := rows.Scan(&saved); err != nil {
			t.Fatal(err)
		}
		cancel()
		if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
			t.Fatal("retained cancellation", rows.Err())
		}
		rows.Close()
		rows.Close()
		if saved != 1 || len(cols) != 1 || cols[0].DatabaseTypeName() != "INT4" {
			t.Fatal("retained cell/metadata changed")
		}
	})
}

func TestReferenceSubscriptsFeatureSmoke(t *testing.T) {
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT generate_subscripts('[-2:0]={10,NULL,30}'::integer[],1) AS s ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(-2, -1, 0)),
			fixture.Select("SELECT generate_subscripts($1::integer[],2,true) AS s ORDER BY s DESC", []string{"s"}, []string{"INT4"}, seriesRows(5, 4, 3), "[-1:0][3:5]={{1,NULL,3},{4,5,6}}"),
			fixture.Select("SELECT generate_subscripts(NULL::integer[],1) AS s", []string{"s"}, []string{"INT4"}, nil),
			fixture.Select("SELECT generate_subscripts("+subscriptsMatrix+",3) AS s", []string{"s"}, []string{"INT4"}, nil),
			fixture.Select("SELECT generate_subscripts("+subscriptsMatrix+",2,NULL) AS s", []string{"s"}, []string{"INT4"}, nil),
			fixture.Select("SELECT generate_subscripts("+subscriptsMatrix+",1) AS a,generate_subscripts("+subscriptsMatrix+",2) AS b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(-1), int64(3)}, {int64(0), int64(4)}, {nil, int64(5)}}),
		} {
			seriesBoth(t, p, b, c, s)
		}
		subscriptsParameters(t, p, b, c)
		subscriptsAtomicity(t, p, b, c)
	})
	subscriptsControls(t)
}
