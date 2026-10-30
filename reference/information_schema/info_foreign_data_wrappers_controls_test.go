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

func (c *fdwvCase) identities(e fixture.Executor, want [][]any) {
	c.t.Helper()
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fdwview_reader", 0))
	c.check(e, "fdwview_wrapper", want)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION fdwview_denied", 0))
	c.check(e, "fdwview_wrapper", nil)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.check(e, "fdwview_wrapper", want)
}

func TestReferenceInfoForeignDataWrappersControls(t *testing.T) {
	fdwvScenario(t, func(t *testing.T, c *fdwvCase) {
		c.seed()
		c.exec("CREATE ROLE fdwview_reader", "CREATE ROLE fdwview_denied", "CREATE ROLE fdwview_owner SUPERUSER", "ALTER FOREIGN DATA WRAPPER fdwview_wrapper OWNER TO fdwview_owner", "GRANT USAGE ON FOREIGN DATA WRAPPER fdwview_wrapper TO fdwview_reader")
		c.owner = "fdwview_owner"
		want := c.seedRows()
		c.routes(fdwvQuery, want, "fdwview_wrapper")
		c.identities(c.conn, want)
		if c.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE FOREIGN DATA WRAPPER") || !strings.Contains(dump.String(), "private=value") {
			t.Fatal("missing wrapper options", dump.String())
		}
		// Keep actual authorized rows across descriptor and ACL publication.
		c.exec("SET SESSION AUTHORIZATION fdwview_reader")
		old, err := c.conn.QueryContext(c.p.Ctx, fdwvQuery, "fdwview_wrapper")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		c.exec("RESET SESSION AUTHORIZATION", "ALTER FOREIGN DATA WRAPPER fdwview_wrapper RENAME TO fdwview_renamed", "REVOKE USAGE ON FOREIGN DATA WRAPPER fdwview_renamed FROM fdwview_reader", "SET SESSION AUTHORIZATION fdwview_reader")
		c.check(c.conn, "fdwview_wrapper", nil)
		c.check(c.conn, "fdwview_renamed", nil)
		c.consume(old, want)
		c.exec("RESET SESSION AUTHORIZATION")
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		old, err = c.conn.QueryContext(c.p.Ctx, fdwvQuery, "fdwview_wrapper")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.routes(fdwvQuery, nil, "fdwview_wrapper")
		c.consume(old, want)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c.routes(fdwvQuery, want, "fdwview_wrapper")
		c.identities(c.conn, want)
		connector, err := stuntgres.NewDriver().OpenConnector("fdwv-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		fresh, recorder := fixture.InsertSelectRecordedPool(t, connector)
		freshNative := sql.OpenDB(connector)
		defer freshNative.Close()
		independent := *c
		independent.b.Recorder = recorder
		for _, role := range []string{"fdwview_reader", "fdwview_denied", "fdwview_owner SUPERUSER"} {
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
		c.exec("CREATE TABLE public.fdwview_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.fdwview_sentinel VALUES(91)", 1))
		prefix := dump.String() + "\nCREATE TABLE public.fdwview_staged(v integer); INSERT INTO public.fdwview_staged VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			c.routes(fdwvQuery, want, "fdwview_wrapper")
			c.identities(c.conn, want)
			c.run(c.conn, fixture.Select("SELECT v FROM public.fdwview_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='fdwview_staged'", []string{"relname"}, []string{"NAME"}, nil))
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
		fdwvRegistration(t, c)
	})
}

func (c *fdwvCase) routesOn(e fixture.Executor, want [][]any) {
	c.run(e, c.step(fdwvQuery, want, "fdwview_wrapper"))
	c.run(e, fixture.Prepared(c.step(fdwvQuery, want, "fdwview_wrapper")))
}

// A registration is executable capability, not an actual SQL wrapper.
func fdwvRegistration(t *testing.T, c *fdwvCase) {
	t.Helper()
	d := stuntgres.NewDriver()
	tripwire := fdwvTripwire{t: t}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdwview_handler"}, Wrapper: tripwire}); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector("fdwview-registration")
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
	registered.routes("SELECT * FROM information_schema.foreign_data_wrappers ORDER BY foreign_data_wrapper_name", nil)
	registered.exec("CREATE FOREIGN DATA WRAPPER fdwview_wrapper HANDLER public.fdwview_handler NO VALIDATOR")
	registered.routes(fdwvQuery, registered.seedRows(), "fdwview_wrapper")
	registered.exec("CREATE SERVER fdwview_server FOREIGN DATA WRAPPER fdwview_wrapper", "CREATE FOREIGN TABLE public.fdwview_probe(id integer) SERVER fdwview_server")
	registered.routes(fdwvQuery, registered.seedRows(), "fdwview_wrapper")
}

type fdwvTripwire struct{ t *testing.T }

func (w fdwvTripwire) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	w.t.Fatal("metadata invoked GetForeignRelSize")
	return stuntgres.ForeignRelSize{}, errors.New("unexpected foreign size callback")
}
func (w fdwvTripwire) GetForeignPaths(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	w.t.Fatal("metadata invoked GetForeignPaths")
	return nil, errors.New("unexpected foreign paths callback")
}
func (w fdwvTripwire) GetForeignPlan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	w.t.Fatal("metadata invoked GetForeignPlan")
	return stuntgres.ForeignPlan{}, errors.New("unexpected foreign plan callback")
}
func (w fdwvTripwire) BeginForeignScan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	w.t.Fatal("metadata invoked BeginForeignScan")
	return nil, errors.New("unexpected foreign scan callback")
}
