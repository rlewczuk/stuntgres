package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func daitchCount(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
}

func daitchInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var ext, ns, owner int64
	var version string
	var reloc bool
	var config, condition any
	err := c.QueryRowContext(p.Ctx, "SELECT oid,extnamespace,extowner,extversion,extrelocatable,extconfig,extcondition FROM pg_extension WHERE extname='fuzzystrmatch'").Scan(&ext, &ns, &owner, &version, &reloc, &config, &condition)
	if err != nil || ext <= 0 || ns <= 0 || owner <= 0 || version != "1.2" || !reloc || config != nil || condition != nil {
		t.Fatal("extension identity", ext, ns, owner, version, reloc, config, condition, err)
	}
	var proc, procNS, procOwner, nargs, result int64
	var volatility, parallel string
	var strict bool
	err = c.QueryRowContext(p.Ctx, "SELECT oid,pronamespace,proowner,pronargs,prorettype,provolatile,proisstrict,proparallel FROM pg_proc WHERE proname='daitch_mokotoff' AND pronamespace=$1", ns).Scan(&proc, &procNS, &procOwner, &nargs, &result, &volatility, &strict, &parallel)
	if err != nil || proc <= 0 || procNS != ns || procOwner <= 0 || nargs != 1 || result != 1009 || volatility != "i" || !strict || parallel != "s" {
		t.Fatal("procedure identity/properties", proc, procNS, procOwner, nargs, result, volatility, strict, parallel, err)
	}
	if b.PG {
		// Native pg_proc does not expose proargtypes. Its real descriptor
		// signature is independently checked in the provider identity unit.
		var signature string
		if err := c.QueryRowContext(p.Ctx, "SELECT proargtypes::text FROM pg_proc WHERE oid=$1", proc).Scan(&signature); err != nil || signature != "25" {
			t.Fatal("PostgreSQL argument signature", signature, err)
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass::oid AND objid=$1 AND refclassid='pg_extension'::regclass::oid AND refobjid=$2 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, proc, ext))
	if !b.PG {
		// Upstream installs additional functions; native deliberately installs
		// only the selected overload, so total membership is native-only.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass::oid AND refobjid=$1 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, ext))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("SELECT soundex('John')", fixture.Diagnostic{Code: "0A000", Message: "functions are unsupported", Position: "8"}))
	}
}

func TestReferenceDaitchMokotoffInstallation(t *testing.T) {
	daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		availability := func(installed any) {
			run(fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='fuzzystrmatch'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"fuzzystrmatch", "1.2", installed, "determine similarities and distance between strings"}}))
			run(fixture.Select("SELECT name,version,installed,superuser,trusted,relocatable,schema,requires,comment FROM pg_available_extension_versions WHERE name='fuzzystrmatch' AND version='1.2'", []string{"name", "version", "installed", "superuser", "trusted", "relocatable", "schema", "requires", "comment"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "_NAME", "TEXT"}, [][]any{{"fuzzystrmatch", "1.2", installed != nil, true, true, true, nil, nil, "determine similarities and distance between strings"}}))
		}
		availability(nil)
		run(fixture.Exec("CREATE TABLE dm_sentinel(v int)", 0))
		run(fixture.Exec("INSERT INTO dm_sentinel VALUES(77)", 1))
		run(fixture.ImplicitError("CREATE EXTENSION fuzzystrmatch VERSION 'missing'", fixture.Diagnostic{Code: "22023", Message: `extension "fuzzystrmatch" has no installation script nor update path for version "missing"`}))
		daitchCount(t, p, b, c, 0)
		run(fixture.Select("SELECT v FROM dm_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
		run(fixture.Exec("CREATE SCHEMA dm_addon", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT dm_before", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA dm_addon VERSION '1.2'", 0))
		daitchCount(t, p, b, c, 1)
		run(daitchSelect("SELECT dm_addon.daitch_mokotoff('John')", "{160000,460000}", b.PG))
		run(fixture.Exec("ROLLBACK TO dm_before", 0))
		daitchCount(t, p, b, c, 0)
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("CREATE USER dm_installer NOSUPERUSER", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		quotedDatabase := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
		// Trusted script members belong to BOOTSTRAP_SUPERUSERID10,
		// not the installer. The fixture's initdb bootstrap name is stuntgres;
		// its separate postgres role exists only for native dump SQL replay.
		bootstrap := "postgres"
		if b.PG {
			bootstrap = "stuntgres"
		}
		run(fixture.Select("SELECT oid::bigint FROM pg_roles WHERE rolname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{int64(10)}}, bootstrap))
		run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{bootstrap, bootstrap}}))
		run(fixture.Exec("GRANT CREATE ON DATABASE "+quotedDatabase+" TO dm_installer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dm_installer", 0))
		run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"dm_installer", "dm_installer"}}))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA dm_addon VERSION '1.2'", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{bootstrap, bootstrap}}))
		run(fixture.Select("SELECT proowner::bigint FROM pg_proc WHERE proname='daitch_mokotoff'", []string{"proowner"}, []string{"INT8"}, [][]any{{int64(10)}}))
		daitchNamedOwners(t, p.Ctx, c, "dm_installer", bootstrap)
		availability("1.2")
		daitchInventory(t, p, b, c)
		var before, after int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='fuzzystrmatch'").Scan(&before); err != nil {
			t.Fatal(err)
		}
		run(fixture.ImplicitError("CREATE EXTENSION fuzzystrmatch", fixture.Diagnostic{Code: "42710", Message: `extension "fuzzystrmatch" already exists`}))
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS fuzzystrmatch SCHEMA dm_missing VERSION 'missing'", 0))
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "fuzzystrmatch" already exists, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices %#v want %#v", notices, want)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='fuzzystrmatch'").Scan(&after); err != nil || before != after {
			t.Fatal("duplicate identity", before, after, err)
		}
		run(fixture.Exec("SET search_path TO pg_catalog", 0))
		run(extensionMissing("SELECT daitch_mokotoff('John')", "daitch_mokotoff", "unknown"))
		run(fixture.Prepared(daitchSelect("SELECT dm_addon.daitch_mokotoff('John')", "{160000,460000}", b.PG)))
		run(fixture.Exec("RESET search_path", 0))
		run(fixture.Exec("SET search_path TO dm_addon", 0))
		run(daitchSelect("SELECT daitch_mokotoff('John')", "{160000,460000}", b.PG))
	})
}

func daitchStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE SCHEMA dm_addon", 0))
	run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA dm_addon", 0))
	run(fixture.Exec("CREATE TABLE dm_source(id int, v text, d text[] DEFAULT dm_addon.daitch_mokotoff('George'), g text[] GENERATED ALWAYS AS (dm_addon.daitch_mokotoff(v)) STORED, CONSTRAINT dm_valid CHECK(dm_addon.daitch_mokotoff(v) IS NOT NULL))", 0))
	run(fixture.Exec("INSERT INTO dm_source(id,v) VALUES(1,'John'),(2,'Bierschbach')", 2))
	run(fixture.Exec("CREATE VIEW dm_codes AS SELECT id,dm_addon.daitch_mokotoff(v) AS code,d,g FROM dm_source", 0))
	check := fixture.ArrayReferenceSelect("SELECT id,code,d,g FROM dm_codes ORDER BY id", []string{"id", "code", "d", "g"}, []string{"INT4", "_TEXT", "_TEXT", "_TEXT"}, []string{"INT4", "_TEXT", "_TEXT", "_TEXT"}, [][]any{
		{int64(1), "{160000,460000}", "{595000}", "{160000,460000}"},
		{int64(2), daitchPublished[2].output, "{595000}", daitchPublished[2].output},
	}, b.PG)
	run(check)
	run(fixture.Prepared(check))
	return check
}

func TestReferenceDaitchMokotoffStoredExpressions(t *testing.T) {
	daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := daitchStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Failure detail is independently derived from PostgreSQL's row formatter;
		// the pinned oracle confirms it before production is authorized.
		failure := fixture.ImplicitError("INSERT INTO dm_source(id,v) VALUES(3,'John'),(4,'')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "dm_source" violates check constraint "dm_valid"`, Detail: "Failing row contains (4, , {595000}, null).", Schema: "public", Table: "dm_source", Constraint: "dm_valid"})
		failure.ErrorStage = "execute"
		run(failure)
		run(check)
		run(fixture.Prepared(failure))
		run(check)
		run(fixture.Exec("UPDATE dm_source SET v='brzęczy' WHERE id=1", 1))
		run(fixture.ArrayReferenceSelect("SELECT g FROM dm_source WHERE id=1", []string{"g"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{794640,794400,746400,744000}"}}, b.PG))
	})
}

func TestReferenceDaitchMokotoffControls(t *testing.T) {
	daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE USER dm_installer SUPERUSER", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION dm_installer", 0))
		check := daitchStored(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		daitchNamedOwners(t, p.Ctx, c, "dm_installer", "dm_installer")
		daitchInventory(t, p, b, c)
		if b.PG {
			daitchDropSchema(t, p, b, c)
			return // Native controls have no PostgreSQL API equivalent.
		}
		dump := extensionDump(t, p.Ctx, p.Native)
		if strings.Count(dump, "-- stuntgres-extension-owners-v1 ") != 1 {
			t.Fatal("expected one symbolic owner record", dump)
		}
		if strings.Index(dump, "CREATE EXTENSION") > strings.Index(dump, "CREATE TABLE") {
			t.Fatal("installation must precede stored consumers", dump)
		}
		heldQuery := "SELECT dm_addon.daitch_mokotoff('John')"
		retainedStatement, err := c.PrepareContext(p.Ctx, heldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer retainedStatement.Close()
		heldStatement := fixture.HeldStatement{Executor: c, Statement: retainedStatement, SQL: heldQuery}
		fixture.RunStep(t, p.Ctx, heldStatement, b.Recorder, daitchSelect(heldQuery, "{160000,460000}", b.PG))
		held, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		metadata, err := held.ColumnTypes()
		if err != nil || len(metadata) != 4 || metadata[1].DatabaseTypeName() != "_TEXT" || metadata[1].ScanType() != reflect.TypeFor[string]() {
			t.Fatal("retained metadata", metadata, err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		daitchCount(t, p, b, c, 0)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		// Builtin TEXT/text[] keeps the same logical result shape. The held
		// statement reanalyzes against the restored installed procedure,
		// rather than acquiring an invented blanket replacement fence.
		fixture.RunStep(t, p.Ctx, heldStatement, b.Recorder, daitchSelect(heldQuery, "{160000,460000}", b.PG))
		daitchNamedOwners(t, p.Ctx, c, "dm_installer", "dm_installer")
		for i, want := range check.Rows {
			if !held.Next() {
				t.Fatal("retained row", i, held.Err())
			}
			var id int64
			var code, d, g string
			if err := held.Scan(&id, &code, &d, &g); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual([]any{id, code, d, g}, want) {
				t.Fatal("retained values", id, code, d, g, want)
			}
		}
		if held.Next() || held.Err() != nil {
			t.Fatal("retained EOF", held.Err())
		}
		held.Close()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(check))
		daitchInventory(t, p, b, c)
		extensionControlsFailures(t, p, c, b.Recorder, dump, check)
		// A valid staged prefix followed by a genuine invalid provider version
		// must not replace the executable current table/view/provider.
		bad := "CREATE SCHEMA dm_addon; CREATE EXTENSION fuzzystrmatch SCHEMA dm_addon VERSION 'missing';"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid version accepted")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &daitchRestoreCancelReader{cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("staged-prefix cancellation", err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		ioFailure := errors.New("daitch restore reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: ioFailure}); !errors.Is(err, ioFailure) {
			t.Fatal("ordinary reader error", err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "dm_destination")
		if _, err := other.ExecContext(p.Ctx, "CREATE USER dm_installer NOSUPERUSER"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal("independent Driver restore", err)
		}
		// The independent destination is an unwrapped public pool. Exercise
		// its actual plain Query route, without inventing a middleware recorder.
		destinationRows, err := other.Query(check.Query)
		if err != nil {
			t.Fatal("independent Driver plain query", err)
		}
		defer destinationRows.Close()
		columns, err := destinationRows.Columns()
		if err != nil || !reflect.DeepEqual(columns, check.Columns) {
			t.Fatal("destination columns", columns, check.Columns, err)
		}
		destinationTypes, err := destinationRows.ColumnTypes()
		if err != nil || len(destinationTypes) != len(check.Types) {
			t.Fatal("destination metadata", destinationTypes, err)
		}
		for i, typ := range destinationTypes {
			if typ.DatabaseTypeName() != check.Types[i] {
				t.Fatal("destination type", i, typ.DatabaseTypeName(), check.Types[i])
			}
			if check.Types[i] == "INT4" && typ.ScanType() != reflect.TypeFor[int64]() {
				t.Fatal("destination INT4 ScanType", typ.ScanType())
			}
		}
		check.CheckMetadata(t, destinationRows)
		for i, want := range check.Rows {
			if !destinationRows.Next() {
				t.Fatal("destination missing row", i, destinationRows.Err())
			}
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for j := range values {
				destinations[j] = &values[j]
			}
			if err := destinationRows.Scan(destinations...); err != nil {
				t.Fatal("destination scan", err)
			}
			for j, value := range values {
				fixture.AssertRawValue(t, false, check.RawTypes[j], value)
			}
			if !reflect.DeepEqual(values, want) {
				t.Fatal("destination row", i, values, want)
			}
		}
		if destinationRows.Next() || destinationRows.Err() != nil {
			t.Fatal("destination EOF", destinationRows.Err())
		}
		if err := destinationRows.Close(); err != nil {
			t.Fatal("destination close", err)
		}
		if extensionDump(t, p.Ctx, other) != dump {
			t.Fatal("independent Driver symbolic redump changed")
		}
		otherConn, err := other.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		daitchNamedOwners(t, p.Ctx, otherConn, "dm_installer", "dm_installer")
		otherConn.Close()
		missing := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "dm_missing_owner_destination")
		if _, err := missing.ExecContext(p.Ctx, "CREATE TABLE dm_sentinel(v int); INSERT INTO dm_sentinel VALUES(77)"); err != nil {
			t.Fatal(err)
		}
		before := extensionDump(t, p.Ctx, missing)
		if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(dump)); err == nil {
			t.Fatal("missing named owner accepted")
		}
		if extensionDump(t, p.Ctx, missing) != before {
			t.Fatal("missing owner replacement changed target")
		}
		daitchDropSchema(t, p, b, c)
	})
}

func daitchNamedOwners(t *testing.T, ctx context.Context, c *sql.Conn, extensionOwner, procedureOwner string) {
	t.Helper()
	for _, tc := range []struct{ query, want string }{
		{"SELECT rolname FROM pg_roles WHERE oid=(SELECT extowner FROM pg_extension WHERE extname='fuzzystrmatch')", extensionOwner},
		{"SELECT rolname FROM pg_roles JOIN pg_proc ON pg_roles.oid=pg_proc.proowner WHERE proname='daitch_mokotoff'", procedureOwner},
	} {
		var owner string
		if err := c.QueryRowContext(ctx, tc.query).Scan(&owner); err != nil || owner != tc.want {
			t.Fatal("named extension/procedure owner", owner, tc.want, err)
		}
	}
}

func daitchDropSchema(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA dm_addon CASCADE", 0))
	daitchCount(t, p, b, c, 0)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("SELECT dm_addon.daitch_mokotoff('John')", fixture.Diagnostic{Code: "3F000", Message: `schema "dm_addon" does not exist`, Position: "8"}))
}

type daitchRestoreCancelReader struct {
	cancel     context.CancelFunc
	prefixSent bool
}

func (r *daitchRestoreCancelReader) Read(p []byte) (int, error) {
	if !r.prefixSent {
		r.prefixSent = true
		return copy(p, "CREATE SCHEMA dm_staged;\n"), nil
	}
	r.cancel()
	return 0, context.Canceled
}

func TestReferenceDaitchMokotoffFeatureSmoke(t *testing.T) {
	daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := daitchStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT dm_addon.daitch_mokotoff($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ input, output any }{{"John", "{160000,460000}"}, {"Bierschbach", daitchPublished[2].output}, {"brzęczy", "{794640,794400,746400,744000}"}, {"", nil}, {nil, nil}} {
			s := daitchSelect(q, tc.output, b.PG, tc.input)
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("SET search_path TO pg_catalog", 0))
		run(daitchSelect("SELECT dm_addon.daitch_mokotoff('John')", "{160000,460000}", b.PG))
		run(extensionMissing("SELECT daitch_mokotoff('John')", "daitch_mokotoff", "unknown"))
		run(fixture.Exec("RESET search_path", 0))
		run(check)
		// Native precancellation has an ordinary context-error/no-output
		// contract. pq's fast precanceled calls have no stable admission or
		// nil-output guarantee (docs/TESTING.md); do not issue them on PG.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, "John")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled held query published Rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("precanceled held query", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("context error acquired SQLSTATE", err)
			}
			rows, err = c.QueryContext(ctx, q, "John")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled direct query published Rows")
			}
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("precanceled direct query", err)
			}
			canceledStmt, err := c.PrepareContext(ctx, q)
			if canceledStmt != nil {
				canceledStmt.Close()
				t.Fatal("precanceled prepare published Stmt")
			}
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("precanceled prepare", err)
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, daitchSelect(q, "{160000,460000}", b.PG, "John"))
	})
}
