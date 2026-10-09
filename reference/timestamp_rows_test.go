package reference_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"
)

func referenceTimestampConsume(t *testing.T, rows *sql.Rows, pg bool, s referenceStep) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !slices.Equal(columns, s.columns) {
		t.Fatal(columns, s.columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(s.types) {
		t.Fatal(meta, err)
	}
	for i, typ := range meta {
		if typ.DatabaseTypeName() != s.types[i] {
			t.Fatal(typ)
		}
	}
	referenceTemporalMetadata(t, pg, meta)
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
			referenceAssertRawValue(t, pg, s.types[i], value)
			if stamp, ok := value.(time.Time); ok && s.types[i] == "TIMESTAMP" {
				_, offset := stamp.Zone()
				if offset != 0 {
					t.Fatal("raw TIMESTAMP offset", stamp)
				}
			}
			row[i] = referenceNormalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	referenceTemporalMetadata(t, pg, meta)
	for i, typ := range meta {
		if typ.Name() != s.columns[i] || typ.DatabaseTypeName() != s.types[i] {
			t.Fatal("retained TIMESTAMP descriptor changed", typ)
		}
	}
	compared, matched := referenceCompareRows(values, referenceTemporalExpected(s.rows), s.bag)
	if !matched {
		t.Fatalf("TIMESTAMP rows %#v want %#v", values, s.rows)
	}
	return compared
}

func referenceTimestampExec(t *testing.T, ctx context.Context, db referenceExecutor, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(query, err)
	}
}

func referenceTimestampQuery(t *testing.T, ctx context.Context, db referenceExecutor, recorder *referenceRecorder, query string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	referenceTemporalRunStep(t, ctx, db, recorder, referenceSelect(query, names, types, want, args...))
}

func referenceTimestampCheckRows(t *testing.T, rows *sql.Rows, pg bool, names, types []string, want [][]any) {
	t.Helper()
	referenceTimestampConsume(t, rows, pg, referenceSelect("", names, types, want))
}

func referenceTimestampStep(t *testing.T, p *referencePair, s referenceStep) {
	t.Helper()
	for _, b := range p.backends() {
		referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s)
	}
}
