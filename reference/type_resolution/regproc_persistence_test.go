package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func regprocPersistentSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	for _, q := range []string{
		"CREATE SCHEMA regproc_store",
		"CREATE EXTENSION unaccent SCHEMA regproc_store",
		"SET search_path TO regproc_store,public",
		"CREATE DOMAIN regproc_store.proc_value AS regprocedure",
		"CREATE TABLE regproc_store.values_subject(id integer,r regproc,p regprocedure)",
		"CREATE TABLE regproc_store.recursive_subject(id integer,d regproc_store.proc_value,a regproc[],b regprocedure[])",
		"CREATE TABLE regproc_store.defaults_subject(id integer,r regproc DEFAULT 'pg_catalog.abs(integer)'::regprocedure::regproc,p regprocedure DEFAULT 'regproc_store.unaccent(text)'::regprocedure)",
		"CREATE VIEW regproc_store.constant_view AS SELECT 'pg_catalog.abs(integer)'::regprocedure::regproc AS r,'regproc_store.unaccent(text)'::regprocedure AS p",
	} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(`INSERT INTO regproc_store.values_subject VALUES
 (1,'boolin','abs(integer)'),(2,'1397','abs(bigint)'),
 (3,'"RI_FKey_check_ins"','"RI_FKey_check_ins"()'),
 (4,'-','4294967295'),(5,NULL,NULL),
 (6,'regproc_store.unaccent(text)'::regprocedure::regproc,'regproc_store.unaccent(text)')`, 6))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO regproc_store.defaults_subject(id) VALUES(1)", 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO regproc_store.recursive_subject VALUES(1,'regproc_store.unaccent(text)','{boolin,1397,NULL}','{now(),abs(integer),NULL}'),(2,NULL,NULL,NULL)", 2))
}

func regprocPersistentCheck(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, r, fixture.Exec("SET search_path TO regproc_store,public", 0))
	regprocBoth(t, p, x, r, fixture.Select("SELECT id,r,p FROM regproc_store.values_subject ORDER BY id", []string{"id", "r", "p"}, []string{"INT4", "REGPROC", "REGPROCEDURE"}, [][]any{
		{int64(1), "boolin", "abs(integer)"}, {int64(2), "pg_catalog.abs", "abs(bigint)"},
		{int64(3), `"RI_FKey_check_ins"`, `"RI_FKey_check_ins"()`}, {int64(4), "-", "4294967295"}, {int64(5), nil, nil},
		{int64(6), "regproc_store.unaccent", "unaccent(text)"},
	}))
	for _, q := range []string{"SELECT r,p FROM regproc_store.defaults_subject", "SELECT r,p FROM regproc_store.constant_view"} {
		regprocBoth(t, p, x, r, fixture.Select(q, []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{"pg_catalog.abs", "unaccent(text)"}}))
	}
	regprocBoth(t, p, x, r, fixture.Select("SELECT d,a,b FROM regproc_store.recursive_subject ORDER BY id", []string{"d", "a", "b"}, []string{"REGPROCEDURE", "_REGPROC", "_REGPROCEDURE"}, [][]any{{"unaccent(text)", "{boolin,pg_catalog.abs,NULL}", "{now(),abs(integer),NULL}"}, {nil, nil, nil}}))
	regprocBoth(t, p, x, r, fixture.Select("SELECT (r::oid='pg_catalog.abs(integer)'::regprocedure::oid) AS builtin FROM regproc_store.values_subject WHERE id=2", []string{"builtin"}, []string{"BOOL"}, [][]any{{true}}))
	regprocBoth(t, p, x, r, fixture.Select("SELECT (p::oid='regproc_store.unaccent(text)'::regprocedure::oid) AS actual_provider FROM regproc_store.values_subject WHERE id=6", []string{"actual_provider"}, []string{"BOOL"}, [][]any{{true}}))
}

func TestReferenceRegProcPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "regproc_store")
	var emitted []byte
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		regprocPersistentSetup(t, p, c, r)
		regprocPersistentCheck(t, p, c, r)
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop extension unaccent because other objects depend on it")
		want.Detail = "default value for column p of table defaults_subject depends on function unaccent(text)\nview constant_view depends on function unaccent(text)"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("DROP EXTENSION unaccent RESTRICT", want))
		regprocPersistentCheck(t, p, c, r)
		// This expression is also the required overload-safe dump spelling.
		regprocBoth(t, p, c, r, fixture.Select("SELECT 'pg_catalog.abs(integer)'::regprocedure::regproc AS r", []string{"r"}, []string{"REGPROC"}, [][]any{{"pg_catalog.abs"}}))
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("DROP SCHEMA regproc_store CASCADE", 0))
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native Dump PostgreSQL replay", err)
			}
			regprocPersistentCheck(t, p, c, r)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		retained, err := c.QueryContext(p.Ctx, "SELECT r,p FROM regproc_store.values_subject WHERE id=6")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		regprocPersistentCheck(t, p, c, r)
		columns, err := retained.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"r", "p"}) {
			t.Fatal("retained alias columns", columns, err)
		}
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, family := range []string{"REGPROC", "REGPROCEDURE"} {
			col := metadata[i]
			nullable, nullableOK := col.Nullable()
			length, lengthOK := col.Length()
			precision, scale, decimalOK := col.DecimalSize()
			if col.Name() != columns[i] || col.DatabaseTypeName() != family || col.ScanType() != reflect.TypeFor[string]() || !nullable || !nullableOK || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
				t.Fatalf("retained alias metadata column=%d name=%q type=%q scan=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), nullable, nullableOK, length, lengthOK, precision, scale, decimalOK)
			}
		}
		if !retained.Next() {
			t.Fatal("retained alias row absent", retained.Err())
		}
		var rawR, rawP any
		if err := retained.Scan(&rawR, &rawP); err != nil {
			t.Fatal(err)
		}
		wantR, wantP := "regproc_store.unaccent", "unaccent(text)"
		if r, ok := rawR.(string); !ok || r != wantR {
			t.Fatalf("retained REGPROC raw got=%T/%#v want=%q", rawR, rawR, wantR)
		}
		if p, ok := rawP.(string); !ok || p != wantP {
			t.Fatalf("retained REGPROCEDURE raw got=%T/%#v want=%q", rawP, rawP, wantP)
		}
		var ownedR, ownedP string
		if err := retained.Scan(&ownedR, &ownedP); err != nil {
			t.Fatal(err)
		}
		if ownedR != wantR || ownedP != wantP || retained.Next() || retained.Err() != nil {
			t.Fatal("retained alias values/EOF", ownedR, ownedP, retained.Err())
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		if rawR != wantR || rawP != wantP || ownedR != wantR || ownedP != wantP {
			t.Fatal("retained alias cells changed after close")
		}
		target := fixture.NewFixture(t, false, true)
		for _, q := range []string{"CREATE SCHEMA regproc_displacement", "CREATE DOMAIN regproc_displacement.d AS integer", "CREATE TABLE regproc_displacement.t(v integer)"} {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		regprocPersistentCheck(t, target, target.Memory, target.MemoryRecord)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.regproc_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.regproc_sentinel VALUES(42)", 1))
		for _, bad := range []string{"CREATE TABLE regproc_bad(r regproc); INSERT INTO regproc_bad VALUES('missing_regproc_name');", string(emitted) + " SELECT ("} {
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			regprocBoth(t, target, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.regproc_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			regprocPersistentCheck(t, target, target.Memory, target.MemoryRecord)
		}
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		regprocPersistentCheck(t, target, target.Memory, target.MemoryRecord)
		regprocBoth(t, target, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.regproc_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
}

func TestReferenceRegProcFeatureSmoke(t *testing.T) {
	var emitted []byte
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		regprocBoth(t, p, c, r, fixture.Select(`SELECT 'boolin'::regproc AS r,'abs(integer)'::regprocedure AS p,'"RI_FKey_check_ins"()'::regprocedure::oid::bigint AS id`, []string{"r", "p", "id"}, []string{"REGPROC", "REGPROCEDURE", "INT8"}, [][]any{{"boolin", "abs(integer)", int64(1644)}}))
		regprocBoth(t, p, c, r, fixture.Select("SELECT 'boolin'::regproc::oid::bigint AS r,'abs(integer)'::regprocedure::oid::bigint AS p", []string{"r", "p"}, []string{"INT8", "INT8"}, [][]any{{int64(1242), int64(1397)}}))
		const q = "SELECT $1::regprocedure AS p"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		rows, err := held.QueryContext(p.Ctx, "abs(text)")
		if rows != nil {
			rows.Close()
			t.Error("invalid signature exposed rows")
		}
		regprocError(t, err, "42883", `function "abs(text)" does not exist`, q, "held-frame", "abs(text)")
		regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, fixture.Select(q, []string{"p"}, []string{"REGPROCEDURE"}, [][]any{{"abs(integer)"}}, "abs(int)"))
		_, err = c.ExecContext(p.Ctx, "SELECT $1::regproc", "abs")
		regprocError(t, err, "42725", `more than one function named "abs"`, "SELECT $1::regproc", "direct-frame", "abs")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE public.regproc_smoke(id integer,r regproc,p regprocedure)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.regproc_smoke VALUES(1,'boolin','abs(integer)')", 1))
		const emptyDemand = "SELECT $1::regprocedure AS p FROM public.regproc_smoke WHERE id=99"
		emptyRows, emptyErr := c.QueryContext(p.Ctx, emptyDemand, "abs(text)")
		if emptyRows != nil {
			emptyRows.Close()
			t.Error("empty result bypassed invalid parameter admission")
		}
		regprocError(t, emptyErr, "42883", `function "abs(text)" does not exist`, emptyDemand, "direct-frame", "abs(text)")
		const bad = "INSERT INTO public.regproc_smoke VALUES(2,'now','now()'),(3,'missing_regproc_name','now()')"
		result, err := c.ExecContext(p.Ctx, bad)
		if result != nil {
			t.Error("failed multirow insert exposed result")
		}
		regprocError(t, err, "42883", `function "missing_regproc_name" does not exist`, bad, "atomic-insert", "")
		check := fixture.Select("SELECT id,r,p FROM public.regproc_smoke ORDER BY id", []string{"id", "r", "p"}, []string{"INT4", "REGPROC", "REGPROCEDURE"}, [][]any{{int64(1), "boolin", "abs(integer)"}})
		regprocBoth(t, p, c, r, check)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			regprocBoth(t, p, c, r, check)
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("DROP TABLE public.regproc_smoke", 0))
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual smoke Dump PostgreSQL replay", err)
			}
			regprocBoth(t, p, c, r, check)
		}
	})
}
