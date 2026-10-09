package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"stuntgres"
	"testing"
	"time"
)

// Instrument only temporal metadata; execution, comparison and recording remain
// in the shipped runner. Captured descriptors are rechecked after EOF/Close.
type temporalMetadataExecutor struct {
	referenceExecutor
	t        *testing.T
	pg       bool
	metadata *[]*sql.ColumnType
}

func (x temporalMetadataExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := x.referenceExecutor.QueryContext(ctx, query, args...)
	if err != nil || rows == nil {
		return rows, err
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return nil, err
	}
	*x.metadata = meta
	referenceTemporalMetadata(x.t, x.pg, meta)
	return rows, nil
}

func referenceTemporalMetadata(t *testing.T, pg bool, meta []*sql.ColumnType) {
	t.Helper()
	for _, typ := range meta {
		name := typ.DatabaseTypeName()
		if name != "TIME" && name != "TIMESTAMP" && name != "TIMETZ" {
			continue
		}
		want := reflect.TypeFor[time.Time]()
		if !pg && name != "TIMESTAMP" {
			want = reflect.TypeFor[string]()
		}
		if typ.ScanType() != want {
			t.Fatalf("%s ScanType %v want %v", name, typ.ScanType(), want)
		}
		if _, known := typ.Length(); known {
			t.Fatalf("%s storage length must be unknown", name)
		}
		if _, _, known := typ.DecimalSize(); known {
			t.Fatalf("%s decimal precision must be unknown", name)
		}
		if _, known := typ.Nullable(); pg && known {
			t.Fatalf("pq %s nullability must be unknown", name)
		}
	}
}

func referenceTemporalRunStep(t *testing.T, ctx context.Context, db referenceExecutor, recorder *referenceRecorder, s referenceStep) referenceObserved {
	t.Helper()
	s.rows = referenceTemporalExpected(s.rows)
	pg := recorder.rawBackend == referenceRawPostgres
	var meta []*sql.ColumnType
	x := temporalMetadataExecutor{referenceExecutor: db, t: t, pg: pg, metadata: &meta}
	// Prepare returns a real held statement; wrap its Query route as well.
	if s.prepared {
		stmt, err := db.PrepareContext(context.WithValue(ctx, referenceRecorderKey{}, recorder), s.query)
		if err != nil {
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare exposed a handle")
			}
			diag := referenceCheckDiagnostic(t, err, s, "prepare")
			return referenceObserved{state: referenceState(t, err, s.state), diagnostic: diag, errorStage: "prepare"}
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		x.referenceExecutor = referenceHeldStatement{referenceExecutor: db, statement: stmt, query: s.query}
		s.prepared = false
	}
	observed := referenceRunStep(t, ctx, x, recorder, s)
	referenceTemporalMetadata(t, pg, meta)
	if !s.selectRows || s.state != "" {
		return observed
	}
	for i, typ := range meta {
		if typ.Name() != s.columns[i] || typ.DatabaseTypeName() != s.types[i] {
			t.Fatal("retained temporal descriptor changed", typ)
		}
	}
	recorder.mu.Lock()
	var values [][]any
	for _, op := range recorder.operations {
		if op.SQL != s.query || op.Err != nil || op.Columns == nil {
			continue
		}
		values = make([][]any, len(op.Rows))
		for i, row := range op.Rows {
			values[i] = make([]any, len(row))
			for j, value := range row {
				values[i][j] = referenceCopyValue(value)
			}
		}
	}
	recorder.mu.Unlock()
	for _, row := range values {
		for i, value := range row {
			if s.types[i] != "TIMESTAMP" || value == nil {
				continue
			}
			if stamp, ok := value.(time.Time); ok {
				_, offset := stamp.Zone()
				if offset != 0 {
					t.Fatalf("raw TIMESTAMP has nonzero offset: %v", stamp)
				}
			} else {
				bytes, ok := value.([]byte)
				if !ok || bytes == nil || (string(bytes) != "infinity" && string(bytes) != "-infinity") {
					t.Fatalf("raw TIMESTAMP %T %#v", value, value)
				}
			}
		}
	}
	return observed
}

func referenceTemporalRecorded(t *testing.T, recorder *referenceRecorder, s referenceStep) {
	t.Helper()
	s.rows = referenceTemporalExpected(s.rows)
	recorder.mu.Lock()
	var recorded *referenceOperation
	for _, op := range recorder.operations {
		if op.SQL == s.query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil {
		recorder.mu.Unlock()
		t.Fatal("temporal query has no recorded result")
	}
	columns := append([]string(nil), recorded.Columns...)
	captured := make([][]any, len(recorded.Rows))
	for i, row := range recorded.Rows {
		captured[i] = make([]any, len(row))
		for j, value := range row {
			captured[i][j] = referenceCopyValue(value)
		}
	}
	closed, closeErr := recorded.Closed, recorded.CloseErr
	recorder.mu.Unlock()
	for _, row := range captured {
		for i, value := range row {
			referenceAssertRawValue(t, recorder.rawBackend == referenceRawPostgres, s.types[i], value)
			if stamp, ok := value.(time.Time); ok && s.types[i] == "TIMESTAMP" {
				_, offset := stamp.Zone()
				if offset != 0 {
					t.Fatal("raw TIMESTAMP offset", stamp)
				}
			}
			row[i] = referenceNormalize(value)
		}
	}
	if !reflect.DeepEqual(columns, s.columns) || !referenceRecordedRowsMatch(captured, s) {
		t.Fatalf("recorded temporal columns=%v rows=%#v want=%#v", columns, captured, s.rows)
	}
	if !closed || closeErr != nil {
		t.Fatalf("recorded temporal closure %v/%v", closed, closeErr)
	}
}

func referenceTemporalAdvanceClock(t *testing.T, ctx context.Context, stamp time.Time) {
	t.Helper()
	wait, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for !time.Now().After(stamp.Add(time.Microsecond)) {
		select {
		case <-wait.Done():
			t.Fatal("wall clock did not advance past captured timestamp", wait.Err())
		default:
			runtime.Gosched()
		}
	}
}

func referenceTemporalExpected(rows [][]any) [][]any {
	var normalized [][]any
	for i, row := range rows {
		copied := false
		for j, value := range row {
			if _, bytes := value.([]byte); !bytes {
				continue
			}
			if normalized == nil {
				normalized = append([][]any(nil), rows...)
			}
			if !copied {
				normalized[i] = append([]any(nil), row...)
				copied = true
			}
			normalized[i][j] = referenceNormalize(value)
		}
	}
	if normalized != nil {
		return normalized
	}
	return rows
}

func referenceTemporalDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	diagnostic := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: want.Code, Message: want.Message,
		Detail: want.Detail, Hint: want.Hint, Position: want.Position, InternalPosition: want.InternalPosition,
		InternalQuery: want.InternalQuery, Context: want.Context, Schema: want.Schema, Table: want.Table,
		Column: want.Column, DataType: want.DataType, Constraint: want.Constraint}
	referenceState(t, err, want.Code)
	referenceAssertDiagnostic(t, err, &diagnostic)
}
