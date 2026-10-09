package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func arrayReferenceConsumeHeld(t *testing.T, rows *sql.Rows, want [][]any, types []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		if column.DatabaseTypeName() != types[i] {
			t.Fatalf("held type %d=%q want %q", i, column.DatabaseTypeName(), types[i])
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(types))
		dest := make([]any, len(types))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			values[i] = referenceNormalize(value)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("held rows=%#v want %#v", got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func arrayReferenceNativeRoundtrip(t *testing.T, p *referencePair, c *sql.Conn, b referenceBackend, check referenceStep, targetSetup ...referenceStep) {
	t.Helper()
	if b.pg {
		t.Fatal("native controls called on PostgreSQL")
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	held, err := c.QueryContext(p.ctx, check.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if check.checkMetadata != nil {
		check.checkMetadata(t, held)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE public.array_replacement (v TEXT[])", 0))
	referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO public.array_replacement VALUES ('{new}')", 1))
	arrayReferenceConsumeHeld(t, held, check.rows, check.types)
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, c, b.recorder, check)
	referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(check))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatalf("dump changed after replacement\nfirst=%s\nsecond=%s", dump.String(), redump.String())
	}
	target := newReferenceFixture(t, false, true)
	for _, step := range targetSetup {
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, step)
	}
	// Deliberately advance the target's relation/domain allocator independently.
	for _, q := range []string{"CREATE TABLE public.array_spacer (v INT)", "CREATE DOMAIN public.array_spacer_domain AS TEXT"} {
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec(q, 0))
	}
	if err := stuntgres.Restore(target.ctx, target.native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, check)
	referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referencePrepared(check))
	var targetDump bytes.Buffer
	if err := stuntgres.Dump(target.ctx, target.native, &targetDump); err != nil {
		t.Fatal(err)
	}
	if targetDump.String() != dump.String() {
		t.Fatalf("independent target dump=%s want %s", targetDump.String(), dump.String())
	}
	referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("CREATE TABLE public.array_sentinel (v INT[])", 0))
	referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("INSERT INTO public.array_sentinel VALUES ('[0:1]={42,NULL}')", 1))
	sentinel := arrayReferenceSelect("SELECT v FROM public.array_sentinel", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"[0:1]={42,NULL}"}}, false)
	for _, tc := range []struct{ input, code string }{{"{1,,2}", "22P02"}, {"{{1},{2,3}}", "22P02"}, {"{oops}", "22P02"}} {
		sql := "CREATE TABLE public.array_staged (a INT[]); INSERT INTO public.array_staged VALUES ('{7}'); INSERT INTO public.array_staged VALUES (" + arrayReferenceQuote(tc.input) + ");"
		err := stuntgres.Restore(target.ctx, target.native, strings.NewReader(sql))
		referenceState(t, err, tc.code)
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, sentinel)
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, check)
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceError("SELECT a FROM public.array_staged", "42P01"))
	}
	ioFailure := errors.New("array control IO failure")
	if err := stuntgres.Dump(target.ctx, target.native, catalogControlFailingWriter{ioFailure}); !errors.Is(err, ioFailure) {
		t.Fatalf("writer failure=%v", err)
	}
	if err := stuntgres.Restore(target.ctx, target.native, catalogControlFailingReader{ioFailure}); !errors.Is(err, ioFailure) {
		t.Fatalf("reader failure=%v", err)
	}
	ctx, cancel := context.WithCancel(target.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, target.native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore=%v", err)
	}
	if _, err := target.memory.ExecContext(ctx, "INSERT INTO public.array_sentinel VALUES ('{99}')"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled array insert=%v", err)
	}
	referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, sentinel)
	referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, check)
}

func TestReferenceArrayNativeControls(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN public.array_control_di AS INT", "CREATE DOMAIN public.array_control_dj AS public.array_control_di",
			"CREATE DOMAIN public.array_control_da AS INT[]", "CREATE DOMAIN public.array_control_dd AS public.array_control_di[]",
			"CREATE TABLE public.array_control (id INT,i INT[],t TEXT[][],d public.array_control_dj[],a public.array_control_da[],n public.array_control_dd,b BYTEA[],z TIMESTAMPTZ[],r INTERVAL[])",
		} {
			run(referenceExec(q, 0))
		}
		run(referenceExec(`INSERT INTO public.array_control VALUES (1,'[-1:0][3:4]={{1,NULL},{2,3}}','{NULL,"NULL","","a,b","a\\b"}','{7,NULL}','{"{1,2}",NULL}','{9,NULL}','{"\\x00ff",NULL}','{"2024-02-29 12:00:00+00",NULL}','{"1 day 02:03:04",NULL}'),(2,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL),(3,'{}','{}','{}','{}','{}','{}','{}','{}')`, 3))
		types := []string{"INT4", "_INT4", "_TEXT", "_ARRAY_CONTROL_DJ", "_ARRAY_CONTROL_DA", "_ARRAY_CONTROL_DI", "_BYTEA", "_TIMESTAMPTZ", "_INTERVAL"}
		if b.pg {
			types[3], types[4], types[5] = "", "", ""
		}
		raw := []string{"INT4", "_INT4", "_TEXT", "_INT4", "_INT4", "_INT4", "_BYTEA", "_TIMESTAMPTZ", "_INTERVAL"}
		check := arrayReferenceSelect("SELECT id,i,t,d,a,n,b,z,r FROM public.array_control ORDER BY id", []string{"id", "i", "t", "d", "a", "n", "b", "z", "r"}, types, raw, [][]any{
			{int64(1), "[-1:0][3:4]={{1,NULL},{2,3}}", `{NULL,"NULL","","a,b","a\\b"}`, "{7,NULL}", `{"{1,2}",NULL}`, "{9,NULL}", `{"\\x00ff",NULL}`, `{"2024-02-29 12:00:00+00",NULL}`, `{"1 day 02:03:04",NULL}`},
			{int64(2), nil, nil, nil, nil, nil, nil, nil, nil}, {int64(3), "{}", "{}", "{}", "{}", "{}", "{}", "{}", "{}"},
		}, b.pg)
		run(check)
		run(referencePrepared(check))
		if !b.pg {
			arrayReferenceNativeRoundtrip(t, p, c, b, check)
		}
		arrayReferenceTemporaryControls(t, p, c, b, check)
	})
}

func TestReferenceArraySmoke(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE DOMAIN public.array_smoke_di AS INT", 0))
		run(referenceExec("CREATE TABLE public.array_smoke (id INT,i INT[],t TEXT[][],d public.array_smoke_di[])", 0))
		run(referenceExec(`INSERT INTO public.array_smoke VALUES (1,'[-1:0][3:4]={{1,NULL},{2,3}}','{NULL,"NULL","","a,b"}','{7,NULL}')`, 1))
		input := []byte(`{NULL,"NULL","","a,b"}`)
		run(referencePrepared(referenceExec("INSERT INTO public.array_smoke VALUES (2,$1,$2,$3)", 1, "[-1:0][3:4]={{1,NULL},{2,3}}", input, "{7,NULL}")))
		for i := range input {
			input[i] = 'x'
		}
		name := "_ARRAY_SMOKE_DI"
		if b.pg {
			name = ""
		}
		check := arrayReferenceSelect("SELECT id,i,t,d FROM public.array_smoke ORDER BY id", []string{"id", "i", "t", "d"}, []string{"INT4", "_INT4", "_TEXT", name}, []string{"INT4", "_INT4", "_TEXT", "_INT4"}, [][]any{{int64(1), "[-1:0][3:4]={{1,NULL},{2,3}}", `{NULL,"NULL","","a,b"}`, "{7,NULL}"}, {int64(2), "[-1:0][3:4]={{1,NULL},{2,3}}", `{NULL,"NULL","","a,b"}`, "{7,NULL}"}}, b.pg)
		run(check)
		run(referencePrepared(check))
		want := controlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		want.Detail = `Unexpected "," character.`
		want.Position = "8"
		s := referenceErrorDiagnostic("SELECT '{1,,2}'::INT4[] AS a FROM public.one", want)
		s.selectRows = true
		s.errorStage = "execute"
		run(s)
		want.Position = ""
		want.Context = "unnamed portal parameter $1 = '...'"
		s = referenceErrorDiagnostic("SELECT $1::INT4[] AS a FROM public.one", want, "{1,,2}")
		s.selectRows = true
		s.prepared = true
		s.errorStage = "execute"
		run(s)
		want.Context = "unnamed portal parameter $2 = '...'"
		run(referencePrepared(referenceErrorDiagnostic("INSERT INTO public.array_smoke (id,i) VALUES (3,$1),(4,$2)", want, "{9}", "{1,,2}")))
		run(check)
		if !b.pg {
			for _, q := range []string{"SELECT ARRAY[1,2] FROM public.one", "SELECT i[1] FROM public.array_smoke"} {
				run(referenceError(q, "42601"))
			}
			for _, q := range []string{"SELECT i FROM public.array_smoke ORDER BY i", "SELECT i=i FROM public.array_smoke"} {
				run(referenceError(q, "42883"))
			}
			arrayReferenceNativeRoundtrip(t, p, c, b, check)
		}
		functionQuery := "SELECT array_length(i,1) AS n FROM public.array_smoke ORDER BY id"
		if b.pg {
			run(referenceSelect(functionQuery, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
		} else {
			want := controlsReferenceDiagnostic("0A000", "functions are unsupported")
			want.Position = "8"
			step := referenceErrorDiagnostic(functionQuery, want)
			step.selectRows = true
			step.errorStage = "execute"
			run(step)
		}
	})
}

func arrayReferenceTemporaryControls(t *testing.T, p *referencePair, c *sql.Conn, b referenceBackend, persistent referenceStep) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TEMP SEQUENCE array_temp_seed", 0))
	// TEMP tables remain an adjacent capability boundary, even with supported
	// array declarations. PostgreSQL's positive branch does not grant native SQL.
	for _, prepared := range []bool{false, true} {
		name := "array_temp_direct"
		if prepared {
			name = "array_temp_prepared"
		}
		query := "CREATE TABLE pg_temp." + name + " (i INT[],t TEXT[])"
		var step referenceStep
		if b.pg {
			step = referenceExec(query, 0)
		} else {
			want := controlsReferenceDiagnostic("0A000", "temporary schema creation is unsupported")
			want.Position = "14"
			step = referenceErrorDiagnostic(query, want)
			step.errorStage = "execute"
		}
		step.prepared = prepared
		run(step)
		if b.pg {
			run(referenceExec("INSERT INTO pg_temp."+name+` VALUES ('[-2:-1]={5,NULL}','{NULL,"NULL","a,b"}')`, 1))
			run(arrayReferenceSelect("SELECT i,t FROM pg_temp."+name, []string{"i", "t"}, []string{"_INT4", "_TEXT"}, []string{"_INT4", "_TEXT"}, [][]any{{"[-2:-1]={5,NULL}", `{NULL,"NULL","a,b"}`}}, true))
			run(referenceExec("DROP TABLE pg_temp."+name, 0))
		}
		run(persistent)
	}
	run(referenceExec("DROP SEQUENCE pg_temp.array_temp_seed", 0))
}
