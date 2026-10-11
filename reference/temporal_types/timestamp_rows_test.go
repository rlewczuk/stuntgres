package reference_test

import (
	"context"
	"database/sql"
	"slices"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func referenceTimestampConsume(t *testing.T, rows *sql.Rows, pg bool, s fixture.Step) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !slices.Equal(columns, s.Columns) {
		t.Fatal(columns, s.Columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(s.Types) {
		t.Fatal(meta, err)
	}
	for i, typ := range meta {
		if typ.DatabaseTypeName() != s.Types[i] {
			t.Fatal(typ)
		}
	}
	fixture.TemporalMetadata(t, pg, meta)
	values := make([][]any, 0)
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			fixture.AssertRawValue(t, pg, s.Types[i], value)
			if stamp, ok := value.(time.Time); ok && s.Types[i] == "TIMESTAMP" {
				_, offset := stamp.Zone()
				if offset != 0 {
					t.Fatal("raw TIMESTAMP offset", stamp)
				}
			}
			row[i] = fixture.Normalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.TemporalMetadata(t, pg, meta)
	for i, typ := range meta {
		if typ.Name() != s.Columns[i] || typ.DatabaseTypeName() != s.Types[i] {
			t.Fatal("retained TIMESTAMP descriptor changed", typ)
		}
	}
	compared, matched := fixture.CompareRows(values, fixture.TemporalExpected(s.Rows), s.Bag)
	if !matched {
		t.Fatalf("TIMESTAMP rows %#v want %#v", values, s.Rows)
	}
	return compared
}

func referenceTimestampExec(t *testing.T, ctx context.Context, db fixture.Executor, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(query, err)
	}
}

func referenceTimestampQuery(t *testing.T, ctx context.Context, db fixture.Executor, recorder *fixture.Recorder, query string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	fixture.TemporalRunStep(t, ctx, db, recorder, fixture.Select(query, names, types, want, args...))
}

func referenceTimestampCheckRows(t *testing.T, rows *sql.Rows, pg bool, names, types []string, want [][]any) {
	t.Helper()
	referenceTimestampConsume(t, rows, pg, fixture.Select("", names, types, want))
}

func referenceTimestampStep(t *testing.T, p *fixture.Pair, s fixture.Step) {
	t.Helper()
	for _, b := range p.Backends() {
		fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}
