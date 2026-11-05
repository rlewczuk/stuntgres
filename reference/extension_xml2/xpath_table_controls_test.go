package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xt_control_reader", "xt_control_owner"})
	xml2Case(t, xtControls)
}

func xtControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	xtExec(t, p, b, c,
		"CREATE ROLE xt_control_reader", "CREATE ROLE xt_control_owner",
		"GRANT USAGE ON SCHEMA public TO xt_control_reader",
		"CREATE TABLE xt_control(id integer,doc text)",
		"CREATE DOMAIN xt_control_number AS integer")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xt_control VALUES(1,'<doc><a>10</a><a>20</a><b>7</b></doc>')", 1))
	xml2Install(t, p, b, c)
	xtExec(t, p, b, c,
		"CREATE VIEW xt_control_view AS SELECT id,a,b FROM xpath_table('id','doc','xt_control','/doc/a|/doc/b','true') AS x(id integer,a xt_control_number,b integer)",
		"GRANT SELECT ON xt_control,xt_control_view TO xt_control_reader")
	// Continuity across catalog lifetimes uses stable builtin result codecs;
	// the stored source/view still owns its genuine domain member definition.
	q := "SELECT id,a::integer AS a,b FROM xt_control_view ORDER BY id,a"
	want := fixture.Select(q, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(7)}, {int64(1), int64(20), nil}})
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	xml2Both(t, p, b, c, want)
	if !b.PG {
		if !t.Run("installed_replacement", func(t *testing.T) {
			xtReplacement(t, p, b, c, h, want)
		}) {
			t.FailNow()
		}
	}
	// A mere domain label change in the same lifetime preserves its codec.
	rawWant := want
	rawWant.Query = "SELECT id,a,b FROM xt_control_view ORDER BY id,a"
	rawStatement, err := c.PrepareContext(p.Ctx, rawWant.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rawStatement.Close()
	rawHeld := fixture.HeldStatement{Executor: c, Statement: rawStatement, SQL: rawWant.Query}
	fixture.RunStep(t, p.Ctx, rawHeld, b.Recorder, rawWant)
	if !t.Run("same_lifetime_domain_rename", func(t *testing.T) {
		xtExec(t, p, b, c,
			"ALTER FUNCTION xpath_table(text,text,text,text,text) RENAME TO xt_current",
			"ALTER DOMAIN xt_control_number RENAME TO xt_current_number",
			"ALTER EXTENSION xml2 DROP FUNCTION xt_current(text,text,text,text,text)",
			"ALTER FUNCTION xt_current(text,text,text,text,text) OWNER TO xt_control_owner",
			"REVOKE EXECUTE ON FUNCTION xt_current(text,text,text,text,text) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION xt_current(text,text,text,text,text) TO xt_control_reader")
		xml2Both(t, p, b, c, want)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, rawHeld, b.Recorder, rawWant)
	}) {
		t.FailNow()
	}
	xtExec(t, p, b, c, "SET ROLE xt_control_reader")
	defer c.ExecContext(context.Background(), "RESET ROLE")
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	xtExec(t, p, b, c, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION xt_current(text,text,text,text,text) FROM xt_control_reader", "SET ROLE xt_control_reader")
	denied := fixture.ErrorDiagnostic(q, xml2Error("42501", "permission denied for function xt_current"))
	denied.SelectRows, denied.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, h, b.Recorder, denied)
	xtExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION xt_current(text,text,text,text,text) TO xt_control_reader")
	if !b.PG {
		if !t.Run("detached_replacement", func(t *testing.T) {
			xtReplacement(t, p, b, c, h, want)
		}) {
			t.FailNow()
		}
	}
	// Only xpath_table is detached. No consumer here depends on the attached
	// scalars, so removing the header must preserve this source/view.
	xtExec(t, p, b, c, "DROP EXTENSION xml2")
	xml2Both(t, p, b, c, want)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	if !b.PG {
		if !t.Run("headerless_replacement", func(t *testing.T) {
			xtReplacement(t, p, b, c, h, want)
		}) {
			t.FailNow()
		}
	}
	xml2Both(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='xml2'", []string{"extname"}, []string{"NAME"}, nil))
}

func xtReplacement(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, h fixture.HeldStatement, want fixture.Step) {
	t.Helper()
	rawWant := want
	rawWant.Query = "SELECT id,a,b FROM xt_control_view ORDER BY id,a"
	rawStatement, err := c.PrepareContext(p.Ctx, rawWant.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rawStatement.Close()
	rawHeld := fixture.HeldStatement{Executor: c, Statement: rawStatement, SQL: rawWant.Query}
	fixture.RunStep(t, p.Ctx, rawHeld, b.Recorder, rawWant)
	old, err := c.QueryContext(p.Ctx, rawWant.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	xtRetained(t, old)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	// A replaced catalog domain is not the accepted held result generation,
	// even when its restored name and numeric coordinates happen to agree.
	replaced := fixture.ErrorDiagnostic(rawWant.Query, xml2Error("0A000", "cached plan must not change result type"))
	replaced.SelectRows, replaced.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, rawHeld, b.Recorder, replaced)
	xml2Both(t, p, b, c, rawWant)
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	xml2Both(t, p, b, c, want)
	acceptedStatement, err := c.PrepareContext(p.Ctx, rawWant.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer acceptedStatement.Close()
	acceptedDomain := fixture.HeldStatement{Executor: c, Statement: acceptedStatement, SQL: rawWant.Query}
	fixture.RunStep(t, p.Ctx, acceptedDomain, b.Recorder, rawWant)
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	readFailure := errors.New("xpath_table restore reader")
	for _, mode := range []string{"sql", "reader", "precanceled", "late-cancel"} {
		ctx, cancel := context.WithCancel(p.Ctx)
		var input io.Reader = strings.NewReader(dump + "\nINVALID SQL;")
		var ordinary error
		switch mode {
		case "reader":
			input = io.MultiReader(strings.NewReader(dump), &fixture.ReferenceRangeErrorReader{Err: readFailure})
			ordinary = readFailure
		case "precanceled":
			cancel()
			input, ordinary = strings.NewReader(dump), context.Canceled
		case "late-cancel":
			input = io.MultiReader(strings.NewReader(dump), &xml2CancelReader{Reader: strings.NewReader("\n"), cancel: cancel})
			ordinary = context.Canceled
		}
		err := stuntgres.Restore(ctx, p.Native, input)
		cancel()
		if err == nil || ordinary != nil && !errors.Is(err, ordinary) {
			t.Fatalf("failed %s replacement: %v", mode, err)
		}
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("rejected replacement mutated accepted catalog/data")
		}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, acceptedDomain, b.Recorder, rawWant)
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("xpath_table_destination")
	if err != nil {
		t.Fatal(err)
	}
	control := sql.OpenDB(connector)
	defer control.Close()
	pool, recorder := fixture.RecordedNativePool(t, connector)
	dst, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	destination := fixture.Backend{Name: "destination", DB: pool, Recorder: recorder}
	xtExec(t, p, destination, dst,
		"CREATE ROLE xt_control_reader", "CREATE ROLE xt_control_owner",
		"CREATE TABLE displaced(id integer)", "CREATE DOMAIN displaced_number AS text")
	if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	xml2Both(t, p, destination, dst, want)
	xtExec(t, p, destination, dst, "SET ROLE xt_control_reader")
	defer dst.ExecContext(context.Background(), "RESET ROLE")
	xml2Both(t, p, destination, dst, want)
	xtExec(t, p, destination, dst, "RESET ROLE")
}

func xtRetained(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id", "a", "b"}) {
		t.Fatal("retained columns", columns, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != 3 {
		t.Fatal("retained metadata", metadata, err)
	}
	for _, column := range metadata {
		if column.DatabaseTypeName() != "INT4" {
			t.Fatal("retired member type", column.DatabaseTypeName())
		}
	}
	for _, want := range [][]any{{int64(1), int64(10), int64(7)}, {int64(1), int64(20), nil}} {
		if !rows.Next() {
			t.Fatal("retired row", rows.Err())
		}
		var id, a, b any
		if err := rows.Scan(&id, &a, &b); err != nil || !reflect.DeepEqual([]any{fixture.Normalize(id), fixture.Normalize(a), fixture.Normalize(b)}, want) {
			t.Fatal("retired values", id, a, b, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retired tail", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
