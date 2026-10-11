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

var referenceMathGcdLcmStoredNames = []string{"gi", "li", "gb", "lb", "gn", "ln"}
var referenceMathGcdLcmStoredTypes = []string{"INT4", "INT4", "INT8", "INT8", "NUMERIC", "NUMERIC"}
var referenceMathGcdLcmStoredRow = []any{int64(21), int64(23562), int64(21), int64(23562), "0.0600", "3.6000"}

const referenceMathGcdLcmStoredQuery = "SELECT gi,li,gb,lb,gn,ln FROM gl_view"

func referenceMathGcdLcmRawRow(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, referenceMathGcdLcmStoredNames) {
		t.Fatal(names, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != len(referenceMathGcdLcmStoredTypes) {
		t.Fatal("metadata width", len(metadata))
	}
	for i, column := range metadata {
		if column.DatabaseTypeName() != referenceMathGcdLcmStoredTypes[i] {
			t.Fatal("stored type", i, column.DatabaseTypeName())
		}
		if _, _, ok := column.DecimalSize(); ok {
			t.Fatal("native stored result inherited typmod")
		}
	}
	if !rows.Next() {
		t.Fatal("missing stored row", rows.Err())
	}
	got := make([]any, len(metadata))
	dest := make([]any, len(metadata))
	for i := range got {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, referenceMathGcdLcmStoredRow) {
		t.Fatalf("stored row %#v want %#v", got, referenceMathGcdLcmStoredRow)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("stored EOF", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	// Retain actual cached descriptors after Rows.Close, not a reconstructed copy.
	for i, column := range metadata {
		if column.Name() != names[i] || column.DatabaseTypeName() != referenceMathGcdLcmStoredTypes[i] {
			t.Fatal("cached descriptor changed after Close")
		}
		if _, _, ok := column.DecimalSize(); ok {
			t.Fatal("cached descriptor inherited typmod after Close")
		}
	}
}

func referenceMathGcdLcmStored(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var emitted []byte
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE gl_stored(a INTEGER,b INTEGER,n NUMERIC,m NUMERIC)", 0))
		run(fixture.Exec("INSERT INTO gl_stored VALUES(1071,462,-1.20,0.1800)", 1))
		run(fixture.Exec("CREATE VIEW gl_view AS SELECT gcd(a,b) AS gi,lcm(a,b) AS li,gcd(a::bigint,b::bigint) AS gb,lcm(a::bigint,b::bigint) AS lb,gcd(n,m) AS gn,lcm(n,m) AS ln FROM gl_stored", 0))
		check := func() {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(referenceMathGcdLcmStoredQuery, referenceMathGcdLcmStoredNames, referenceMathGcdLcmStoredTypes, [][]any{referenceMathGcdLcmStoredRow})
				s.Prepared = prepared
				run(s)
			}
		}
		check()
		if b.PG {
			return
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, referenceMathGcdLcmStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained, err := p.Native.QueryContext(p.Ctx, referenceMathGcdLcmStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		cached, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
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
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, referenceMathGcdLcmStoredQuery}, b.Recorder, fixture.Select(referenceMathGcdLcmStoredQuery, referenceMathGcdLcmStoredNames, referenceMathGcdLcmStoredTypes, [][]any{referenceMathGcdLcmStoredRow}))
		run(fixture.Exec("UPDATE gl_stored SET a=12,b=18", 1))
		run(fixture.Select(referenceMathGcdLcmStoredQuery, referenceMathGcdLcmStoredNames, referenceMathGcdLcmStoredTypes, [][]any{{int64(6), int64(36), int64(6), int64(36), "0.0600", "3.6000"}}))
		referenceMathGcdLcmRawRow(t, retained)
		for i, column := range cached {
			if column.Name() != referenceMathGcdLcmStoredNames[i] || column.DatabaseTypeName() != referenceMathGcdLcmStoredTypes[i] {
				t.Fatal("retained cached descriptor changed")
			}
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check()
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("gcd-lcm-restored")
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		rows, err := fresh.QueryContext(p.Ctx, referenceMathGcdLcmStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		referenceMathGcdLcmRawRow(t, rows)
	})
	if p.Postgres != nil && emitted != nil {
		b := fixture.Backend{Name: "postgres_replay", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP VIEW gl_view", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE gl_stored", 0))
		if _, err := b.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual native Dump replay", err)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(referenceMathGcdLcmStoredQuery, referenceMathGcdLcmStoredNames, referenceMathGcdLcmStoredTypes, [][]any{referenceMathGcdLcmStoredRow})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
}

func TestReferenceMathGcdLcmControls(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmStored(t, p)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		if b.PG {
			return
		} // Native adapter contract, not a pq timing oracle.
		query := "SELECT gcd($1::numeric,0.1800) AS g,lcm($1::numeric,0.1800) AS l"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		insert := "INSERT INTO gl_stored SELECT gcd(12,18),lcm(12,18),gcd(1.20,0.1800),lcm(1.20,0.1800)"
		write, err := b.DB.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var result sql.Result
			var err error
			if prepared {
				rows, err = stmt.QueryContext(ctx, "-1.20")
			} else {
				rows, err = b.DB.QueryContext(ctx, query, "-1.20")
			}
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled query", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary query cancellation acquired SQLSTATE", err)
			}
			if prepared {
				result, err = write.ExecContext(ctx)
			} else {
				result, err = b.DB.ExecContext(ctx, insert)
			}
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled write publication", result, err)
			}
			if errors.As(err, &state) {
				t.Fatal("ordinary write cancellation acquired SQLSTATE", err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(referenceMathGcdLcmStoredQuery, referenceMathGcdLcmStoredNames, referenceMathGcdLcmStoredTypes, [][]any{referenceMathGcdLcmStoredRow}))
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, query}, b.Recorder, fixture.Select(query, []string{"g", "l"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"0.0600", "3.6000"}}, "-1.20"))
	})
}

func TestReferenceMathGcdLcmFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd(1071,462) AS gi,lcm(1071,462) AS li,gcd(1071::bigint,462::bigint) AS gb,lcm(1071::bigint,462::bigint) AS lb,gcd(-1.20,0.1800) AS gn,lcm(-1.20,0.1800) AS ln", referenceMathGcdLcmStoredNames, referenceMathGcdLcmStoredTypes, [][]any{referenceMathGcdLcmStoredRow}))
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd(0,0) AS g,lcm('-9223372036854775808'::bigint,0::bigint) AS l,lcm(0.0,'Infinity'::numeric) AS n", []string{"g", "l", "n"}, []string{"INT4", "INT8", "NUMERIC"}, [][]any{{int64(0), int64(0), "NaN"}}))
		q := "SELECT gcd($1,18) AS g,lcm($1,18) AS l"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		for _, frame := range []struct {
			input any
			want  []any
		}{{int64(-12), []any{int64(6), int64(36)}}, {nil, []any{nil, nil}}, {"9", []any{int64(9), int64(18)}}} {
			s := fixture.Select(q, []string{"g", "l"}, []string{"INT4", "INT4"}, [][]any{frame.want}, frame.input)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathGcdLcmMetadata(t, rows, b.PG) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		failure := referenceMathGcdLcmFailure(q, "22003", "integer out of range")
		failure.Args = []any{int64(2147483647)}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"g", "l"}, []string{"INT4", "INT4"}, [][]any{{int64(18), int64(0)}}, int64(0)))
	})
	referenceMathGcdLcmStored(t, p)
}
