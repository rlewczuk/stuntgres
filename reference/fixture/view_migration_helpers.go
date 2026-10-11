package fixture

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"testing"
)

// Retrieve the actual deparser output through the recorded consumer, then use it.
func ViewCopy(t *testing.T, p *Pair, b Backend, name, copyName string) {
	t.Helper()
	ViewCopySQL(t, p, b, copyName, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name)
}

// Presentation variants share the same recorded body and semantic replay path.
func ViewCopySQL(t *testing.T, p *Pair, b Backend, copyName, query string, args ...any) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, RecorderKey{}, b.Recorder)
	rows, err := b.DB.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"definition"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
		t.Fatal(types, err)
	}
	var definition string
	if !rows.Next() {
		t.Fatal("missing definition", rows.Err())
	}
	if err := rows.Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra definition or cursor error", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	ViewRecorded(t, b.Recorder, query, columns, []string{"TEXT"}, [][]any{{definition}}, false)
	RunStep(t, p.Ctx, b.DB, b.Recorder, Exec("CREATE VIEW "+copyName+" AS "+definition, 0))
}

func ViewDump(t *testing.T, p *Pair) []byte {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.Bytes()
}

func ViewRestore(t *testing.T, p *Pair, dump []byte) {
	t.Helper()
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
}

func ViewHold(t *testing.T, p *Pair, b Backend, query string) HeldStatement {
	t.Helper()
	ctx := context.WithValue(p.Ctx, RecorderKey{}, b.Recorder)
	stmt, err := b.DB.PrepareContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Errorf("held statement close: %v", err)
		}
	})
	return HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
}

// Used only for serial manual probes; concurrent consumers inspect their own operations.
func ViewRecorded(t *testing.T, recorder *Recorder, query string, columns, types []string, want [][]any, bag bool) {
	t.Helper()
	recorder.Mu.Lock()
	var found *Operation
	for _, op := range recorder.Operations {
		if op.SQL == query && op.Columns != nil && op.Err == nil {
			found = op
		}
	}
	if found == nil {
		recorder.Mu.Unlock()
		t.Fatal("missing recorded view probe")
	}
	rows, names, closed, closeErr := found.Rows, append([]string(nil), found.Columns...), found.Closed, found.CloseErr
	recorder.Mu.Unlock()
	if !reflect.DeepEqual(names, columns) || !closed || closeErr != nil {
		t.Fatal("recorded view columns/closure", names, closed, closeErr)
	}
	normalized := make([][]any, len(rows))
	for i, row := range rows {
		if len(row) != len(types) {
			t.Fatal("recorded row width", row)
		}
		normalized[i] = make([]any, len(row))
		for j, v := range row {
			AssertRawValue(t, recorder.RawBackend == RawPostgres, types[j], v)
			normalized[i][j] = Normalize(v)
		}
	}
	if _, ok := CompareRows(normalized, want, bag); !ok {
		t.Fatal("recorded owned rows", normalized, "want", want)
	}
}
