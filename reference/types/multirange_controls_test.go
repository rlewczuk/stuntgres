package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceMultirangeControls(t *testing.T)       { multirangeControls(t, false) }
func TestReferenceMultirangeDomainControls(t *testing.T) { multirangeControls(t, true) }

func multirangeControls(t *testing.T, domains bool) {
	t.Helper()
	p := fixture.NewScenario(t)
	columns := []string{"k", "a", "b", "c"}
	types := []string{"INT4", "INT4MULTIRANGE", "INT8MULTIRANGE", "NUMMULTIRANGE"}
	want := [][]any{
		{int64(1), "{[1,4),[8,9)}", "{[1,4),[8,9)}", "{[1.0,3.0)}"},
		{int64(2), "{}", "{(,)}", "{(4,5)}"}, {int64(3), nil, nil, nil},
		{int64(4), "{[-2147483648,2147483647)}", "{[-9223372036854775808,9223372036854775807)}", "{[-Infinity,NaN]}"},
	}
	for _, backend := range p.Backends() {
		declared := []string{"int4multirange", "int8multirange", "nummultirange"}
		if domains {
			for i, typ := range declared {
				name := "span" + strconv.Itoa(i)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE DOMAIN public."+name+" AS pg_catalog."+typ, 0))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE DOMAIN public.chain"+strconv.Itoa(i)+" AS public."+name, 0))
				declared[i] = "public.chain" + strconv.Itoa(i)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE plain(a public.span0,b public.span1,c public.span2)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO plain VALUES ($1,$2,$3)", 1, "{[1,3),[2,4)}", []byte("{(,)}"), "{(4,5)}")))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO plain VALUES (NULL,NULL,NULL)", 1))
		}
		definition := "(k integer NOT NULL,a " + declared[0] + ",b " + declared[1] + ",c " + declared[2] + ")"
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r"+definition, 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE vacant(a "+declared[0]+" NOT NULL,b "+declared[1]+",c "+declared[2]+")", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'{[1,3),[2,4),empty,[8,9)}','{[1,3),[2,4),empty,[8,9)}','{[1.0,2.00),[2.000,3.0)}'),(2,'{}','{(,)}','{(4,5)}'),(3,NULL,NULL,NULL),(4,'{[-2147483648,2147483647)}','{[-9223372036854775808,9223372036854775807)}','{[-Infinity,Infinity],[Infinity,NaN]}')", 4))
		check := rangeSelect(backend, "SELECT k,a,b,c FROM r ORDER BY k", columns, types, want, []bool{false, true, true, true})
		vacant := rangeSelect(backend, "SELECT a,b,c FROM vacant", columns[1:], types[1:], nil, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(check))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, vacant)
		if backend.PG {
			continue
		} // No PostgreSQL analogue of process-local controls.
		held, err := backend.DB.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check.CheckMetadata(t, held)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("populated same Driver replay", err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rangeConsumeHeld(t, held, check)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
		assertDump := func(db fixture.Backend) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, db.DB, db.Recorder, rangeSelect(db, check.Query, columns, types, want, []bool{false, true, true, true}))
			fixture.RunStep(t, p.Ctx, db.DB, db.Recorder, rangeSelect(db, vacant.Query, columns[1:], types[1:], nil, []bool{false, true, true}))
			if domains {
				fixture.RunStep(t, p.Ctx, db.DB, db.Recorder, rangeSelect(db, "SELECT a,b,c FROM plain WHERE a IS NOT NULL", columns[1:], types[1:], [][]any{{"{[1,4)}", "{(,)}", "{(4,5)}"}}, []bool{true, true, true}))
				fixture.RunStep(t, p.Ctx, db.DB, db.Recorder, rangeSelect(db, "SELECT a,b,c FROM plain WHERE a IS NULL", columns[1:], types[1:], [][]any{{nil, nil, nil}}, []bool{true, true, true}))
			}
		}
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE spacer(k integer)", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("fresh Driver replay", err)
		}
		targetBackend := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
		assertDump(targetBackend)
		redumpCheck := func(poolNative bool) {
			t.Helper()
			var redump bytes.Buffer
			pool := target.Native
			if poolNative {
				pool = p.Native
			}
			if err := stuntgres.Dump(p.Ctx, pool, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("redump changed", err, redump.String())
			}
		}
		redumpCheck(true)
		redumpCheck(false)
		for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
			for _, failure := range multirangeFailures(typ) {
				held, err := target.Memory.QueryContext(target.Ctx, check.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				check.CheckMetadata(t, held)
				script := "CREATE TABLE staged(v " + typ + "); INSERT INTO staged VALUES ('{[7,8)}'); INSERT INTO staged VALUES (" + rangeLiteral(failure.input) + ");"
				position := utf8.RuneCountInString(script[:strings.LastIndex(script, "('")+1]) + 1
				wantError := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: failure.code, Message: failure.message, Detail: failure.detail, Position: strconv.Itoa(position)}
				fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)), &wantError)
				rangeConsumeHeld(t, held, check)
				assertDump(targetBackend)
				redumpCheck(false)
			}
		}
		if domains {
			for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
				held, err := target.Memory.QueryContext(target.Ctx, check.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				script := "CREATE DOMAIN public.span AS " + typ + "; CREATE DOMAIN public.chain AS public.span; CREATE TABLE guard(k integer NOT NULL,a public.chain NOT NULL); INSERT INTO guard VALUES (1,'{[7,8)}'); INSERT INTO guard VALUES (2,NULL);"
				wantError := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "guard" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "guard", Column: "a"}
				fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)), &wantError)
				rangeConsumeHeld(t, held, check)
				assertDump(targetBackend)
				redumpCheck(false)
			}
		}
		canceled, cancel := context.WithCancel(target.Ctx)
		cancel()
		for _, call := range []func() error{
			func() error { return stuntgres.Dump(canceled, target.Native, &bytes.Buffer{}) },
			func() error { return stuntgres.Reset(canceled, target.Native) },
			func() error { return stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())) },
		} {
			err := call()
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("ordinary cancellation lost", err)
			}
		}
		redumpCheck(false)
		held, err = target.Memory.QueryContext(target.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check.CheckMetadata(t, held)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE r(replacement text NOT NULL); INSERT INTO r VALUES ('new shape');")); err != nil {
			t.Fatal(err)
		}
		rangeConsumeHeld(t, held, check)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT replacement FROM r", []string{"replacement"}, []string{"TEXT"}, [][]any{{"new shape"}}))
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted script PostgreSQL replay", err)
			}
			assertDump(fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord})
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
