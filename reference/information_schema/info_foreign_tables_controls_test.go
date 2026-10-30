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

func (c *iftCase) identities(e fixture.Executor, want [][]any) {
	c.t.Helper()
	for _, role := range []string{"ift_owner", "ift_inherit", "ift_column"} {
		c.run(e, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		c.run(e, c.step(iftQuery, want, "ift_table"))
		c.run(e, fixture.Prepared(c.step(iftQuery, want, "ift_table")))
		c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	}
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION ift_reader", 0))
	c.check(e, "ift_table", want)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.run(e, fixture.Exec("SET SESSION AUTHORIZATION ift_denied", 0))
	c.check(e, "ift_table", nil)
	c.run(e, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	c.check(e, "ift_table", want)
}

func TestReferenceInfoForeignTablesControls(t *testing.T) {
	iftScenario(t, func(c *iftCase) {
		c.seed()
		c.exec("CREATE ROLE ift_reader", "CREATE ROLE ift_denied", "CREATE ROLE ift_owner", "CREATE ROLE ift_inherit", "GRANT USAGE ON FOREIGN SERVER ift_server TO ift_owner", "GRANT CREATE ON SCHEMA ift_scope TO ift_owner", "ALTER FOREIGN TABLE ift_scope.ift_table OWNER TO ift_owner", "GRANT SELECT ON ift_scope.ift_table TO ift_reader", "GRANT ift_owner TO ift_inherit WITH INHERIT TRUE, SET TRUE", "CREATE ROLE ift_column", "GRANT SELECT(a) ON ift_scope.ift_table TO ift_column")
		want := c.seedRows()
		c.routes(iftQuery, want, "ift_table")
		c.identities(c.conn, want)
		if c.b.PG {
			return
		}
		iftRegistration(t, c)
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE FOREIGN TABLE") || !strings.Contains(dump.String(), "CREATE SERVER") || !strings.Contains(dump.String(), "CREATE FOREIGN DATA WRAPPER") || !strings.Contains(dump.String(), "visible=value") {
			t.Fatal("missing wrapper options", dump.String())
		}
		// Keep actual authorized rows across descriptor and ACL publication.
		c.exec("SET SESSION AUTHORIZATION ift_reader")
		old, err := c.conn.QueryContext(c.p.Ctx, iftQuery, "ift_table")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		c.exec("RESET SESSION AUTHORIZATION", "ALTER FOREIGN TABLE ift_scope.ift_table RENAME TO ift_renamed", "REVOKE SELECT ON ift_scope.ift_renamed FROM ift_reader", "SET SESSION AUTHORIZATION ift_reader")
		c.check(c.conn, "ift_table", nil)
		c.check(c.conn, "ift_renamed", nil)
		c.consume(old, want)
		c.exec("RESET SESSION AUTHORIZATION")
		c.routes(iftQuery, [][]any{c.row("ift_scope", "ift_renamed", "ift_server")}, "ift_renamed")
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		old, err = c.conn.QueryContext(c.p.Ctx, iftQuery, "ift_table")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.routes(iftQuery, nil, "ift_table")
		c.consume(old, want)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c.routes(iftQuery, want, "ift_table")
		c.identities(c.conn, want)
		connector, err := stuntgres.NewDriver().OpenConnector("ift-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		fresh, recorder := fixture.InsertSelectRecordedPool(t, connector)
		freshNative := sql.OpenDB(connector)
		defer freshNative.Close()
		independent := *c
		independent.b.Recorder = recorder
		for _, role := range []string{"ift_reader", "ift_denied", "ift_owner", "ift_inherit", "ift_column"} {
			independent.run(fresh, fixture.Exec("CREATE ROLE "+role, 0))
		}
		independent.run(fresh, fixture.Exec("GRANT ift_owner TO ift_inherit WITH INHERIT TRUE, SET TRUE", 0))
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
		if independent.database == c.database {
			t.Fatal("independent destination must distinguish captured database values")
		}
		independent.routesOn(destination, independent.seedRows())
		independent.identities(destination, independent.seedRows())
		c.exec("CREATE TABLE public.ift_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.ift_sentinel VALUES(91)", 1))
		prefix := dump.String() + "\nCREATE TABLE public.ift_staged(v integer); INSERT INTO public.ift_staged VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			c.routes(iftQuery, want, "ift_table")
			c.identities(c.conn, want)
			c.run(c.conn, fixture.Select("SELECT v FROM public.ift_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='ift_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		held := c.hold(c.conn, iftQuery)
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		for _, err := range []error{stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())), stuntgres.Reset(ctx, c.p.Native), stuntgres.Dump(ctx, c.p.Native, &bytes.Buffer{})} {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled control", err)
			}
		}
		for _, query := range []func(context.Context, ...any) (*sql.Rows, error){
			func(ctx context.Context, args ...any) (*sql.Rows, error) {
				return c.conn.QueryContext(ctx, iftQuery, args...)
			},
			held.Statement.QueryContext,
		} {
			rows, err := query(ctx, "ift_table")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled read returned rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled read", err)
			}
		}
		prepared, err := c.conn.PrepareContext(ctx, iftQuery)
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

func (c *iftCase) routesOn(e fixture.Executor, want [][]any) {
	c.run(e, c.step(iftQuery, want, "ift_table"))
	c.run(e, fixture.Prepared(c.step(iftQuery, want, "ift_table")))
}

func iftRegistration(t *testing.T, c *iftCase) {
	t.Helper()
	d := stuntgres.NewDriver()
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "ift_handler"}, Wrapper: fsvTripwire{t: t}}); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector("ift-registration")
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
	registered.b.Recorder, registered.conn = recorder, conn
	if err := conn.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&registered.database); err != nil {
		t.Fatal(err)
	}
	registered.routes("SELECT * FROM information_schema.foreign_tables ORDER BY foreign_table_schema,foreign_table_name", nil)
	registered.exec("CREATE FOREIGN DATA WRAPPER ift_wrapper HANDLER public.ift_handler NO VALIDATOR", "CREATE SERVER ift_server FOREIGN DATA WRAPPER ift_wrapper", "CREATE SCHEMA ift_scope", "CREATE FOREIGN TABLE ift_scope.ift_table(a integer) SERVER ift_server")
	registered.routes(iftQuery, registered.seedRows(), "ift_table")
	registered.run(conn, fixture.Select("SELECT column_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='foreign_tables' ORDER BY ordinal_position", []string{"column_name"}, []string{"NAME"}, [][]any{{"foreign_table_catalog"}, {"foreign_table_schema"}, {"foreign_table_name"}, {"foreign_server_catalog"}, {"foreign_server_name"}}))
	native := sql.OpenDB(connector)
	defer native.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(c.p.Ctx, native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(c.p.Ctx, native); err != nil {
		t.Fatal(err)
	}
	registered.routes(iftQuery, nil, "ift_table")
	if err := stuntgres.Restore(c.p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	registered.routes(iftQuery, registered.seedRows(), "ift_table")
}
