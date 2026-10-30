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

func (c *fdwoCase) identities(e fixture.Executor, want [][]any) {
	c.t.Helper()
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fdwo_reader", 0))
	c.check(e, "fdwo_wrapper", want)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fdwo_denied", 0))
	c.check(e, "fdwo_wrapper", nil)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.check(e, "fdwo_wrapper", want)
}

func TestReferenceInfoForeignDataWrapperOptionsControls(t *testing.T) {
	fdwoScenario(t, func(c *fdwoCase) {
		c.seed()
		c.exec("CREATE ROLE fdwo_reader", "CREATE ROLE fdwo_denied", "GRANT USAGE ON FOREIGN DATA WRAPPER fdwo_wrapper TO fdwo_reader")
		want := c.seedRows()
		c.routes(fdwoQuery, want, "fdwo_wrapper")
		c.identities(c.conn, want)
		if c.b.PG { return }
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil { t.Fatal(err) }
		if !strings.Contains(dump.String(), "CREATE FOREIGN DATA WRAPPER") || !strings.Contains(dump.String(), "private=value") { t.Fatal("missing wrapper options", dump.String()) }
		// Keep actual authorized rows across descriptor and ACL publication.
		c.exec("SET SESSION AUTHORIZATION fdwo_reader")
		old, err := c.conn.QueryContext(c.p.Ctx, fdwoQuery, "fdwo_wrapper")
		if err != nil { t.Fatal(err) }
		defer old.Close()
		c.exec("RESET SESSION AUTHORIZATION", "ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(SET remote 'changed')", "REVOKE USAGE ON FOREIGN DATA WRAPPER fdwo_wrapper FROM fdwo_reader", "SET SESSION AUTHORIZATION fdwo_reader")
		c.check(c.conn, "fdwo_wrapper", nil)
		c.consume(old, want)
		c.exec("RESET SESSION AUTHORIZATION")
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		old, err = c.conn.QueryContext(c.p.Ctx, fdwoQuery, "fdwo_wrapper")
		if err != nil { t.Fatal(err) }
		defer old.Close()
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil { t.Fatal(err) }
		c.routes(fdwoQuery, nil, "fdwo_wrapper")
		c.consume(old, want)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		c.routes(fdwoQuery, want, "fdwo_wrapper")
		c.identities(c.conn, want)
		connector, err := stuntgres.NewDriver().OpenConnector("fdwo-independent-controls")
		if err != nil { t.Fatal(err) }
		fresh, recorder := fixture.InsertSelectRecordedPool(t, connector)
		freshNative := sql.OpenDB(connector)
		defer freshNative.Close()
		independent := *c
		independent.b.Recorder = recorder
		for _, role := range []string{"fdwo_reader", "fdwo_denied"} { independent.run(fresh, fixture.Exec("CREATE ROLE "+role, 0)) }
		if err := stuntgres.Restore(c.p.Ctx, freshNative, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		destination, err := fresh.Conn(c.p.Ctx)
		if err != nil { t.Fatal(err) }
		defer destination.Close()
		if err := destination.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&independent.database); err != nil { t.Fatal(err) }
		independent.routesOn(destination, independent.seedRows())
		independent.identities(destination, independent.seedRows())
		c.exec("CREATE TABLE public.fdwo_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.fdwo_sentinel VALUES(91)", 1))
		prefix := dump.String()+"\nCREATE TABLE public.fdwo_staged(v integer); INSERT INTO public.fdwo_staged VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix)+1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			c.routes(fdwoQuery, want, "fdwo_wrapper")
			c.identities(c.conn, want)
			c.run(c.conn, fixture.Select("SELECT v FROM public.fdwo_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='fdwo_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		for _, err := range []error{stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())), stuntgres.Reset(ctx, c.p.Native), stuntgres.Dump(ctx, c.p.Native, &bytes.Buffer{})} {
			if !errors.Is(err, context.Canceled) { t.Fatal("canceled control", err) }
		}
		unchanged()
	})
}

func (c *fdwoCase) routesOn(e fixture.Executor, want [][]any) {
	c.run(e, c.step(fdwoQuery, want, "fdwo_wrapper"))
	c.run(e, fixture.Prepared(c.step(fdwoQuery, want, "fdwo_wrapper")))
}
