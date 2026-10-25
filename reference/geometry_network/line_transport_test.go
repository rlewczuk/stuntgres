package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLineTransport(t *testing.T) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE line_transport(id integer NOT NULL,l line NOT NULL,n line)", 0))
		q := "INSERT INTO line_transport VALUES($1,$2,$3)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		args := []any{"{2,4,6}", []byte("{2,4,6}"), boxValuer{value: "{2,4,6}"}, boxValuer{value: []byte("{2,4,6}")}}
		for i, arg := range args {
			s := fixture.Exec(q, 1, i+1, arg, []byte(nil))
			if i == 1 {
				s.Prepared = true
			}
			if i >= 2 {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			} else {
				run(s)
			}
		}
		want := [][]any{{int64(1), "{2,4,6}", nil}, {int64(2), "{2,4,6}", nil}, {int64(3), "{2,4,6}", nil}, {int64(4), "{2,4,6}", nil}}
		run(lineSelect(b, "SELECT id,l,n FROM line_transport ORDER BY id", []string{"id", "l", "n"}, []string{"INT4", "LINE", "LINE"}, want))
		update := "UPDATE line_transport SET n=$1 WHERE id=$2"
		us, err := c.PrepareContext(p.Ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		defer us.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: us, SQL: update}, b.Recorder, fixture.Exec(update, 1, "[(1,2),(3,4)]", 1))
		run(lineSelect(b, "SELECT n FROM line_transport WHERE id=1", []string{"n"}, []string{"LINE"}, [][]any{{"{1,-1,1}"}}))
		query := "SELECT $1::line AS l"
		sel, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer sel.Close()
		sh := fixture.HeldStatement{Executor: c, Statement: sel, SQL: query}
		for _, arg := range []any{"[(1,2),(3,4)]", "{1,-1,1}", nil, []byte(nil), []byte("{1,-1,1}")} {
			value := any("{1,-1,1}")
			if arg == nil {
				value = nil
			} else if v, ok := arg.([]byte); ok && v == nil {
				value = nil
			}
			fixture.RunStep(t, p.Ctx, sh, b.Recorder, lineSelect(b, query, []string{"l"}, []string{"LINE"}, [][]any{{value}}, arg))
		}
		buf := []byte("{2,4,6}")
		rows, err := sel.QueryContext(p.Ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		copy(buf, "{9,9,9}")
		if !rows.Next() {
			t.Fatalf("retained row %v", rows.Err())
		}
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != "{2,4,6}" {
			t.Fatalf("caller mutation changed datum %q", value)
		}
		rows.Close()
		sentinel := errors.New("line Valuer failure")
		_, err = sel.QueryContext(p.Ctx, boxValuer{err: sentinel})
		if err == nil || !strings.Contains(err.Error(), sentinel.Error()) {
			t.Fatalf("Valuer error %v", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatalf("Valuer acquired SQLSTATE %s", state.SQLState())
		}
		if !b.PG {
			cancelled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err = stmt.ExecContext(cancelled, 99, "{2,4,6}", nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-cancel %v", err)
			}
		}
		d := fixture.ControlsReferenceDiagnostic("23502", "null value in column \"l\" of relation \"line_transport\" violates not-null constraint")
		d.Schema = "public"
		d.Table = "line_transport"
		d.Column = "l"
		d.Detail = "Failing row contains (9, null, null)."
		s := fixture.ErrorDiagnostic(q, d, 9, []byte(nil), nil)
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Select("SELECT count(*) AS n FROM line_transport", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}
