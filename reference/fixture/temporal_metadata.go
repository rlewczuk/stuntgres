package fixture

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
	Executor
	t        *testing.T
	pg       bool
	metadata *[]*sql.ColumnType
}

func (x temporalMetadataExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := x.Executor.QueryContext(ctx, query, args...)
	if err != nil || rows == nil {
		return rows, err
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return nil, err
	}
	*x.metadata = meta
	TemporalMetadata(x.t, x.pg, meta)
	return rows, nil
}

func TemporalMetadata(t *testing.T, pg bool, meta []*sql.ColumnType) {
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

func TemporalRunStep(t *testing.T, ctx context.Context, db Executor, recorder *Recorder, s Step) Observed {
	t.Helper()
	s.Rows = TemporalExpected(s.Rows)
	pg := recorder.RawBackend == RawPostgres
	var meta []*sql.ColumnType
	x := temporalMetadataExecutor{Executor: db, t: t, pg: pg, metadata: &meta}
	// Prepare returns a real held statement; wrap its Query route as well.
	if s.Prepared {
		stmt, err := db.PrepareContext(context.WithValue(ctx, RecorderKey{}, recorder), s.Query)
		if err != nil {
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare exposed a handle")
			}
			diag := CheckDiagnostic(t, err, s, "prepare")
			return Observed{state: State(t, err, s.State), Diagnostic: diag, ErrorStage: "prepare"}
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		x.Executor = HeldStatement{Executor: db, Statement: stmt, SQL: s.Query}
		s.Prepared = false
	}
	observed := RunStep(t, ctx, x, recorder, s)
	TemporalMetadata(t, pg, meta)
	if !s.SelectRows || s.State != "" {
		return observed
	}
	for i, typ := range meta {
		if typ.Name() != s.Columns[i] || typ.DatabaseTypeName() != s.Types[i] {
			t.Fatal("retained temporal descriptor changed", typ)
		}
	}
	recorder.Mu.Lock()
	var values [][]any
	for _, op := range recorder.Operations {
		if op.SQL != s.Query || op.Err != nil || op.Columns == nil {
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
	recorder.Mu.Unlock()
	for _, row := range values {
		for i, value := range row {
			if s.Types[i] != "TIMESTAMP" || value == nil {
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

func TemporalRecorded(t *testing.T, recorder *Recorder, s Step) {
	t.Helper()
	s.Rows = TemporalExpected(s.Rows)
	recorder.Mu.Lock()
	var recorded *Operation
	for _, op := range recorder.Operations {
		if op.SQL == s.Query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil {
		recorder.Mu.Unlock()
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
	recorder.Mu.Unlock()
	for _, row := range captured {
		for i, value := range row {
			AssertRawValue(t, recorder.RawBackend == RawPostgres, s.Types[i], value)
			if stamp, ok := value.(time.Time); ok && s.Types[i] == "TIMESTAMP" {
				_, offset := stamp.Zone()
				if offset != 0 {
					t.Fatal("raw TIMESTAMP offset", stamp)
				}
			}
			row[i] = Normalize(value)
		}
	}
	if !reflect.DeepEqual(columns, s.Columns) || !RecordedRowsMatch(captured, s) {
		t.Fatalf("recorded temporal columns=%v rows=%#v want=%#v", columns, captured, s.Rows)
	}
	if !closed || closeErr != nil {
		t.Fatalf("recorded temporal closure %v/%v", closed, closeErr)
	}
}

func TemporalAdvanceClock(t *testing.T, ctx context.Context, stamp time.Time) {
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

func TemporalExpected(rows [][]any) [][]any {
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
			normalized[i][j] = Normalize(value)
		}
	}
	if normalized != nil {
		return normalized
	}
	return rows
}

func TemporalDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	diagnostic := Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: want.Code, Message: want.Message,
		Detail: want.Detail, Hint: want.Hint, Position: want.Position, InternalPosition: want.InternalPosition,
		InternalQuery: want.InternalQuery, Context: want.Context, Schema: want.Schema, Table: want.Table,
		Column: want.Column, DataType: want.DataType, Constraint: want.Constraint}
	State(t, err, want.Code)
	AssertDiagnostic(t, err, &diagnostic)
}
