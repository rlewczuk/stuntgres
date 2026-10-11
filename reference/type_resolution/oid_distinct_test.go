package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOIDMixedDistinct(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE one(id INT,s SMALLINT,i INT,b BIGINT,v OID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO one VALUES(1,-1,-1,4294967295,'4294967295'),(2,NULL,NULL,NULL,NULL),(3,-1,-1,4294967295,NULL),(4,1,1,1,'0'),(5,NULL,NULL,NULL,'4294967295')", 5))
	q := "SELECT v IS DISTINCT FROM s,s IS DISTINCT FROM v,v IS NOT DISTINCT FROM s,s IS NOT DISTINCT FROM v,v IS DISTINCT FROM i,i IS DISTINCT FROM v,v IS NOT DISTINCT FROM i,i IS NOT DISTINCT FROM v,v IS DISTINCT FROM b,b IS DISTINCT FROM v,v IS NOT DISTINCT FROM b,b IS NOT DISTINCT FROM v FROM one ORDER BY id"
	columns, types := make([]string, 12), make([]string, 12)
	for i := range columns {
		columns[i], types[i] = "?column?", "BOOL"
	}
	step := fixture.Select(q, columns, types, [][]any{
		{false, false, true, true, false, false, true, true, false, false, true, true},
		{false, false, true, true, false, false, true, true, false, false, true, true},
		{true, true, false, false, true, true, false, false, true, true, false, false},
		{true, true, false, false, true, true, false, false, true, true, false, false},
		{true, true, false, false, true, true, false, false, true, true, false, false},
	})
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			func() {
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				backend.Recorder.Mu.Lock()
				recordStart := len(backend.Recorder.Operations)
				backend.Recorder.Mu.Unlock()
				rows, closeQuery, err := fixture.TransactionQuery(ctx, backend.DB, q, prepared)
				if err != nil {
					t.Fatal(err)
				}
				defer closeQuery()
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, columns) {
					t.Fatal(names, err)
				}
				descriptors, err := rows.ColumnTypes()
				if err != nil || len(descriptors) != len(types) {
					t.Fatal(descriptors, err)
				}
				for _, typ := range descriptors {
					if typ.DatabaseTypeName() != "BOOL" || typ.ScanType() != reflect.TypeFor[bool]() {
						t.Fatal(typ)
					}
				}
				values := make([][]any, 0, len(step.Rows))
				for rows.Next() {
					row, dest := make([]any, len(columns)), make([]any, len(columns))
					for i := range row {
						dest[i] = &row[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for _, value := range row {
						fixture.AssertRawValue(t, backend.PG, "BOOL", value)
					}
					values = append(values, row)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := closeQuery(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(values, step.Rows) {
					t.Fatal(values, step.Rows)
				}
				backend.Recorder.Mu.Lock()
				var captured *fixture.Operation
				for _, op := range backend.Recorder.Operations[recordStart:] {
					if op.SQL == q && op.Err == nil && op.Columns != nil {
						captured = op
					}
				}
				backend.Recorder.Mu.Unlock()
				if captured == nil || !reflect.DeepEqual(captured.Columns, columns) || len(captured.Rows) != len(step.Rows) || !captured.Closed || captured.CloseErr != nil {
					t.Fatal("BOOL query recording", captured)
				}
				for i, row := range captured.Rows {
					if len(row) != len(types) {
						t.Fatal("recorded BOOL width", row)
					}
					for j, value := range row {
						fixture.AssertRawValue(t, backend.PG, "BOOL", value)
						if value != step.Rows[i][j] {
							t.Fatal("recorded BOOL value", i, j, value)
						}
					}
				}
			}()
		}
	}
	for _, value := range []string{"(-1)::int8", "4294967296::int8"} {
		for _, kind := range []string{" IS DISTINCT FROM ", " IS NOT DISTINCT FROM "} {
			for _, q := range []string{"SELECT v" + kind + value + " FROM one", "SELECT " + value + kind + "v FROM one"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "OID out of range"})
				s.ErrorStage = "execute"
				p.Step(t, s)
				p.Step(t, fixture.Prepared(s))
			}
		}
	}
	for _, backend := range p.Backends() {
		func() {
			q := "SELECT v IS DISTINCT FROM $1,$1 IS NOT DISTINCT FROM v FROM one WHERE id=1"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				value          any
				distinct, same bool
			}{{"4294967295", false, true}, {nil, true, false}, {"0", true, false}} {
				var distinct, same bool
				if err := stmt.QueryRow(tc.value).Scan(&distinct, &same); err != nil || distinct != tc.distinct || same != tc.same {
					t.Fatal("original held DISTINCT single-row frame", distinct, same, tc, err)
				}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, columns[:2], types[:2], [][]any{{tc.distinct, tc.same}}, tc.value))
			}
		}()
	}
	s := fixture.Select("SELECT NULL::oid IS DISTINCT FROM s,s IS NOT DISTINCT FROM NULL::oid,NULL::oid IS DISTINCT FROM i,i IS NOT DISTINCT FROM NULL::oid,NULL::oid IS DISTINCT FROM b,b IS NOT DISTINCT FROM NULL::oid,v=NULL::oid FROM one WHERE id=2", columns[:7], types[:7], [][]any{{false, true, false, true, false, true, nil}})
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}
