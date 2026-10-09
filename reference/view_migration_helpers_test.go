package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"testing"
)

// Retrieve the actual deparser output through the recorded consumer, then use it.
func referenceViewCopy(t *testing.T, p *referencePair, b referenceBackend, name, copyName string) {
	t.Helper()
	referenceViewCopySQL(t, p, b, copyName, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name)
}

// Presentation variants share the same recorded body and semantic replay path.
func referenceViewCopySQL(t *testing.T, p *referencePair, b referenceBackend, copyName, query string, args ...any) {
	t.Helper()
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
	rows, err := b.db.QueryContext(ctx, query, args...)
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
	referenceViewRecorded(t, b.recorder, query, columns, []string{"TEXT"}, [][]any{{definition}}, false)
	referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE VIEW "+copyName+" AS "+definition, 0))
}

func referenceViewDump(t *testing.T, p *referencePair) []byte {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.Bytes()
}

func referenceViewRestore(t *testing.T, p *referencePair, dump []byte) {
	t.Helper()
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
}

func referenceViewHold(t *testing.T, p *referencePair, b referenceBackend, query string) referenceHeldStatement {
	t.Helper()
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
	stmt, err := b.db.PrepareContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Errorf("held statement close: %v", err)
		}
	})
	return referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: query}
}

// Used only for serial manual probes; concurrent consumers inspect their own operations.
func referenceViewRecorded(t *testing.T, recorder *referenceRecorder, query string, columns, types []string, want [][]any, bag bool) {
	t.Helper()
	recorder.mu.Lock()
	var found *referenceOperation
	for _, op := range recorder.operations {
		if op.SQL == query && op.Columns != nil && op.Err == nil {
			found = op
		}
	}
	if found == nil {
		recorder.mu.Unlock()
		t.Fatal("missing recorded view probe")
	}
	rows, names, closed, closeErr := found.Rows, append([]string(nil), found.Columns...), found.Closed, found.CloseErr
	recorder.mu.Unlock()
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
			referenceAssertRawValue(t, recorder.rawBackend == referenceRawPostgres, types[j], v)
			normalized[i][j] = referenceNormalize(v)
		}
	}
	if _, ok := referenceCompareRows(normalized, want, bag); !ok {
		t.Fatal("recorded owned rows", normalized, "want", want)
	}
}
