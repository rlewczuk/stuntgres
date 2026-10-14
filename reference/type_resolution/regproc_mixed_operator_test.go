package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRegProcMixedOperatorPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "regproc_operator_store")
	var emitted []byte
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, q := range []string{
			"CREATE SCHEMA regproc_operator_store",
			"CREATE TYPE regproc_operator_store.mixed AS (p regprocedure,o regoperator,t regtype)",
			`CREATE TABLE regproc_operator_store.values_subject(id integer,c regproc_operator_store.mixed DEFAULT '("abs(integer)","+(integer,integer)",integer)'::regproc_operator_store.mixed)`,
			`CREATE VIEW regproc_operator_store.constant_view AS SELECT '("abs(integer)","+(integer,integer)",integer)'::regproc_operator_store.mixed AS c`,
		} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(`INSERT INTO regproc_operator_store.values_subject VALUES(0,'("abs(integer)","+(integer,integer)",integer)'::regproc_operator_store.mixed)`, 1))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO regproc_operator_store.values_subject(id) VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO regproc_operator_store.values_subject VALUES(2,NULL)", 1))
		regprocMixedOperatorCheck(t, p, c, r)

		const parameterSQL = "SELECT (c).p AS p,(c).o AS o,(c).t AS t,(c).p::oid::bigint AS po,(c).o::oid::bigint AS oo,(c).t::oid::bigint AS ot FROM (SELECT $1::regproc_operator_store.mixed AS c) AS input"
		held, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, frame := range []any{`("abs(integer)","+(integer,integer)",integer)`, []byte(`("abs(integer)","+(integer,integer)",integer)`), nil} {
			want := [][]any{{"abs(integer)", "+(integer,integer)", "integer", int64(1397), int64(551), int64(23)}}
			if frame == nil {
				want = [][]any{{nil, nil, nil, nil, nil, nil}}
			}
			step := fixture.Select(parameterSQL, []string{"p", "o", "t", "po", "oo", "ot"}, []string{"REGPROCEDURE", "REGOPERATOR", "REGTYPE", "INT8", "INT8", "INT8"}, want, frame)
			regprocMixedOperatorStep(t, p, c, r, step)
			regprocMixedOperatorStep(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: parameterSQL}, r, step)
		}

		if b.PG {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("DROP SCHEMA regproc_operator_store CASCADE", 0))
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("PostgreSQL rejected actual mixed routine/operator Dump", err)
			}
			regprocMixedOperatorCheck(t, p, c, r)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO regproc_operator_store.values_subject(id) VALUES(3)", 1))
			regprocMixedOperatorDefaultCheck(t, p, c, r)
			return
		}

		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty mixed routine/operator Dump")
		}
		emitted = bytes.Clone(dump.Bytes())
		retained, err := c.QueryContext(p.Ctx, "SELECT (c).p AS p,(c).o AS o,(c).t AS t FROM regproc_operator_store.values_subject WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		originalMetadata, err := retained.ColumnTypes()
		if err != nil || len(originalMetadata) != 3 {
			t.Fatal("original retained mixed metadata", originalMetadata, err)
		}
		for i, family := range []string{"REGPROCEDURE", "REGOPERATOR", "REGTYPE"} {
			if originalMetadata[i].Name() != []string{"p", "o", "t"}[i] || originalMetadata[i].DatabaseTypeName() != family || originalMetadata[i].ScanType() != reflect.TypeFor[string]() {
				t.Fatal("original retained mixed alias metadata", i, originalMetadata[i])
			}
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("mixed routine/operator Restore", err)
		}
		regprocMixedOperatorCheck(t, p, c, r)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO regproc_operator_store.values_subject(id) VALUES(3)", 1))
		regprocMixedOperatorDefaultCheck(t, p, c, r)
		metadata, err := retained.ColumnTypes()
		if err != nil || len(metadata) != 3 {
			t.Fatal("retained mixed metadata", metadata, err)
		}
		for i, family := range []string{"REGPROCEDURE", "REGOPERATOR", "REGTYPE"} {
			if metadata[i].Name() != originalMetadata[i].Name() || metadata[i].DatabaseTypeName() != family || metadata[i].ScanType() != originalMetadata[i].ScanType() {
				t.Fatal("retained mixed alias metadata changed", i, metadata[i])
			}
		}
		if !retained.Next() {
			t.Fatal("retained mixed row absent", retained.Err())
		}
		var routine, operator, typ any
		if err := retained.Scan(&routine, &operator, &typ); err != nil {
			t.Fatal(err)
		}
		if routine != "abs(integer)" || operator != "+(integer,integer)" || typ != "integer" || retained.Next() || retained.Err() != nil {
			t.Fatal("retained mixed output lost authority", routine, operator, typ, retained.Err())
		}
	})
}

func regprocMixedOperatorCheck(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
		"SELECT id,(c).p AS p,(c).o AS o,(c).t AS t FROM regproc_operator_store.values_subject ORDER BY id",
		[]string{"id", "p", "o", "t"}, []string{"INT4", "REGPROCEDURE", "REGOPERATOR", "REGTYPE"},
		[][]any{{int64(0), "abs(integer)", "+(integer,integer)", "integer"}, {int64(1), "abs(integer)", "+(integer,integer)", "integer"}, {int64(2), nil, nil, nil}},
	))
	regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
		"SELECT (c).p::oid::bigint AS p,(c).o::oid::bigint AS o,(c).t::oid::bigint AS t FROM regproc_operator_store.values_subject WHERE id=1",
		[]string{"p", "o", "t"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1397), int64(551), int64(23)}},
	))
	regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
		"SELECT (c).p AS p,(c).o AS o,(c).t AS t FROM regproc_operator_store.constant_view",
		[]string{"p", "o", "t"}, []string{"REGPROCEDURE", "REGOPERATOR", "REGTYPE"}, [][]any{{"abs(integer)", "+(integer,integer)", "integer"}},
	))
	regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
		"SELECT c::text AS c FROM regproc_operator_store.values_subject WHERE id=1",
		[]string{"c"}, []string{"TEXT"}, [][]any{{`("abs(integer)","+(integer,integer)",integer)`}},
	))
	regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
		"SELECT (c).p::oid::bigint AS p,(c).o::oid::bigint AS o,(c).t::oid::bigint AS t FROM regproc_operator_store.constant_view",
		[]string{"p", "o", "t"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1397), int64(551), int64(23)}},
	))
}

func regprocMixedOperatorDefaultCheck(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
		"SELECT (c).p::oid::bigint AS p,(c).o::oid::bigint AS o,(c).t::oid::bigint AS t FROM regproc_operator_store.values_subject WHERE id=3",
		[]string{"p", "o", "t"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1397), int64(551), int64(23)}},
	))
}

func regprocMixedOperatorBoth(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, step fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		step.Prepared = prepared
		regprocMixedOperatorStep(t, p, x, r, step)
	}
}

func regprocMixedOperatorStep(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, step fixture.Step) {
	t.Helper()
	postgres := r.RawBackend == fixture.RawPostgres
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(step.Columns) {
			t.Fatal("mixed result metadata width", columns, err)
		}
		for i, column := range columns {
			switch step.Types[i] {
			case "REGPROCEDURE", "REGOPERATOR", "REGTYPE":
				scan := reflect.TypeFor[string]()
				if postgres {
					scan = reflect.TypeFor[any]()
				}
				length, lengthOK := column.Length()
				precision, scale, decimalOK := column.DecimalSize()
				nullable, nullableOK := column.Nullable()
				if column.Name() != step.Columns[i] || column.DatabaseTypeName() != step.Types[i] || column.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || postgres && (nullable || nullableOK) {
					t.Fatal("mixed alias metadata", i, column)
				}
			}
		}
	}
	step.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		switch step.Types[column] {
		case "REGPROCEDURE", "REGOPERATOR", "REGTYPE":
			fixture.RegtypeRaw(t, postgres, value)
			return true
		default:
			return false
		}
	}
	fixture.RunStep(t, p.Ctx, x, r, step)
}
