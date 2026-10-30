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

func (c *ftoCase) identities(e fixture.Executor, want [][]any) {
	c.t.Helper()
	for _, role := range []string{"fto_owner", "fto_inherit"} {
		c.run(e, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		c.run(e, c.step(ftoQuery, want, "fto_table"))
		c.run(e, fixture.Prepared(c.step(ftoQuery, want, "fto_table")))
		c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	}
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fto_reader", 0))
	c.check(e, "fto_table", want)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fto_denied", 0))
	c.check(e, "fto_table", nil)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.check(e, "fto_table", want)
}

func TestReferenceInfoForeignTableOptionsControls(t *testing.T) {
	ftoScenario(t, func(c *ftoCase) {
		c.seed()
		c.exec("CREATE ROLE fto_reader", "CREATE ROLE fto_denied", "CREATE ROLE fto_owner", "CREATE ROLE fto_inherit", "GRANT USAGE ON FOREIGN SERVER fto_server TO fto_owner", "GRANT CREATE ON SCHEMA fto_scope TO fto_owner", "ALTER FOREIGN TABLE fto_scope.fto_table OWNER TO fto_owner", "GRANT SELECT(a) ON fto_scope.fto_table TO fto_reader", "GRANT SELECT ON fto_scope.fto_table TO fto_inherit")
		want := c.seedRows()
		c.routes(ftoQuery, want, "fto_table")
		c.identities(c.conn, want)
		if c.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE FOREIGN TABLE") || !strings.Contains(dump.String(), "CREATE SERVER") || !strings.Contains(dump.String(), "CREATE FOREIGN DATA WRAPPER") || !strings.Contains(dump.String(), "visible=value") {
			t.Fatal("missing wrapper options", dump.String())
		}
		// Keep actual authorized rows across descriptor and ACL publication.
		c.exec("SET SESSION AUTHORIZATION fto_reader")
		old, err := c.conn.QueryContext(c.p.Ctx, ftoQuery, "fto_table")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		c.exec("RESET SESSION AUTHORIZATION", "ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(SET remote 'changed')", "REVOKE SELECT(a) ON fto_scope.fto_table FROM fto_reader", "SET SESSION AUTHORIZATION fto_reader")
		c.check(c.conn, "fto_table", nil)
		c.consume(old, want)
		c.exec("RESET SESSION AUTHORIZATION")
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		old, err = c.conn.QueryContext(c.p.Ctx, ftoQuery, "fto_table")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.routes(ftoQuery, nil, "fto_table")
		c.consume(old, want)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c.routes(ftoQuery, want, "fto_table")
		c.identities(c.conn, want)
		connector, err := stuntgres.NewDriver().OpenConnector("fto-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		fresh, recorder := fixture.InsertSelectRecordedPool(t, connector)
		freshNative := sql.OpenDB(connector)
		defer freshNative.Close()
		independent := *c
		independent.b.Recorder = recorder
		for _, role := range []string{"fto_reader", "fto_denied", "fto_owner", "fto_inherit"} {
			independent.run(fresh, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(c.p.Ctx, freshNative, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		destination, err := fresh.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		if err := destination.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&independent.database); err != nil {
			t.Fatal(err)
		}
		independent.routesOn(destination, independent.seedRows())
		independent.identities(destination, independent.seedRows())
		c.exec("CREATE TABLE public.fto_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.fto_sentinel VALUES(91)", 1))
		prefix := dump.String() + "\nCREATE TABLE public.fto_staged(v integer); INSERT INTO public.fto_staged VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			c.routes(ftoQuery, want, "fto_table")
			c.identities(c.conn, want)
			c.run(c.conn, fixture.Select("SELECT v FROM public.fto_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='fto_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		held := c.hold(c.conn, ftoQuery)
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		for _, err := range []error{stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())), stuntgres.Reset(ctx, c.p.Native), stuntgres.Dump(ctx, c.p.Native, &bytes.Buffer{})} {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled control", err)
			}
		}
		for _, query := range []func(context.Context, ...any) (*sql.Rows, error){
			func(ctx context.Context, args ...any) (*sql.Rows, error) {
				return c.conn.QueryContext(ctx, ftoQuery, args...)
			},
			held.Statement.QueryContext,
		} {
			rows, err := query(ctx, "fto_table")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled read returned rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled read", err)
			}
		}
		prepared, err := c.conn.PrepareContext(ctx, ftoQuery)
		if prepared != nil {
			prepared.Close()
			t.Fatal("canceled prepare returned statement")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled prepare", err)
		}
		unchanged()
	})
}

func (c *ftoCase) routesOn(e fixture.Executor, want [][]any) {
	c.run(e, c.step(ftoQuery, want, "fto_table"))
	c.run(e, fixture.Prepared(c.step(ftoQuery, want, "fto_table")))
}
