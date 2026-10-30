package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGForeignDataWrapperControls(t *testing.T) {
	pgfdwScenario(t, func(c *pgfdwCase) {
		c.exec(pgfdwSeed, "CREATE SERVER pgfdw_server FOREIGN DATA WRAPPER pgfdw_wrapper", "CREATE FOREIGN TABLE public.pgfdw_probe(id integer) SERVER pgfdw_server")
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("SELECT * FROM public.pgfdw_probe", fixture.Diagnostic{Code: "55000", Message: `foreign-data wrapper "pgfdw_wrapper" has no handler`})
			step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
			c.run(c.conn, step)
		}
		if c.b.PG { return }
		c.exec("DROP SERVER pgfdw_server CASCADE")
		oid := c.oid("pgfdw_wrapper")
		old, err := c.conn.QueryContext(c.p.Ctx, pgfdwQuery, "pgfdw_wrapper")
		if err != nil { c.t.Fatal(err) }
		defer old.Close()
		savedColumns, err := old.ColumnTypes()
		if err != nil { c.t.Fatal(err) }
		pgfdwMetadata(c.t, savedColumns, false)
		c.exec("CREATE ROLE pgfdw_reader", "GRANT USAGE ON FOREIGN DATA WRAPPER pgfdw_wrapper TO pgfdw_reader")
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil { c.t.Fatal(err) }
		if !strings.Contains(dump.String(), "CREATE FOREIGN DATA WRAPPER") || !strings.Contains(dump.String(), "private=value") { c.t.Fatal("wrapper logical definition missing", dump.String()) }
		c.exec("ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(SET password 'changed')", "ALTER FOREIGN DATA WRAPPER pgfdw_wrapper RENAME TO pgfdw_renamed")
		c.options(c.conn, "pgfdw_renamed", []string{"password=changed", "empty=", "remote=é=🙂"})
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil { c.t.Fatal(err) }
		c.raw(c.conn, "pgfdw_wrapper", nil)
		c.raw(c.conn, "pgfdw_renamed", nil)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil { c.t.Fatal(err) }
		c.options(c.conn, "pgfdw_wrapper", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.identity("pgfdw_wrapper", c.oid("pgfdw_wrapper"), c.owner)
		c.both(c.conn, fixture.Select("SELECT a.grantor=w.fdwowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_data_wrapper w CROSS JOIN LATERAL aclexplode(w.fdwacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE w.fdwname='pgfdw_wrapper' AND r.rolname='pgfdw_reader'", []string{"owner_grantor", "privilege_type", "is_grantable"}, []string{"BOOL", "TEXT", "BOOL"}, [][]any{{true, "USAGE", false}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { c.t.Fatal("deterministic logical round trip", err, dump.String(), redump.String()) }
		// This is the actual cursor acquired before ALTER/Reset/Restore, not a
		// recomputed query. Its arrays, raw NULL ACL and old identity survive.
		if !old.Next() { c.t.Fatal("lost retained row", old.Err()) }
		values, destinations := make([]any, 7), make([]any, 7)
		for i := range values { destinations[i] = &values[i] }
		if err := old.Scan(destinations...); err != nil { c.t.Fatal(err) }
		want := []any{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}
		for i, value := range values { fixture.AssertRawValue(c.t, false, pgfdwTypes[i], value) }
		if !reflect.DeepEqual(values, want) || old.Next() || old.Err() != nil { c.t.Fatal("retained metadata", values, old.Err()) }
		if err := old.Close(); err != nil { c.t.Fatal(err) }
		pgfdwMetadata(c.t, savedColumns, false)
		c.exec("CREATE TABLE public.pgfdw_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.pgfdw_sentinel VALUES(91)", 1))
		prefix := dump.String() + "\nCREATE TABLE public.pgfdw_staged(v integer); INSERT INTO public.pgfdw_staged VALUES("
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(c.t, err, &diagnostic)
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) { c.t.Fatal("canceled restore", err) }
		c.options(c.conn, "pgfdw_wrapper", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.both(c.conn, fixture.Select("SELECT v FROM public.pgfdw_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		c.both(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='pgfdw_staged'", []string{"relname"}, []string{"NAME"}, nil))
		pgfdwRegistration(c)
	})
}

func pgfdwRegistration(c *pgfdwCase) {
	c.t.Helper()
	driver := stuntgres.NewDriver()
	if err := driver.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "pgfdw_handler"}, Wrapper: pgfdwTripwire{t: c.t}}); err != nil { c.t.Fatal(err) }
	connector, err := driver.OpenConnector("pgfdw-registration")
	if err != nil { c.t.Fatal(err) }
	db, recorder := fixture.InsertSelectRecordedPool(c.t, connector)
	conn, err := db.Conn(c.p.Ctx)
	if err != nil { c.t.Fatal(err) }
	defer conn.Close()
	registered := *c
	registered.conn, registered.b.Recorder, registered.statements = conn, recorder, nil
	defer func() { for _, statement := range registered.statements { statement.Close() } }()
	if err := conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&registered.owner); err != nil { c.t.Fatal(err) }
	registered.raw(conn, "pgfdw_wrapper", nil)
	registered.both(conn, fixture.Select("SELECT fdwname FROM pg_catalog.pg_foreign_data_wrapper", []string{"fdwname"}, []string{"NAME"}, nil))
	registered.exec("CREATE FOREIGN DATA WRAPPER pgfdw_wrapper HANDLER public.pgfdw_handler NO VALIDATOR OPTIONS(remote 'registered')", "CREATE SERVER pgfdw_server FOREIGN DATA WRAPPER pgfdw_wrapper", "CREATE FOREIGN TABLE public.pgfdw_probe(id integer) SERVER pgfdw_server")
	registered.both(conn, fixture.Select("SELECT fdwname FROM pg_catalog.pg_foreign_data_wrapper", []string{"fdwname"}, []string{"NAME"}, [][]any{{"pgfdw_wrapper"}}))
	registered.both(conn, fixture.Select("SELECT w.fdwhandler=p.oid AS actual_handler,w.fdwhandler>0 AS nonzero,w.fdwvalidator=0 AS no_validator FROM pg_catalog.pg_foreign_data_wrapper w JOIN pg_catalog.pg_proc p ON p.oid=w.fdwhandler WHERE w.fdwname='pgfdw_wrapper' AND p.proname='pgfdw_handler'", []string{"actual_handler", "nonzero", "no_validator"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	var handler int64
	if err := conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_proc WHERE proname='pgfdw_handler'").Scan(&handler); err != nil { c.t.Fatal(err) }
	held := registered.hold(conn, pgfdwQuery)
	registered.raw(held, "pgfdw_wrapper", [][]any{{registered.oid("pgfdw_wrapper"), "pgfdw_wrapper", registered.owner, handler, int64(0), nil, "{remote=registered}"}})
	registered.options(conn, "pgfdw_wrapper", []string{"remote=registered"})
}

type pgfdwTripwire struct{ t *testing.T }

func (w pgfdwTripwire) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	w.t.Fatal("metadata executed foreign size callback")
	return stuntgres.ForeignRelSize{}, errors.New("unexpected foreign size callback")
}
func (w pgfdwTripwire) GetForeignPaths(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	w.t.Fatal("metadata executed foreign path callback")
	return nil, errors.New("unexpected foreign path callback")
}
func (w pgfdwTripwire) GetForeignPlan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	w.t.Fatal("metadata executed foreign plan callback")
	return stuntgres.ForeignPlan{}, errors.New("unexpected foreign plan callback")
}
func (w pgfdwTripwire) BeginForeignScan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	w.t.Fatal("metadata executed foreign scan callback")
	return nil, errors.New("unexpected foreign scan callback")
}
