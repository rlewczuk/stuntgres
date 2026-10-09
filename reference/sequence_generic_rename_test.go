package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func referenceGenericRenameUtility(t *testing.T, p *referencePair, c *sql.Conn, q string, prepared bool) error {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(p.ctx, q)
		if prepareErr != nil {
			t.Fatal("utility Prepare", q, prepareErr)
		}
		defer stmt.Close()
		result, err = stmt.ExecContext(p.ctx)
	} else {
		result, err = c.ExecContext(p.ctx, q)
	}
	if err == nil {
		if n, countErr := result.RowsAffected(); countErr != nil || n != 0 {
			t.Fatalf("utility count=%d err=%v", n, countErr)
		}
	}
	return err
}

// Native log_cnt is exactly zero; PostgreSQL's physical WAL field is
// nonnegative and unchanged across renames/rejected utilities, not backend parity.
func referenceGenericRenameState(t *testing.T, p *referencePair, c *sql.Conn, name string, value int64, called, pg bool) int64 {
	t.Helper()
	recorder := p.memoryRecord
	if pg {
		recorder = p.postgresRecord
	}
	query := "SELECT * FROM " + name
	recorder.mu.Lock()
	firstOperation := len(recorder.operations)
	recorder.mu.Unlock()
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, recorder)
	rows, err := c.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	// Capture this newly opened cursor before consumption, not a prior same-SQL result.
	recorder.mu.Lock()
	var recorded *referenceOperation
	for _, op := range recorder.operations[firstOperation:] {
		if op.SQL == query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	recorder.mu.Unlock()
	if recorded == nil {
		t.Fatal("sequence state query has no recorded cursor", query)
	}
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"last_value", "log_cnt", "is_called"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	for i, want := range []string{"INT8", "INT8", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatal("type", i, types[i].DatabaseTypeName(), want)
		}
	}
	if !rows.Next() {
		t.Fatal("missing sequence row", rows.Err())
	}
	var rawValue, rawLog, rawCalled any
	if err = rows.Scan(&rawValue, &rawLog, &rawCalled); err != nil {
		t.Fatal("sequence state scan", err)
	}
	got, valueOK := rawValue.(int64)
	log, logOK := rawLog.(int64)
	isCalled, calledOK := rawCalled.(bool)
	if !valueOK || !logOK || !calledOK {
		t.Fatalf("raw sequence state types got %T/%T/%T want int64/int64/bool", rawValue, rawLog, rawCalled)
	}
	if got != value || isCalled != called || log < 0 {
		t.Fatal("sequence state", got, log, isCalled)
	}
	if !pg && log != 0 {
		t.Fatal("native log_cnt must be exactly zero", log)
	}
	var typedValue, typedLog int64
	var typedCalled bool
	if err := rows.Scan(&typedValue, &typedLog, &typedCalled); err != nil {
		t.Fatal("typed sequence state scan", err)
	}
	if typedValue != value || typedCalled != called || (!pg && typedLog != 0) ||
		(pg && (typedLog < 0 || typedLog != log)) {
		t.Fatal("typed sequence state", typedValue, typedLog, typedCalled)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("sequence row count", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal("sequence state close", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if !recorded.Closed || recorded.CloseErr != nil || !reflect.DeepEqual(recorded.Columns, columns) ||
		len(recorded.Rows) != 1 || len(recorded.Rows[0]) != 3 {
		t.Fatal("consumed sequence state recording/closure", query, recorded)
	}
	recordedValue, valueOK := recorded.Rows[0][0].(int64)
	recordedLog, logOK := recorded.Rows[0][1].(int64)
	recordedCalled, calledOK := recorded.Rows[0][2].(bool)
	if !valueOK || !logOK || !calledOK {
		t.Fatalf("recorded sequence state types got %T/%T/%T want int64/int64/bool",
			recorded.Rows[0][0], recorded.Rows[0][1], recorded.Rows[0][2])
	}
	if recordedValue != value || recordedCalled != called || (!pg && recordedLog != 0) ||
		(pg && recordedLog != log) {
		t.Fatal("recorded sequence state", recorded.Rows[0])
	}
	return log
}

func TestReferenceSequenceGenericRelationRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+query, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						c, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s START 14 CACHE 1")
						held, err := c.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						log := referenceGenericRenameState(t, p, c, "s", 14, false, backend.pg)
						if err = referenceGenericRenameUtility(t, p, c, "ALTER TABLE s RENAME TO identity", prepared); err != nil {
							t.Fatal(err)
						}
						var value int64
						err = held.QueryRowContext(p.ctx).Scan(&value)
						want := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
						want.Position = "16"
						referenceAssertDiagnostic(t, err, &want)
						if after := referenceGenericRenameState(t, p, c, "identity", 14, false, backend.pg); after != log {
							t.Fatal("rename/failed held query changed log_cnt", log, after)
						}
						parameter, err := c.PrepareContext(p.ctx, "SELECT nextval($1)")
						if err != nil {
							t.Fatal(err)
						}
						defer parameter.Close()
						referenceRunStep(t, p.ctx, referenceHeldStatement{c, parameter, "SELECT nextval($1)"}, backend.recorder,
							sequenceInt64Step(referenceSelect("SELECT nextval($1)", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(14)}}, "identity")))
						sequenceProbeValue(t, p.ctx, c, "SELECT currval('identity')", 14)
						sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 14)
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('identity')", 15)
					})
				}
			})
		}
	}
}

func TestReferenceSequenceGenericColumnRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, keyword := range []string{"COLUMN ", ""} {
			for _, source := range []string{"last_value", "missing", "ctid"} {
				q := "ALTER TABLE s RENAME " + keyword + source + " TO renamed"
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+q, func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							c, err := backend.db.Conn(p.ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s START 14 CACHE 1")
							sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 14)
							log := referenceGenericRenameState(t, p, c, "s", 14, true, backend.pg)
							err = referenceGenericRenameUtility(t, p, c, q, prepared)
							want := controlsReferenceDiagnostic("42809", `cannot rename columns of relation "s"`)
							want.Detail = "This operation is not supported for sequences."
							referenceAssertDiagnostic(t, err, &want)
							if after := referenceGenericRenameState(t, p, c, "s", 14, true, backend.pg); after != log {
								t.Fatal("rejected rename changed log_cnt", log, after)
							}
							sequenceProbeValue(t, p.ctx, c, "SELECT currval('s')", 14)
							sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 14)
							sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 15)
						})
					}
				})
			}
		}
	}
}
