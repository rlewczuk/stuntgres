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

func (c *fsvCase) identities(e fixture.Executor, want [][]any) {
	c.t.Helper()
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fsv_reader", 0))
	c.check(e, "fsv_text", want)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fsv_denied", 0))
	c.check(e, "fsv_text", nil)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.check(e, "fsv_text", want)
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fsv_inherit", 0))
	c.check(e, "fsv_text", want)
	c.run(e, fixture.Prepared(c.step(fsvQuery, want, "fsv_text")))
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}

func TestReferenceInfoForeignServersControls(t *testing.T) {
	fsvScenario(t, func(t *testing.T, c *fsvCase) {
		c.seed()
		bootstrap := c.owner
		c.exec("CREATE ROLE fsv_reader", "CREATE ROLE fsv_denied", "CREATE ROLE fsv_owner", "GRANT USAGE ON FOREIGN DATA WRAPPER fsv_wrapper TO fsv_owner", "ALTER SERVER fsv_text OWNER TO fsv_owner", "GRANT USAGE ON FOREIGN SERVER fsv_text TO fsv_reader", "CREATE USER MAPPING FOR PUBLIC SERVER fsv_text OPTIONS(password 'mapping-secret')", "CREATE FOREIGN TABLE public.fsv_control_table(id integer) SERVER fsv_text OPTIONS(remote 'table')")
		c.exec("CREATE ROLE fsv_inherit", "GRANT fsv_owner TO fsv_inherit WITH INHERIT TRUE", "CREATE SERVER fsv_null FOREIGN DATA WRAPPER fsv_wrapper")
		c.owner = "fsv_owner"
		want := c.seedRows()
		c.routes(fsvQuery, want, "fsv_text")
		c.identities(c.conn, want)
		if c.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE FOREIGN DATA WRAPPER") || !strings.Contains(dump.String(), "server-secret") {
			t.Fatal("missing foreign definitions/options", dump.String())
		}
		// Keep actual authorized rows across descriptor and ACL publication.
		c.exec("SET SESSION AUTHORIZATION fsv_reader")
		old, err := c.conn.QueryContext(c.p.Ctx, fsvQuery, "fsv_text")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		c.exec("RESET SESSION AUTHORIZATION", "ALTER SERVER fsv_text RENAME TO fsv_renamed", "REVOKE USAGE ON FOREIGN SERVER fsv_renamed FROM fsv_reader", "SET SESSION AUTHORIZATION fsv_reader")
		c.check(c.conn, "fsv_text", nil)
		c.check(c.conn, "fsv_renamed", nil)
		c.consume(old, want)
		c.exec("RESET SESSION AUTHORIZATION")
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		old, err = c.conn.QueryContext(c.p.Ctx, fsvQuery, "fsv_text")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		allQuery := "SELECT * FROM information_schema.foreign_servers ORDER BY foreign_server_name"
		allWant := [][]any{c.row("fsv_null", "fsv_wrapper", bootstrap, nil, nil), want[0]}
		retained, err := c.conn.QueryContext(c.p.Ctx, allQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.routes(fsvQuery, nil, "fsv_text")
		c.consume(old, want)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c.consume(retained, allWant)
		c.routes(fsvQuery, want, "fsv_text")
		c.identities(c.conn, want)
		connector, err := stuntgres.NewDriver().OpenConnector("fsv-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		fresh, recorder := fixture.InsertSelectRecordedPool(t, connector)
		freshNative := sql.OpenDB(connector)
		defer freshNative.Close()
		independent := *c
		independent.b.Recorder = recorder
		for _, role := range []string{"fsv_reader", "fsv_denied", "fsv_owner", "fsv_inherit"} {
			independent.run(fresh, fixture.Exec("CREATE ROLE "+role, 0))
		}
		independent.run(fresh, fixture.Exec("GRANT fsv_owner TO fsv_inherit WITH INHERIT TRUE", 0))
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
		c.exec("CREATE TABLE public.fsv_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.fsv_sentinel VALUES(91)", 1))
		prefix := dump.String() + "\nCREATE TABLE public.fsv_staged(v integer); INSERT INTO public.fsv_staged VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			c.routes(fsvQuery, want, "fsv_text")
			c.identities(c.conn, want)
			c.run(c.conn, fixture.Select("SELECT v FROM public.fsv_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='fsv_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		for _, err := range []error{stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())), stuntgres.Reset(ctx, c.p.Native), stuntgres.Dump(ctx, c.p.Native, &bytes.Buffer{})} {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled control", err)
			}
		}
		unchanged()
		fsvRegistration(t, c)
	})
}

func (c *fsvCase) routesOn(e fixture.Executor, want [][]any) {
	c.run(e, c.step(fsvQuery, want, "fsv_text"))
	c.run(e, fixture.Prepared(c.step(fsvQuery, want, "fsv_text")))
}

// A registration is executable capability, not an actual SQL wrapper.
func fsvRegistration(t *testing.T, c *fsvCase) {
	t.Helper()
	d := stuntgres.NewDriver()
	tripwire := fsvTripwire{t: t}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fsv_handler"}, Wrapper: tripwire}); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector("fsv-registration")
	if err != nil {
		t.Fatal(err)
	}
	db, recorder := fixture.InsertSelectRecordedPool(t, connector)
	conn, err := db.Conn(c.p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	registered := *c
	registered.b.Recorder = recorder
	registered.conn = conn
	if err := conn.QueryRowContext(c.p.Ctx, "SELECT current_database(),current_user").Scan(&registered.database, &registered.owner); err != nil {
		t.Fatal(err)
	}
	registered.routes("SELECT * FROM information_schema.foreign_servers ORDER BY foreign_server_name", nil)
	registered.exec("CREATE FOREIGN DATA WRAPPER fsv_wrapper HANDLER public.fsv_handler NO VALIDATOR")
	registered.routes(fsvQuery, nil, "fsv_text")
	registered.exec("CREATE SERVER fsv_text TYPE 'é=🙂' VERSION 'before' FOREIGN DATA WRAPPER fsv_wrapper", "CREATE FOREIGN TABLE public.fsv_probe(id integer) SERVER fsv_text")
	registered.routes(fsvQuery, registered.seedRows(), "fsv_text")
}

type fsvTripwire struct{ t *testing.T }

func (w fsvTripwire) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	w.t.Fatal("metadata invoked GetForeignRelSize")
	return stuntgres.ForeignRelSize{}, errors.New("unexpected foreign size callback")
}
func (w fsvTripwire) GetForeignPaths(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	w.t.Fatal("metadata invoked GetForeignPaths")
	return nil, errors.New("unexpected foreign paths callback")
}
func (w fsvTripwire) GetForeignPlan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	w.t.Fatal("metadata invoked GetForeignPlan")
	return stuntgres.ForeignPlan{}, errors.New("unexpected foreign plan callback")
}
func (w fsvTripwire) BeginForeignScan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	w.t.Fatal("metadata invoked BeginForeignScan")
	return nil, errors.New("unexpected foreign scan callback")
}
