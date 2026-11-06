package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"stuntgres"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
)

// dualScript is a statement stream with caller-visible outcomes rendered as
// text, so a dual run can be compared with a bare native replay.
type dualScript struct {
	table string
	extra []string // queries after the standard ones
}

func (s dualScript) statements() []string {
	return append([]string{
		"CREATE TABLE " + s.table + " (id int, name text)",
		"INSERT INTO " + s.table + " VALUES (1, 'a'), (2, 'b')",
		"SELECT id, name FROM " + s.table + " ORDER BY id",
		"SELECT * FROM dual_absent",
	}, s.extra...)
}

// want is the independently specified caller outcome of each statement.
func (s dualScript) want() []string {
	out := []string{"exec 0", "exec 2", "rows [1 a] [2 b]", `error 42P01 relation "dual_absent" does not exist`}
	for _, q := range s.extra {
		out = append(out, "rows ["+strings.TrimPrefix(q, "SELECT ")+"]")
	}
	return out
}

// run executes statement i on c; between runs after statement i (1-based).
func (s dualScript) run(t *testing.T, ctx context.Context, c *sql.Conn, between map[int]func()) []string {
	t.Helper()
	var out []string
	for i, q := range s.statements() {
		if f := between[i]; f != nil {
			f()
		}
		if strings.HasPrefix(q, "SELECT") {
			rows, err := c.QueryContext(ctx, q)
			if err != nil {
				code, message := dualSQLState(err)
				out = append(out, fmt.Sprintf("error %s %s", code, message))
				continue
			}
			var got []string
			cols, _ := rows.Columns()
			for rows.Next() {
				values := make([]sql.NullString, len(cols))
				ptrs := make([]any, len(cols))
				for i := range values {
					ptrs[i] = &values[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				var cells []string
				for _, v := range values {
					cells = append(cells, v.String)
				}
				got = append(got, "["+strings.Join(cells, " ")+"]")
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			out = append(out, "rows "+strings.Join(got, " "))
			continue
		}
		res, err := c.ExecContext(ctx, q)
		if err != nil {
			code, message := dualSQLState(err)
			out = append(out, fmt.Sprintf("error %s %s", code, message))
			continue
		}
		n, err := res.RowsAffected()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint("exec ", n))
	}
	return out
}

// check runs the script through the dual pool and through a fresh bare native
// pool; both must give the independent expectations.
func (s dualScript) check(t *testing.T, ctx context.Context, d *dualPair, between map[int]func()) []dualRecord {
	t.Helper()
	c, err := d.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := s.run(t, ctx, c, between)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name() + "/replay-" + s.table)
	if err != nil {
		t.Fatal(err)
	}
	bare := sql.OpenDB(connector)
	defer bare.Close()
	bc, err := bare.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	replay := s.run(t, ctx, bc, nil)
	if want := s.want(); !slices.Equal(got, want) || !slices.Equal(replay, want) {
		t.Fatalf("caller outcomes:\n  dual %q\nreplay %q\n  want %q", got, replay, want)
	}
	var out []dualRecord
	for _, r := range sqlRecords(d.Log.records(t)) {
		if r.Call == "query" || r.Call == "exec" {
			out = append(out, r)
		}
	}
	return out
}

func TestReferenceDualConnectorReferenceFailures(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx

	t.Run("connect", func(t *testing.T) {
		fault := newDualFault()
		fault.connectErr = errors.New("injected")
		d := newDualPair(t, p, dual.Options{}, fault.wrap)
		rs := dualScript{table: "dual_fa"}.check(t, ctx, d, nil)
		connect := d.Log.records(t)[0]
		const failure = "reference connection failed: injected"
		if connect.Call != "connect" || connect.ErrorRef != "08001" || connect.ErrmsgRef != failure {
			t.Errorf("connect record: %+v", connect)
		}
		wantDiff(t, connect, failure)
		for _, r := range rs {
			if r.ErrorRef != "08001" || r.Unmirrored != "reference connection failed" || r.ResponseDiff != nil {
				t.Errorf("%q: error_ref %q unmirrored %q diff %q", r.Query, r.ErrorRef, r.Unmirrored, r.ResponseDiff)
			}
		}
	})

	t.Run("lost", func(t *testing.T) {
		fault := newDualFault()
		d := newDualPair(t, p, dual.Options{}, fault.wrap)
		rs := dualScript{table: "dual_fb"}.check(t, ctx, d, map[int]func(){2: func() { fault.kill(t) }})
		for _, r := range rs[:2] {
			if r.ErrorRef != "" || r.ResponseDiff != nil || r.Unmirrored != "" {
				t.Errorf("before kill %q: error_ref %q diff %q", r.Query, r.ErrorRef, r.ResponseDiff)
			}
		}
		lost := rs[2]
		suffix, ok := strings.CutPrefix(lost.ErrmsgRef, "reference connection lost: ")
		if lost.ErrorRef != "08006" || !ok || suffix == "" || lost.Unmirrored != "" {
			t.Errorf("transition record: error_ref %q errmsg_ref %q unmirrored %q", lost.ErrorRef, lost.ErrmsgRef, lost.Unmirrored)
		}
		wantDiff(t, lost, lost.ErrmsgRef)
		for _, r := range rs[3:] {
			if r.ErrorRef != "08006" || r.ErrmsgRef != lost.ErrmsgRef || r.Unmirrored != "reference connection lost" || r.ResponseDiff != nil {
				t.Errorf("after loss %q: error_ref %q unmirrored %q diff %q", r.Query, r.ErrorRef, r.Unmirrored, r.ResponseDiff)
			}
		}
	})

	t.Run("error", func(t *testing.T) {
		fault := newDualFault()
		fault.inject["SELECT 2"] = sqlStateErr{"XX000", "injected"}
		d := newDualPair(t, p, dual.Options{}, fault.wrap)
		rs := dualScript{table: "dual_fc", extra: []string{"SELECT 2", "SELECT 3"}}.check(t, ctx, d, nil)
		injected := lastQuery(t, rs, "query", "SELECT 2")
		if injected.ErrorRef != "XX000" || injected.ErrmsgRef != "injected" {
			t.Errorf("injected record: error_ref %q errmsg_ref %q", injected.ErrorRef, injected.ErrmsgRef)
		}
		wantDiff(t, injected, "error: stuntgres none, reference XX000 injected")
		next := lastQuery(t, rs, "query", "SELECT 3")
		if next.ErrorRef != "" || next.Unmirrored != "" || next.ResponseDiff != nil {
			t.Errorf("after an ordinary reference error: %+v", next)
		}
	})
}
