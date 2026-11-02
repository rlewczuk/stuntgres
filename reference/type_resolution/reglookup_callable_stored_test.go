package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var callableStoredNames = []string{"r", "p", "o", "s"}
var callableStoredTypes = []string{"REGPROC", "REGPROCEDURE", "REGOPER", "REGOPERATOR"}
var callableStoredValues = []any{"boolin", "abs(integer)", "||/", "+(integer,integer)"}

func callableStoredSelect(b fixture.Backend, q string, names, types []string, rows [][]any) fixture.Step {
	nullable := make([]bool, len(names))
	for i := range nullable {
		nullable[i] = true
	}
	return callableSelectMetadata(b, q, names, types, rows, nullable)
}

func callableCreateStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	regObjectExec(t, p, b, c,
		"CREATE TABLE public.call_store_data(id integer,r regproc DEFAULT to_regproc('boolin'),p regprocedure DEFAULT to_regprocedure('abs(integer)'),o regoper DEFAULT to_regoper('||/'),s regoperator DEFAULT to_regoperator('+(int,int)'))",
		"CREATE VIEW public.call_store_view AS SELECT to_regproc('boolin') AS r,to_regprocedure('abs(integer)') AS p,to_regoper('||/') AS o,to_regoperator('+(int,int)') AS s,to_regproc(NULL::text) AS rn,to_regprocedure(NULL::text) AS pn,to_regoper('call_absent') AS om,to_regoperator('call_absent(int,int)') AS sm",
		"CREATE VIEW public.call_store_nested AS SELECT r,p,o,s,rn,pn,om,sm FROM public.call_store_view")
	regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.call_store_data(id) VALUES(1)", 1))
	regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.call_store_data VALUES(2,to_regproc(NULL),to_regprocedure('call_absent()'),to_regoper(NULL),to_regoperator('call_absent(int,int)'))", 1))
}

func callableStoredChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, source := range []string{"public.call_store_data WHERE id=1", "public.call_store_view", "public.call_store_nested"} {
		regObjectBoth(t, p, b, c, callableStoredSelect(b, "SELECT r,p,o,s FROM "+source, callableStoredNames, callableStoredTypes, [][]any{callableStoredValues}))
		regObjectBoth(t, p, b, c, fixture.Select("SELECT r::oid::bigint AS r,p::oid::bigint AS p,o::oid::bigint AS o,s::oid::bigint AS s FROM "+source, callableStoredNames, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(1242), int64(1397), int64(597), int64(551)}}))
	}
	regObjectBoth(t, p, b, c, callableStoredSelect(b, "SELECT r,p,o,s FROM public.call_store_data WHERE id=2", callableStoredNames, callableStoredTypes, [][]any{{nil, nil, nil, nil}}))
	regObjectBoth(t, p, b, c, callableStoredSelect(b, "SELECT rn,pn,om,sm FROM public.call_store_nested", []string{"rn", "pn", "om", "sm"}, callableStoredTypes, [][]any{{nil, nil, nil, nil}}))
	regObjectBoth(t, p, b, c, callableStoredSelect(b, "SELECT r,p,o,s FROM public.call_store_nested WHERE FALSE", callableStoredNames, callableStoredTypes, nil))
	regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.call_store_data(id) VALUES(3)", 1))
	regObjectBoth(t, p, b, c, callableStoredSelect(b, "SELECT r,p,o,s FROM public.call_store_data WHERE id=3", callableStoredNames, callableStoredTypes, [][]any{callableStoredValues}))
	regObjectBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM public.call_store_data", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
	regObjectRunStep(t, p, b, c, fixture.Exec("DELETE FROM public.call_store_data WHERE id=3", 1))
}

func callableStoredControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted *[]byte) {
	t.Helper()
	if b.PG {
		if len(*emitted) == 0 {
			t.Fatal("missing actual native callable Dump for PostgreSQL replay")
		}
		regObjectExec(t, p, b, c, "DROP VIEW public.call_store_nested", "DROP VIEW public.call_store_view", "DROP TABLE public.call_store_data")
		if _, err := c.ExecContext(p.Ctx, string(*emitted)); err != nil {
			t.Fatal("PostgreSQL replay of actual native callable Dump", err)
		}
		regObjectExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		callableStoredChecks(t, p, b, c)
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if dump.Len() == 0 {
		t.Fatal("empty actual callable Dump")
	}
	*emitted = bytes.Clone(dump.Bytes())
	// A native eager finalized cursor must keep its actual descriptor/value
	// ownership across catalog replacement. No unread pq portal is simulated.
	retained, err := c.QueryContext(p.Ctx, "SELECT r,p,o,s FROM public.call_store_data WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	check := callableStoredSelect(b, "", callableStoredNames, callableStoredTypes, nil)
	check.CheckMetadata(t, retained)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(*emitted)); err != nil {
		t.Fatal(err)
	}
	check.CheckMetadata(t, retained)
	for i, col := range metadata {
		if col.Name() != callableStoredNames[i] || col.DatabaseTypeName() != callableStoredTypes[i] {
			t.Fatal("previously returned callable ColumnTypes changed", i, col)
		}
	}
	fixture.ArrayReferenceConsumeHeld(t, retained, [][]any{callableStoredValues}, callableStoredTypes)
	callableStoredChecks(t, p, b, c)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(*emitted, redump.Bytes()) {
		t.Fatal("unchanged callable round-trip redump differs")
	}

	// A genuinely different Driver/engine starts with displaced catalog state;
	// Restore must replace it and rebuild retained calls, defaults and views.
	fresh := fixture.NewFixture(t, false, true)
	fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
	fc, err := fresh.Memory.Conn(fresh.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fc.Close()
	regObjectExec(t, fresh, fb, fc, "CREATE TABLE public.call_displaced(v integer)")
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(*emitted)); err != nil {
		t.Fatal(err)
	}
	callableStoredChecks(t, fresh, fb, fc)
	regObjectExec(t, fresh, fb, fc, "CREATE TABLE public.call_sentinel(v integer)")
	regObjectRunStep(t, fresh, fb, fc, fixture.Exec("INSERT INTO public.call_sentinel VALUES(42)", 1))
	preserved := func() {
		t.Helper()
		regObjectBoth(t, fresh, fb, fc, fixture.Select("SELECT v FROM public.call_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		callableStoredChecks(t, fresh, fb, fc)
	}
	for _, bad := range []string{
		"CREATE TABLE call_staged(p regprocedure); INSERT INTO call_staged VALUES(to_regprocedure('abs(numeric(0))'));",
		string(*emitted) + " SELECT (",
	} {
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid staged Restore succeeded")
		}
		preserved()
	}
	ctx, cancel := context.WithCancel(fresh.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, fresh.Native, bytes.NewReader(*emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled Restore", err)
	}
	preserved()
}

func TestReferenceRegLookupCallableStored(t *testing.T) {
	var emitted []byte
	regLookupScenario(t, []string{"call_dynamic"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		callableCreateStored(t, p, b, c)
		callableStoredChecks(t, p, b, c)
		regObjectExec(t, p, b, c, "CREATE SCHEMA call_dynamic", "CREATE FUNCTION call_dynamic.target(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE OPERATOR call_dynamic.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)",
			"CREATE VIEW public.call_dynamic_view AS SELECT to_regproc('call_dynamic.target') AS r,to_regprocedure('call_dynamic.target(integer)') AS p,to_regoper('call_dynamic.#~#') AS o,to_regoperator('call_dynamic.#~#(int,int)') AS s")
		firstF := regLookupIdentity(t, p, c, "SELECT 'call_dynamic.target(integer)'::regprocedure::oid::bigint")
		firstO := regLookupIdentity(t, p, c, "SELECT 'call_dynamic.#~#(int,int)'::regoperator::oid::bigint")
		// Stored alias data deliberately retain identities; the TEXT-call view
		// deliberately does not acquire a dependency on its resolved target.
		regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.call_store_data SELECT 4,r,p,o,s FROM public.call_dynamic_view", 1))
		checkDynamic := func(f, o any) {
			t.Helper()
			regObjectBoth(t, p, b, c, fixture.Select("SELECT r::oid::bigint AS r,p::oid::bigint AS p,o::oid::bigint AS o,s::oid::bigint AS s FROM public.call_dynamic_view", callableStoredNames, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{f, f, o, o}}))
		}
		checkDynamic(firstF, firstO)
		regObjectExec(t, p, b, c, "ALTER FUNCTION call_dynamic.target(integer) RENAME TO renamed", "DROP OPERATOR call_dynamic.#~#(integer,integer)")
		checkDynamic(nil, nil)
		regObjectExec(t, p, b, c, "DROP FUNCTION call_dynamic.renamed(integer)")
		checkDynamic(nil, nil)
		regObjectBoth(t, p, b, c, fixture.Select("SELECT r::oid::bigint AS r,p::oid::bigint AS p,o::oid::bigint AS o,s::oid::bigint AS s FROM public.call_store_data WHERE id=4", callableStoredNames, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{firstF, firstF, firstO, firstO}}))
		regObjectExec(t, p, b, c, "CREATE FUNCTION call_dynamic.target(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "CREATE OPERATOR call_dynamic.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)")
		newF := regLookupIdentity(t, p, c, "SELECT 'call_dynamic.target(integer)'::regprocedure::oid::bigint")
		newO := regLookupIdentity(t, p, c, "SELECT 'call_dynamic.#~#(int,int)'::regoperator::oid::bigint")
		if newF == firstF || newO == firstO {
			t.Fatal("stored callable retained dropped lookup identity")
		}
		checkDynamic(newF, newO)
		regObjectRunStep(t, p, b, c, fixture.Exec("DELETE FROM public.call_store_data WHERE id=4", 1))
		regObjectExec(t, p, b, c, "DROP VIEW public.call_dynamic_view", "DROP SCHEMA call_dynamic CASCADE")
		callableStoredControls(t, p, b, c, &emitted)
	})
}

func TestReferenceRegLookupCallableSmoke(t *testing.T) {
	var emitted []byte
	regLookupScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectBoth(t, p, b, c, callableSelect(b, "SELECT to_regoper('||/') AS o,to_regoperator('+(int,int)') AS s,to_regproc('boolin') AS r,to_regprocedure('abs(integer)') AS p", []string{"o", "s", "r", "p"}, []string{"REGOPER", "REGOPERATOR", "REGPROC", "REGPROCEDURE"}, [][]any{{"||/", "+(integer,integer)", "boolin", "abs(integer)"}}))
		regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regoper('||/')::oid::bigint AS o,to_regoperator('+(int,int)')::oid::bigint AS s,to_regproc('boolin')::oid::bigint AS r,to_regprocedure('abs(integer)')::oid::bigint AS p", []string{"o", "s", "r", "p"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(597), int64(551), int64(1242), int64(1397)}}))
		regObjectBoth(t, p, b, c, callableSelect(b, "SELECT to_regoper('+') AS o,to_regproc('abs') AS r,to_regoperator('call_absent(int,int)') AS s,to_regprocedure(NULL::text) AS p", []string{"o", "r", "s", "p"}, []string{"REGOPER", "REGPROC", "REGOPERATOR", "REGPROCEDURE"}, [][]any{{nil, nil, nil, nil}}))
		q := "SELECT to_regprocedure($1) AS value"
		held := regLookupHeld(t, p, c, q)
		defer held.Statement.Close()
		regObjectRunStep(t, p, b, held, callableSelect(b, q, []string{"value"}, []string{"REGPROCEDURE"}, [][]any{{"abs(integer)"}}, "abs(integer)"))
		t.Run("hard_modifier", func(t *testing.T) {
			regObjectRunStep(t, p, b, held, callableErrorStep(q, callableDiagnostic("22023", "NUMERIC precision 0 must be between 1 and 1000"), "abs(numeric(0))"))
		})
		regObjectRunStep(t, p, b, held, callableSelect(b, q, []string{"value"}, []string{"REGPROCEDURE"}, [][]any{{"abs(integer)"}}, []byte("abs(int)")))
		regObjectRunStep(t, p, b, held, callableSelect(b, q, []string{"value"}, []string{"REGPROCEDURE"}, [][]any{{nil}}, nil))
		callableCreateStored(t, p, b, c)
		callableStoredChecks(t, p, b, c)
		callableStoredControls(t, p, b, c, &emitted)
	})
}
