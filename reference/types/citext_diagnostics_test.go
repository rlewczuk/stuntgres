package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextDiagnostics(t *testing.T) {
	for i, tc := range []struct {
		bytes  []byte
		suffix string
	}{
		{[]byte{0}, "0x00"}, {[]byte{255}, "0xff"}, {[]byte{195, 40}, "0xc3 0x28"}, {[]byte{226, 130}, "0xe2 0x82"},
	} {
		t.Run(fmt.Sprintf("encoding_%d", i), func(t *testing.T) {
			citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				citextInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE citext_diag(id integer NOT NULL,v citext)", 0))
				run(fixture.Exec("INSERT INTO citext_diag VALUES (1,'Sentinel')", 1))
				q := "INSERT INTO citext_diag VALUES (2,$1),(3,$2)"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				want := citextDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+tc.suffix)
				want.Context = "unnamed portal parameter $2"
				for _, input := range []any{string(tc.bytes), tc.bytes} {
					for _, prepared := range []bool{false, true} {
						citextError(t, p, b, c, q, prepared, false, false, want, "Early valid", input)
						run(fixture.CitextSelect(b, "SELECT id,v FROM citext_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "Sentinel"}}))
					}
					citextError(t, p, b, held, q, false, false, false, want, "Early valid", input)
					run(fixture.CitextSelect(b, "SELECT id,v FROM citext_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "Sentinel"}}))
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "Healthy", "Again"))
				run(fixture.CitextSelect(b, "SELECT id,v FROM citext_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "Sentinel"}, {int64(2), "Healthy"}, {int64(3), "Again"}}))
			})
		})
	}
	t.Run("declarations_and_literal", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			for _, tc := range []struct {
				q, code, message, token string
				query                   bool
			}{
				{"/*é*/ CREATE TABLE citext_bad(v citext(2))", "42601", `type modifier is not allowed for type "citext"`, "citext(2)", false},
				{"/*é*/ CREATE TABLE citext_bad(v citext COLLATE citext_missing)", "42704", `collation "citext_missing" for encoding "UTF8" does not exist`, "COLLATE", false},
				{"SELECT E'\\000'::citext AS v", "22021", `invalid byte sequence for encoding "UTF8": 0x00`, "", true},
			} {
				want := citextDiagnostic(tc.code, tc.message)
				if tc.token != "" {
					want.Position = citextPosition(tc.q, tc.token)
				}
				for _, prepared := range []bool{false, true} {
					citextError(t, p, b, c, tc.q, prepared, tc.query, tc.query, want)
				}
			}
		})
	})
	t.Run("late_not_null", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE citext_required(id integer NOT NULL,v citext NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO citext_required VALUES (1,'Sentinel')", 1))
			want := citextDiagnostic("23502", `null value in column "v" of relation "citext_required" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (3, null).", "public", "citext_required", "v"
			q := "INSERT INTO citext_required VALUES (2,$1),(3,$2)"
			for _, prepared := range []bool{false, true} {
				citextError(t, p, b, c, q, prepared, false, false, want, "Early valid", nil)
				run(fixture.CitextSelect(b, "SELECT id,v FROM citext_required ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "Sentinel"}}))
			}
		})
	})
	t.Run("cancelled_binding", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE citext_cancel(v citext)", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := c.ExecContext(ctx, "INSERT INTO citext_cancel VALUES ($1)", "Cancelled")
			wantErr := error(context.Canceled)
			var continuation fixture.Executor = c
			if b.PG {
				// A precancelled pinned pq connection is discarded at the
				// adapter boundary; continuation is not same-owner health.
				wantErr = driver.ErrBadConn
				continuation = b.DB
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("cancelled call: %v, want %v", err, wantErr)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatalf("ordinary adapter error acquired SQLSTATE %q", state.SQLState())
			}
			fixture.RunStep(t, p.Ctx, continuation, b.Recorder, fixture.CitextSelect(b, "SELECT v FROM citext_cancel", []string{"v"}, [][]any{}))
			fixture.RunStep(t, p.Ctx, continuation, b.Recorder, fixture.Exec("INSERT INTO citext_cancel VALUES ($1)", 1, "Healthy"))
			fixture.RunStep(t, p.Ctx, continuation, b.Recorder, fixture.CitextSelect(b, "SELECT v FROM citext_cancel", []string{"v"}, [][]any{{"Healthy"}}))
		})
	})
	t.Run("coercion_context", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE citext_integer(n integer)", 0))
			for _, prepared := range []bool{false, true} {
				q := "SELECT citextout('MiX'::text)"
				want := citextDiagnostic("42883", "function citextout(text) does not exist")
				want.Position = citextPosition(q, "citextout")
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				citextError(t, p, b, c, q, prepared, true, true, want)
				q = "INSERT INTO citext_integer VALUES ('42'::citext)"
				want = citextDiagnostic("42804", `column "n" is of type integer but expression is of type citext`)
				want.Position = citextPosition(q, "'42'")
				want.Hint = "You will need to rewrite or cast the expression."
				citextError(t, p, b, c, q, prepared, false, true, want)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n FROM citext_integer", []string{"n"}, []string{"INT4"}, [][]any{}))
		})
	})
	t.Run("sized_outgoing_assignment", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE citext_sized(v varchar(3))", 0))
			for _, prepared := range []bool{false, true} {
				citextError(t, p, b, c, "INSERT INTO citext_sized VALUES ('Longer'::citext)", prepared, false, false, citextDiagnostic("22001", "value too long for type character varying(3)"))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM citext_sized", []string{"v"}, []string{"VARCHAR"}, [][]any{}))
		})
	})
}
