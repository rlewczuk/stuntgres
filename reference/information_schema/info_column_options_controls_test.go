package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func (c *copCase) controlRoles() {
	c.exec("CREATE ROLE cop_reader", "CREATE ROLE cop_denied", "GRANT SELECT(id) ON cop_scope.options TO cop_reader")
}
func (c *copCase) identities(e fixture.Executor, want [][]any, reader [][]any) {
	c.t.Helper()
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION cop_reader", 0))
	c.check(e, "cop_scope", reader)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION cop_denied", 0))
	c.check(e, "cop_scope", nil)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.check(e, "cop_scope", want)
}

func TestReferenceInfoColumnOptionsNativeControls(t *testing.T) {
	copScenario(t, func(c *copCase) {
		c.seed()
		c.controlRoles()
		want := c.seedRows()
		c.routes(copQuery, want, "cop_scope")
		c.identities(c.conn, want, want[:2])
		if c.b.PG {
			return
		}
		c.nativeControls(want, want[:2])
	})
}

func (c *copCase) nativeControls(want, reader [][]any) {
	c.t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
		c.t.Fatal(err)
	}
	for _, text := range []string{"CREATE FOREIGN DATA WRAPPER", "CREATE SERVER", "CREATE FOREIGN TABLE", "OPTIONS"} {
		if !strings.Contains(dump.String(), text) {
			c.t.Fatalf("dump missing %q: %s", text, dump.String())
		}
	}
	old, err := c.conn.QueryContext(c.p.Ctx, copQuery, "cop_scope")
	if err != nil {
		c.t.Fatal(err)
	}
	defer old.Close()
	if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
		c.t.Fatal(err)
	}
	c.routes(copQuery, nil, "cop_scope")
	c.consume(old, want)
	if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		c.t.Fatal(err)
	}
	c.routes(copQuery, want, "cop_scope")
	c.identities(c.conn, want, reader)
	connector, err := stuntgres.NewDriver().OpenConnector("cop-independent-controls")
	if err != nil {
		c.t.Fatal(err)
	}
	fresh, recorder := fixture.InsertSelectRecordedPool(c.t, connector)
	freshNative := sql.OpenDB(connector)
	defer freshNative.Close()
	independent := *c
	independent.b.Recorder = recorder
	for _, role := range []string{"cop_reader", "cop_denied"} {
		independent.run(fresh, fixture.Exec("CREATE ROLE "+role, 0))
	}
	if err := stuntgres.Restore(c.p.Ctx, freshNative, bytes.NewReader(dump.Bytes())); err != nil {
		c.t.Fatal(err)
	}
	var database string
	if err := fresh.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		c.t.Fatal(err)
	}
	remap := func(rows [][]any) [][]any {
		out := make([][]any, len(rows))
		for i, row := range rows {
			out[i] = append([]any(nil), row...)
			out[i][0] = database
		}
		return out
	}
	independent.check(fresh, "cop_scope", remap(want))
	independent.run(fresh, fixture.Prepared(independent.step(copQuery, remap(want), "cop_scope")))
	independent.identities(fresh, remap(want), remap(reader))
	c.exec("CREATE TABLE public.cop_sentinel(v integer)")
	c.run(c.conn, fixture.Exec("INSERT INTO public.cop_sentinel VALUES(91)", 1))
	prefix := dump.String() + "\nCREATE TABLE public.cop_staged(v integer); INSERT INTO public.cop_staged VALUES("
	d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
	d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
	err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
	fixture.AssertDiagnostic(c.t, err, &d)
	unchanged := func() {
		c.routes(copQuery, want, "cop_scope")
		c.identities(c.conn, want, reader)
		c.run(c.conn, fixture.Select("SELECT v FROM public.cop_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		c.run(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='cop_staged'", []string{"relname"}, []string{"NAME"}, nil))
	}
	unchanged()
	ctx, cancel := context.WithCancel(c.p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		c.t.Fatal(err)
	}
	unchanged()
	if c.p.Postgres != nil {
		pg, err := c.p.Postgres.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer pg.Close()
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS cop_scope CASCADE", "DROP SERVER IF EXISTS cop_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS cop_wrapper CASCADE", "DROP ROLE cop_reader,cop_denied"} {
				if _, err := pg.ExecContext(c.p.Ctx, q); err != nil {
					c.t.Error(err)
				}
			}
		}()
		for _, role := range []string{"cop_reader", "cop_denied"} {
			if _, err := pg.ExecContext(c.p.Ctx, "CREATE ROLE "+role); err != nil {
				c.t.Fatal(err)
			}
		}
		if _, err := pg.ExecContext(c.p.Ctx, dump.String()); err != nil {
			c.t.Fatal(err)
		}
		if err := pg.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			c.t.Fatal(err)
		}
		// Replay uses real emitted SQL, with PostgreSQL's transport descriptors.
		pgCase := *c
		for _, b := range c.p.Backends() {
			if b.PG {
				pgCase.b = b
			}
		}
		pgCase.check(pg, "cop_scope", remap(want))
		pgCase.identities(pg, remap(want), remap(reader))
	}
}

func TestReferenceInfoColumnOptionsFeatureSmoke(t *testing.T) {
	copScenario(t, func(c *copCase) {
		c.exec("CREATE FOREIGN DATA WRAPPER cop_wrapper NO HANDLER NO VALIDATOR", "CREATE SERVER cop_server FOREIGN DATA WRAPPER cop_wrapper", "CREATE SCHEMA cop_scope", "CREATE FOREIGN TABLE cop_scope.smoke(a integer OPTIONS(secret 's=1'),b text OPTIONS(empty '')) SERVER cop_server", "CREATE ROLE cop_reader", "CREATE ROLE cop_denied", "REVOKE ALL ON SCHEMA cop_scope FROM PUBLIC", "GRANT SELECT(a) ON cop_scope.smoke TO cop_reader")
		want := [][]any{c.row("smoke", "a", "secret", "s=1"), c.row("smoke", "b", "empty", "")}
		c.routes(copQuery, want, "cop_scope")
		held := c.hold(c.conn, copQuery)
		c.identities(held, want, want[:1])
		c.exec("REVOKE SELECT(a) ON cop_scope.smoke FROM cop_reader")
		c.identities(held, want, nil)
		c.exec("GRANT UPDATE(b) ON cop_scope.smoke TO cop_reader")
		c.identities(held, want, want[1:])
		c.exec("ALTER FOREIGN TABLE cop_scope.smoke ALTER COLUMN b OPTIONS(SET empty 'changed=é')")
		want[1][5] = "changed=é"
		c.identities(held, want, want[1:])
		c.check(held, nil, nil)
		c.check(held, "missing", nil)
		d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
		d.Position = fixture.Position(8)
		s := fixture.ErrorDiagnostic("SELECT absent FROM information_schema.column_options", d)
		s.SelectRows = true
		s.ErrorStage = "execute"
		c.run(c.conn, s)
		if err := held.Statement.Close(); err != nil {
			t.Fatal(err)
		}
		if !c.b.PG {
			c.nativeControls(want, want[1:])
		}
	})
}
