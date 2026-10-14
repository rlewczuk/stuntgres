package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func regroleScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"rr_role", "Mixed.Role", `a"b`, "select", "10", "rr_held"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			conn, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range []string{`CREATE ROLE rr_role`, `CREATE ROLE "Mixed.Role"`, `CREATE ROLE "a""b"`, `CREATE ROLE "select"`, `CREATE ROLE "10"`} {
				fixture.RunStep(t, ctx, conn, backend.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, backend, conn)
		})
	}
}

func regroleBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func TestReferenceRegroleValues(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var bootstrap string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&bootstrap); err != nil {
			t.Fatal(err)
		}
		regroleBoth(t, p, b, c, fixture.Select("SELECT $1::regrole::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{int64(10)}}, bootstrap))
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT 'rr_role'::regrole AS r,' RR_ROLE '::regrole AS folded,'"Mixed.Role"'::regrole AS q,'"a""b"'::regrole AS escaped,'"select"'::regrole AS keyword`, []string{"r", "folded", "q", "escaped", "keyword"}, []string{"REGROLE", "REGROLE", "REGROLE", "REGROLE", "REGROLE"}, [][]any{{"rr_role", "rr_role", `"Mixed.Role"`, `"a""b"`, `"select"`}}),
			fixture.Select(`SELECT '10'::regrole::oid::bigint AS id,'pg_monitor'::regrole::oid::bigint AS monitor`, []string{"id", "monitor"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(3373)}}),
			fixture.Select(`SELECT '-'::regrole AS r,'0'::regrole AS z,'4294967295'::regrole AS missing,NULL::regrole AS n`, []string{"r", "z", "missing", "n"}, []string{"REGROLE", "REGROLE", "REGROLE", "REGROLE"}, [][]any{{"-", "-", "4294967295", nil}}),
			fixture.Select(`SELECT '"10"'::regrole AS named,'10'::regrole AS numeric,'010'::regrole::oid::bigint AS octal`, []string{"named", "numeric", "octal"}, []string{"REGROLE", "REGROLE", "INT8"}, [][]any{{`"10"`, bootstrap, int64(8)}}),
			fixture.Select(`SELECT (-1)::int4::regrole AS r,4294967295::bigint::regrole::bigint AS id`, []string{"r", "id"}, []string{"REGROLE", "INT8"}, [][]any{{"4294967295", int64(4294967295)}}),
		} {
			regroleBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers=on", 0))
		defer c.ExecContext(p.Ctx, "RESET quote_all_identifiers")
		regroleBoth(t, p, b, c, fixture.Select(`SELECT 'rr_role'::regrole AS r,'0'::regrole AS z,'4294967295'::regrole AS missing`, []string{"r", "z", "missing"}, []string{"REGROLE", "REGROLE", "REGROLE"}, [][]any{{`"rr_role"`, "-", "4294967295"}}))
	})
}

func TestReferenceRegroleParameters(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var bootstrap string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&bootstrap); err != nil {
			t.Fatal(err)
		}
		q := "SELECT $1::regrole AS r,$1::regrole::oid::bigint AS id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			input    any
			spelling any
			id       any
		}{
			{"rr_role", "rr_role", nil}, {`"Mixed.Role"`, `"Mixed.Role"`, nil}, {"10", bootstrap, int64(10)}, {"0", "-", int64(0)}, {"4294967295", "4294967295", int64(4294967295)}, {nil, nil, nil},
		} {
			if tc.input != nil && tc.id == nil {
				var id int64
				if err := c.QueryRowContext(p.Ctx, "SELECT $1::regrole::oid::bigint", tc.input).Scan(&id); err != nil {
					t.Fatal(err)
				}
				if id <= 0 {
					t.Fatal("invalid generated role identity", id)
				}
				tc.id = id
			}
			s := fixture.Select(q, []string{"r", "id"}, []string{"REGROLE", "INT8"}, [][]any{{tc.spelling, tc.id}}, tc.input)
			regroleBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		regroleBoth(t, p, b, c, fixture.Select("SELECT $1::text::regrole AS r", []string{"r"}, []string{"REGROLE"}, [][]any{{"rr_role"}}, "rr_role"))
		regroleBoth(t, p, b, c, fixture.Select("SELECT $1::regrole[] AS a", []string{"a"}, []string{"_REGROLE"}, [][]any{{"{rr_role,NULL,-,4294967295}"}}, "{rr_role,NULL,0,4294967295}"))
		input := []byte("rr_role")
		rows, err := c.QueryContext(p.Ctx, "SELECT $1::regrole AS r", input)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for i := range input {
			input[i] = 'x'
		}
		fixture.ArrayReferenceConsumeHeld(t, rows, [][]any{{"rr_role"}}, []string{"REGROLE"})
	})
}

func TestReferenceRegrolePreparedIdentity(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE rr_held", 0))
		var oldID, newID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'rr_held'::regrole::oid::bigint").Scan(&oldID); err != nil {
			t.Fatal(err)
		}
		queries := []string{"SELECT 'rr_held'::regrole AS r,'rr_held'::regrole::oid::bigint AS id", "SELECT $1::regrole AS r,$1::regrole::oid::bigint AS id", "SELECT $1::text::regrole AS r,$1::text::regrole::oid::bigint AS id"}
		stmts := make([]*sql.Stmt, len(queries))
		for i, q := range queries {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			stmts[i] = stmt
		}
		var eager *sql.Rows
		if !b.PG {
			var err error
			eager, err = stmts[0].QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer eager.Close()
		}
		for _, q := range []string{"DROP ROLE rr_held", "CREATE ROLE rr_held"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT 'rr_held'::regrole::oid::bigint").Scan(&newID); err != nil {
			t.Fatal(err)
		}
		if oldID == newID {
			t.Fatal("recreated role retained identity")
		}
		if eager != nil {
			fixture.ArrayReferenceConsumeHeld(t, eager, [][]any{{"rr_held", oldID}}, []string{"REGROLE", "INT8"})
		}
		for i, stmt := range stmts {
			var args []any
			if i != 0 {
				args = []any{"rr_held"}
			}
			var spelling string
			var id int64
			if err := stmt.QueryRowContext(p.Ctx, args...).Scan(&spelling, &id); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				if id != oldID || spelling != strconv.FormatInt(oldID, 10) {
					t.Errorf("held literal %d/%q want old %d/numeric", id, spelling, oldID)
				}
			} else if id != newID || spelling != "rr_held" {
				t.Errorf("current input identity %d/%q want %d/rr_held", id, spelling, newID)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DISCARD PLANS", 0))
		var spelling string
		var id int64
		if err := stmts[0].QueryRowContext(p.Ctx).Scan(&spelling, &id); err != nil {
			t.Fatal(err)
		}
		if id != newID || spelling != "rr_held" {
			t.Errorf("DISCARD reanalysis %d/%q want new %d/rr_held", id, spelling, newID)
		}
	})
}
