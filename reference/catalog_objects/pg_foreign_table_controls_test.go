package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These are actual opened, unconsumed native rows, not reconstructed snapshots.
func (c *pgftCase) retain(oid int64) (*sql.Rows, []*sql.ColumnType) {
	c.t.Helper()
	rows, err := c.conn.QueryContext(c.p.Ctx, pgftQuery, oid); if err != nil { c.t.Fatal(err) }
	c.t.Cleanup(func() { rows.Close() })
	columns, err := rows.ColumnTypes(); if err != nil { c.t.Fatal(err) }
	pgftMetadata(c.t, columns, false)
	return rows, columns
}
func (c *pgftCase) consume(rows *sql.Rows, columns []*sql.ColumnType, want []any) {
	c.t.Helper()
	if !rows.Next() { c.t.Fatal("lost retained row", rows.Err()) }
	got := make([]any, 3)
	if err := rows.Scan(&got[0], &got[1], &got[2]); err != nil { c.t.Fatal(err) }
	for i, value := range got { fixture.OidReferenceValue(c.t, false, pgftTypes[i], value) }
	if !reflect.DeepEqual(got, want) || rows.Next() || rows.Err() != nil { c.t.Fatal("retained row", got, want, rows.Err()) }
	if err := rows.Close(); err != nil { c.t.Fatal(err) }; pgftMetadata(c.t, columns, false)
}

func TestReferencePGForeignTableControls(t *testing.T) {
	pgftScenario(t, func(c *pgftCase) {
		t := c.t
		// Dump omits removed source objects. Restore stages a fresh catalog, so
		// advancing the destination before Restore cannot distinguish identities.
		// Source-only holes make the actual relation/server remapping observable
		// without prescribing either allocator's generated OIDs.
		for i := range 16 {
			name := fmt.Sprintf("public.pgft_source_spacer_%d", i)
			c.exec("CREATE TABLE "+name+"(v integer)", "DROP TABLE "+name)
		}
		c.seed(); c.identity()
		a, server := c.oid("pgft_a", "item"), c.server("pgft_server")
		c.options(c.conn, a, pgftOptions)
		if c.b.PG { return } // No PostgreSQL equivalents of native fixture replacement.
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil { t.Fatal(err) }
		for _, text := range []string{"CREATE FOREIGN DATA WRAPPER", "CREATE SERVER", "CREATE FOREIGN TABLE", "private=value", "scope", "column", "wrapper", "mapping"} {
			if !strings.Contains(dump.String(), text) { t.Fatal("missing logical definition", text, dump.String()) }
		}
		beforeAlter, alterColumns := c.retain(a)
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'changed')")
		c.options(c.conn, a, []string{"remote=changed", "empty=", "unicode=é🙂"})
		beforeReset, resetColumns := c.retain(a)
		held := c.hold(c.conn, pgftQuery)
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil { t.Fatal(err) }
		emptyPrepared := c.hold(c.conn, pgftQuery)
		for _, e := range []fixture.Executor{c.conn, held, emptyPrepared} { c.raw(e, a, nil); c.shape(e, a, false) }
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		restored, restoredServer := c.oid("pgft_a", "item"), c.server("pgft_server")
		c.raw(held, restored, [][]any{{restored, restoredServer, pgftArray}})
		c.identity(); c.options(c.conn, restored, pgftOptions)
		beforeRestore, restoreColumns := c.retain(restored)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		c.consume(beforeAlter, alterColumns, []any{a, server, pgftArray})
		c.consume(beforeReset, resetColumns, []any{a, server, "{remote=changed,empty=,unicode=é🙂}"})
		c.consume(beforeRestore, restoreColumns, []any{restored, restoredServer, pgftArray})
		var redump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("deterministic redump", err, dump.String(), redump.String()) }
		c.both(c.conn, fixture.Select("SELECT attfdwoptions[1] AS option FROM pg_catalog.pg_attribute WHERE attrelid=$1 AND attnum=1", []string{"option"}, []string{"TEXT"}, [][]any{{"scope=column"}}, c.oid("pgft_a", "item")))
		pgftDestination(c, dump.Bytes(), a, server)
		c.exec("CREATE TABLE public.pgft_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.pgft_sentinel VALUES(91)", 1))
		prefix := dump.String()+"\nCREATE TABLE public.pgft_staged(v integer); INSERT INTO public.pgft_staged VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix)+1)
		err := stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			c.identity(); c.options(c.conn, c.oid("pgft_a", "item"), pgftOptions)
			c.both(c.conn, fixture.Select("SELECT v FROM public.pgft_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.both(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='pgft_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		ctx, cancel := context.WithCancel(c.p.Ctx); cancel()
		for _, err := range []error{stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())), stuntgres.Reset(ctx, c.p.Native), stuntgres.Dump(ctx, c.p.Native, &bytes.Buffer{})} {
			pgftCanceled(t, err)
		}
		for _, e := range []fixture.Executor{c.conn, held} {
			rows, err := e.QueryContext(ctx, pgftQuery, c.oid("pgft_a", "item"))
			if rows != nil { rows.Close(); t.Fatal("canceled query returned rows") }; pgftCanceled(t, err)
		}
		statement, err := c.conn.PrepareContext(ctx, pgftQuery)
		if statement != nil { statement.Close(); t.Fatal("canceled prepare returned handle") }; pgftCanceled(t, err)
		unchanged()
		pgftRegistration(c)
	})
}

func pgftCanceled(t *testing.T, err error) {
	t.Helper(); var state interface{ SQLState() string }
	if !errors.Is(err, context.Canceled) || errors.As(err, &state) { t.Fatal("ordinary cancellation", err) }
}

func pgftDestination(c *pgftCase, dump []byte, sourceTable, sourceServer int64) {
	c.t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector("pgft-independent-destination"); if err != nil { c.t.Fatal(err) }
	db, recorder := fixture.InsertSelectRecordedPool(c.t, connector)
	native := sql.OpenDB(connector); defer native.Close()
	conn, err := db.Conn(c.p.Ctx); if err != nil { c.t.Fatal(err) }; defer conn.Close()
	destination := *c; destination.conn, destination.b.Recorder, destination.statements = conn, recorder, nil
	if err := stuntgres.Restore(c.p.Ctx, native, bytes.NewReader(dump)); err != nil { c.t.Fatal(err) }
	table, server := destination.oid("pgft_a", "item"), destination.server("pgft_server")
	if table == sourceTable || server == sourceServer { c.t.Fatal("source-history fixture did not distinguish restored relation/server identities", table, server) }
	destination.identity(); destination.raw(conn, table, [][]any{{table, server, pgftArray}}); destination.options(conn, table, pgftOptions)
	destination.options(conn, destination.oid("pgft_b", "item"), nil)
	destination.both(conn, fixture.Select("SELECT attfdwoptions[1] AS option FROM pg_catalog.pg_attribute WHERE attrelid=$1 AND attnum=1", []string{"option"}, []string{"TEXT"}, [][]any{{"scope=column"}}, table))
}

func pgftRegistration(c *pgftCase) {
	c.t.Helper()
	driver := stuntgres.NewDriver()
	if err := driver.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "pgft_handler"}, Wrapper: pgfdwTripwire{t: c.t}}); err != nil { c.t.Fatal(err) }
	connector, err := driver.OpenConnector("pgft-registration"); if err != nil { c.t.Fatal(err) }
	db, recorder := fixture.InsertSelectRecordedPool(c.t, connector)
	native := sql.OpenDB(connector); defer native.Close()
	conn, err := db.Conn(c.p.Ctx); if err != nil { c.t.Fatal(err) }; defer conn.Close()
	registered := *c; registered.conn, registered.b.Recorder, registered.statements = conn, recorder, nil
	defer func() { for _, statement := range registered.statements { statement.Close() } }()
	step := fixture.Select("SELECT * FROM pg_catalog.pg_foreign_table", pgftColumns, pgftTypes, nil)
	fixture.OidReferenceRows(c.t, c.p.Ctx, registered.b, conn, step)
	fixture.OidReferenceRows(c.t, c.p.Ctx, registered.b, conn, fixture.Prepared(step))
	registered.exec("CREATE FOREIGN DATA WRAPPER pgft_wrapper HANDLER public.pgft_handler NO VALIDATOR", "CREATE SERVER pgft_server FOREIGN DATA WRAPPER pgft_wrapper", "CREATE SCHEMA pgft_a", "CREATE FOREIGN TABLE pgft_a.item(a integer) SERVER pgft_server OPTIONS(remote 'registered')")
	table, server := registered.oid("pgft_a", "item"), registered.server("pgft_server")
	step.Rows = [][]any{{table, server, "{remote=registered}"}}
	fixture.OidReferenceRows(c.t, c.p.Ctx, registered.b, conn, step)
	fixture.OidReferenceRows(c.t, c.p.Ctx, registered.b, conn, fixture.Prepared(step))
	held := registered.hold(conn, pgftQuery)
	registered.raw(held, table, step.Rows)
	var dump bytes.Buffer
	if err := stuntgres.Dump(c.p.Ctx, native, &dump); err != nil { c.t.Fatal(err) }
	if !strings.Contains(dump.String(), "CREATE FOREIGN TABLE") || !strings.Contains(dump.String(), "registered") { c.t.Fatal("logical registered table missing", dump.String()) }
}
