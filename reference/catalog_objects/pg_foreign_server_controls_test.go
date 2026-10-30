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

func TestReferencePGForeignServerControls(t *testing.T) {
	pgfsScenario(t, func(c *pgfsCase) {
		c.wrapper()
		c.exec(pgfsSeed, "CREATE FOREIGN TABLE public.pgfs_probe(id integer) SERVER pgfs_server")
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("SELECT * FROM public.pgfs_probe", fixture.Diagnostic{Code: "55000", Message: `foreign-data wrapper "pgfs_wrapper" has no handler`})
			step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
			c.run(c.conn, step)
		}
		if c.b.PG { return }
		c.exec("DROP FOREIGN TABLE public.pgfs_probe")
		oid, wrapper := c.oid("pgfs_server"), c.wrapperOID()
		old, err := c.conn.QueryContext(c.p.Ctx, pgfsQuery, "pgfs_server")
		if err != nil { c.t.Fatal(err) }
		defer old.Close()
		savedColumns, err := old.ColumnTypes()
		if err != nil { c.t.Fatal(err) }
		pgfsMetadata(c.t, savedColumns, false)
		c.exec("CREATE ROLE pgfs_reader", "CREATE ROLE pgfs_newowner", "GRANT USAGE ON FOREIGN DATA WRAPPER pgfs_wrapper TO pgfs_newowner", "ALTER SERVER pgfs_server OWNER TO pgfs_newowner", "GRANT USAGE ON FOREIGN SERVER pgfs_server TO pgfs_reader", "CREATE SERVER pgfs_empty FOREIGN DATA WRAPPER pgfs_wrapper")
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil { c.t.Fatal(err) }
		if !strings.Contains(dump.String(), "CREATE SERVER") || !strings.Contains(dump.String(), "private=value") { c.t.Fatal("server logical definition missing", dump.String()) }
		c.exec("ALTER SERVER pgfs_server VERSION NULL OPTIONS(SET password 'changed')", "ALTER SERVER pgfs_server RENAME TO pgfs_renamed")
		c.options(c.conn, "pgfs_renamed", []string{"password=changed", "empty=", "remote=é=🙂"})
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil { c.t.Fatal(err) }
		c.raw(c.conn, "pgfs_server", nil)
		c.raw(c.conn, "pgfs_renamed", nil)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil { c.t.Fatal(err) }
		c.options(c.conn, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
		var restoredOwner int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgfs_newowner'").Scan(&restoredOwner); err != nil { c.t.Fatal(err) }
		c.identity("pgfs_server", c.oid("pgfs_server"), restoredOwner, c.wrapperOID())
		c.both(c.conn, fixture.Select("SELECT srvtype,srvversion FROM pg_catalog.pg_foreign_server WHERE srvname='pgfs_server'", []string{"srvtype", "srvversion"}, []string{"TEXT", "TEXT"}, [][]any{{"", "18"}}))
		c.raw(c.conn, "pgfs_empty", [][]any{c.row("pgfs_empty", c.oid("pgfs_empty"), c.owner, c.wrapperOID(), nil, nil, nil, nil)})
		c.both(c.conn, fixture.Select("SELECT a.grantor=w.srvowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_server w CROSS JOIN LATERAL aclexplode(w.srvacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE w.srvname='pgfs_server' AND r.rolname='pgfs_reader'", []string{"owner_grantor", "privilege_type", "is_grantable"}, []string{"BOOL", "TEXT", "BOOL"}, [][]any{{true, "USAGE", false}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { c.t.Fatal("deterministic logical round trip", err, dump.String(), redump.String()) }
		// This is the actual cursor acquired before ALTER/Reset/Restore, not a
		// recomputed query. Its arrays, raw NULL ACL and old identity survive.
		if !old.Next() { c.t.Fatal("lost retained row", old.Err()) }
		values, destinations := make([]any, 8), make([]any, 8)
		for i := range values { destinations[i] = &values[i] }
		if err := old.Scan(destinations...); err != nil { c.t.Fatal(err) }
		want := []any{oid, "pgfs_server", c.owner, wrapper, "", "18", nil, "{password=private=value,empty=,remote=é=🙂}"}
		for i, value := range values { fixture.AssertRawValue(c.t, false, pgfsTypes[i], value) }
		if !reflect.DeepEqual(values, want) || old.Next() || old.Err() != nil { c.t.Fatal("retained metadata", values, old.Err()) }
		if err := old.Close(); err != nil { c.t.Fatal(err) }
		pgfsMetadata(c.t, savedColumns, false)
		c.exec("CREATE TABLE public.pgfs_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.pgfs_sentinel VALUES(91)", 1))
		prefix := dump.String() + "\nCREATE TABLE public.pgfs_staged(v integer); INSERT INTO public.pgfs_staged VALUES("
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(c.t, err, &diagnostic)
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) { c.t.Fatal("canceled restore", err) }
		c.options(c.conn, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.both(c.conn, fixture.Select("SELECT v FROM public.pgfs_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		c.both(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='pgfs_staged'", []string{"relname"}, []string{"NAME"}, nil))
		pgfsRegistration(c)
	})
}

func pgfsRegistration(c *pgfsCase) {
	c.t.Helper()
	driver := stuntgres.NewDriver()
	if err := driver.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "pgfs_handler"}, Wrapper: pgfdwTripwire{t: c.t}}); err != nil { c.t.Fatal(err) }
	connector, err := driver.OpenConnector("pgfs-registration")
	if err != nil { c.t.Fatal(err) }
	db, recorder := fixture.InsertSelectRecordedPool(c.t, connector)
	conn, err := db.Conn(c.p.Ctx)
	if err != nil { c.t.Fatal(err) }
	defer conn.Close()
	registered := *c
	registered.conn, registered.b.Recorder, registered.statements = conn, recorder, nil
	defer func() { for _, statement := range registered.statements { statement.Close() } }()
	if err := conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&registered.owner); err != nil { c.t.Fatal(err) }
	registered.raw(conn, "pgfs_server", nil)
	registered.both(conn, fixture.Select("SELECT srvname FROM pg_catalog.pg_foreign_server", []string{"srvname"}, []string{"NAME"}, nil))
	registered.exec("CREATE FOREIGN DATA WRAPPER pgfs_wrapper HANDLER public.pgfs_handler NO VALIDATOR", "CREATE SERVER pgfs_server FOREIGN DATA WRAPPER pgfs_wrapper OPTIONS(remote 'registered')", "CREATE FOREIGN TABLE public.pgfs_probe(id integer) SERVER pgfs_server")
	registered.both(conn, fixture.Select("SELECT srvname FROM pg_catalog.pg_foreign_server", []string{"srvname"}, []string{"NAME"}, [][]any{{"pgfs_server"}}))
	held := registered.hold(conn, pgfsQuery)
	oid, wrapper := registered.oid("pgfs_server"), registered.wrapperOID()
	registered.raw(held, "pgfs_server", [][]any{registered.row("pgfs_server", oid, registered.owner, wrapper, nil, nil, nil, "{remote=registered}")})
	registered.identity("pgfs_server", oid, registered.owner, wrapper)
	registered.options(conn, "pgfs_server", []string{"remote=registered"})
}
