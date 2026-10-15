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

const mathLogPowerControlQuery = "SELECT id,root,bucket,threshold,rank,text_bucket,fallback FROM mlp_control_view ORDER BY id"

var mathLogPowerControlNames = []string{"id", "root", "bucket", "threshold", "rank", "text_bucket", "fallback"}
var mathLogPowerControlTypes = []string{"INT4", "NUMERIC", "INT4", "INT4", "INT4", "INT4", "FLOAT8"}
var mathLogPowerControlRows = [][]any{{int64(1), "2.000000000000000", int64(3), int64(2), int64(2), int64(2), float64(8)}, {int64(2), nil, nil, nil, nil, int64(2), float64(8)}}

func mathLogPowerReadControl(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, mathLogPowerControlNames) {
		t.Fatal(names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(mathLogPowerControlTypes) {
		t.Fatal("metadata width", len(columns))
	}
	for i, column := range columns {
		if column.DatabaseTypeName() != mathLogPowerControlTypes[i] {
			t.Fatal("column type", i, column.DatabaseTypeName())
		}
	}
	var got [][]any
	for rows.Next() {
		values := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			values[i] = fixture.Normalize(value)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("control rows=%#v want %#v", got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		if column.Name() != names[i] || column.DatabaseTypeName() != mathLogPowerControlTypes[i] {
			t.Fatal("retained metadata changed after close")
		}
	}
}

func mathLogPowerControlScenario(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE DOMAIN mlp_control_number AS numeric CHECK (VALUE >= 0)", 0))
			run(fixture.Exec("CREATE TYPE mlp_control_rank AS ENUM ('z','a','m')", 0))
			run(fixture.Prepared(fixture.Exec("CREATE TABLE mlp_control (id integer,x mlp_control_number,thresholds mlp_control_number[],r mlp_control_rank,label text COLLATE \"C\" DEFAULT 'm',fallback float8 DEFAULT power(2::float8,3::float8),stored_bucket integer GENERATED ALWAYS AS (width_bucket(x,0::numeric,10::numeric,5)) STORED)", 0)))
			run(fixture.Exec("INSERT INTO mlp_control(id,x,thresholds,r) VALUES (1,4,ARRAY[0,4,9]::mlp_control_number[],'a'),(2,NULL,NULL,NULL)", 2))
			run(fixture.Exec("CREATE VIEW mlp_control_view AS SELECT id,sqrt(x) AS root,stored_bucket AS bucket,width_bucket(x,thresholds) AS threshold,width_bucket(r,ARRAY['z','a','m']::mlp_control_rank[]) AS rank,width_bucket(label,ARRAY['a','m','z']::text[]) AS text_bucket,fallback FROM mlp_control", 0))
			check := func() {
				mathLogPowerBoth(run, fixture.Select(mathLogPowerControlQuery, mathLogPowerControlNames, mathLogPowerControlTypes, mathLogPowerControlRows))
			}
			check()
			if backend.PG {
				return
			}
			query := "SELECT ln($1::numeric) AS result"
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(canceled, "2")
				} else {
					rows, err = backend.DB.QueryContext(canceled, query, "2")
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query published rows")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("ordinary cancellation", err)
				}
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: query}, backend.Recorder, fixture.Select(query, []string{"result"}, []string{"NUMERIC"}, [][]any{{"0.6931471805599453"}}, "2"))
			retained, err := p.Native.QueryContext(p.Ctx, mathLogPowerControlQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			metadata, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			held, err := backend.DB.PrepareContext(p.Ctx, mathLogPowerControlQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
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
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: backend.DB, Statement: held, SQL: mathLogPowerControlQuery}, backend.Recorder, fixture.Select(mathLogPowerControlQuery, mathLogPowerControlNames, mathLogPowerControlTypes, mathLogPowerControlRows))
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(emitted, redump.Bytes()) {
				t.Fatalf("deterministic redump error=%v\noriginal dump:\n%s\nrestored dump:\n%s", err, emitted, redump.Bytes())
			}
			run(fixture.Exec("UPDATE mlp_control SET x=9 WHERE id=1", 1))
			changed := [][]any{{int64(1), "3.000000000000000", int64(5), int64(3), int64(2), int64(2), float64(8)}, mathLogPowerControlRows[1]}
			run(fixture.Select(mathLogPowerControlQuery, mathLogPowerControlNames, mathLogPowerControlTypes, changed))
			mathLogPowerReadControl(t, retained, mathLogPowerControlRows)
			for i, column := range metadata {
				if column.Name() != mathLogPowerControlNames[i] || column.DatabaseTypeName() != mathLogPowerControlTypes[i] {
					t.Fatal("captured metadata changed")
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			// Actual default demand after replacement, not DDL-only acceptance.
			run(fixture.Prepared(fixture.Exec("INSERT INTO mlp_control(id,x,thresholds,r) VALUES (3,4,ARRAY[0,4,9]::mlp_control_number[],'a')", 1)))
			run(fixture.Select("SELECT fallback,stored_bucket FROM mlp_control WHERE id=3", []string{"fallback", "stored_bucket"}, []string{"FLOAT8", "INT4"}, [][]any{{float64(8), int64(3)}}))
			run(fixture.Exec("DELETE FROM mlp_control WHERE id=3", 1))
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			bad := append(bytes.Clone(emitted), []byte("\nINSERT INTO mlp_control(id,x,thresholds,r) VALUES (3,-1,NULL,NULL);\n")...)
			fixture.State(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)), "23514")
			check()
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed replacement changed catalog", err)
			}
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("math-log-power-fresh")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if _, err := fresh.ExecContext(p.Ctx, "CREATE DOMAIN mlp_offset AS numeric; CREATE TYPE mlp_offset_enum AS ENUM ('x','y')"); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			rows, err := fresh.QueryContext(p.Ctx, mathLogPowerControlQuery)
			if err != nil {
				t.Fatal(err)
			}
			mathLogPowerReadControl(t, rows, mathLogPowerControlRows)
		})
	}
	if p.Postgres != nil && emitted != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW mlp_control_view; DROP TABLE mlp_control; DROP TYPE mlp_control_rank; DROP DOMAIN mlp_control_number"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual native Dump SQL replay", err)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(mathLogPowerControlQuery, mathLogPowerControlNames, mathLogPowerControlTypes, mathLogPowerControlRows)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
	}
}

func TestReferenceMathLogPowerControls(t *testing.T) { mathLogPowerControlScenario(t) }
