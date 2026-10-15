package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Every statement is prepared on the same borrowed physical connection. In
// particular, database/sql cannot lazily prepare a replacement statement on a
// different connection and accidentally turn a SAME-held witness into a fresh one.
type sqlJSONObjectReplacementHolds struct {
	conn                       *sql.Conn
	builtin, result, parameter *sql.Stmt
}

const sqlJSONObjectHeldBuiltin = "SELECT json_objectagg(k:v FORMAT JSON WITH UNIQUE RETURNING jsonb) AS v FROM sjo_input WHERE g=1"
const sqlJSONObjectHeldResult = "SELECT json_objectagg(k:v FORMAT JSON WITH UNIQUE RETURNING sjo_binary) AS v FROM sjo_input WHERE g=1"
const sqlJSONObjectHeldParameter = "SELECT json_objectagg('a':$1::sjo_binary RETURNING jsonb) AS v"

func sqlJSONObjectPrepareReplacementHolds(t *testing.T, p *fixture.Pair, b fixture.Backend) *sqlJSONObjectReplacementHolds {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	h := &sqlJSONObjectReplacementHolds{conn: conn}
	t.Cleanup(h.close)
	for _, item := range []struct {
		q      string
		target **sql.Stmt
	}{{sqlJSONObjectHeldBuiltin, &h.builtin}, {sqlJSONObjectHeldResult, &h.result}, {sqlJSONObjectHeldParameter, &h.parameter}} {
		*item.target, err = conn.PrepareContext(p.Ctx, item.q)
		if err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *sqlJSONObjectReplacementHolds) close() {
	for _, s := range []*sql.Stmt{h.builtin, h.result, h.parameter} {
		if s != nil {
			s.Close()
		}
	}
	h.conn.Close()
}

func (h *sqlJSONObjectReplacementHolds) checkValues(t *testing.T, p *fixture.Pair, b fixture.Backend, all bool, value string) {
	t.Helper()
	for _, item := range []struct {
		q    string
		stmt *sql.Stmt
		args []any
		want string
	}{
		{sqlJSONObjectHeldBuiltin, h.builtin, nil, value},
		{sqlJSONObjectHeldResult, h.result, nil, value},
		{sqlJSONObjectHeldParameter, h.parameter, []any{"1"}, `{"a": 1}`},
	} {
		if !all && item.stmt != h.builtin {
			continue
		}
		s := jsonArrayAggregateSelect(item.q, "JSONB", item.want, item.args...)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			jsonArrayAggregateMetadata(t, rows, b.PG, []string{"v"}, []string{"JSONB"}, nil)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: h.conn, Statement: item.stmt, SQL: item.q}, b.Recorder, s)
	}
}

func (h *sqlJSONObjectReplacementHolds) checkFenced(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, item := range []struct {
		stmt    *sql.Stmt
		message string
		args    []any
	}{
		{h.result, "cached plan must not change result type", nil},
		{h.parameter, "cached plan must not change parameter type", []any{"1"}},
	} {
		want := fixture.ControlsReferenceDiagnostic("0A000", item.message)
		rows, err := item.stmt.QueryContext(ctx, item.args...)
		if rows != nil {
			rows.Close()
			t.Fatal("retired catalog signature published a cursor")
		}
		fixture.AssertDiagnostic(t, err, &want)
		result, err := item.stmt.ExecContext(ctx, item.args...)
		if result != nil {
			t.Fatal("retired catalog signature published a result/count")
		}
		fixture.AssertDiagnostic(t, err, &want)
	}
}
