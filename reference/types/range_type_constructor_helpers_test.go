package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The shared checks above this explicit native phase are also run against the
// actual bytes emitted by Dump, not a separately authored PostgreSQL script.
func rangeTypeConstructorReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, raw32 bool, check func(fixture.Backend, fixture.Executor), saved, insert, after fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	held, err := conn.QueryContext(p.Ctx, saved.Query, saved.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var script bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
		t.Fatal(err)
	}
	target := fixture.NewFixture(t, false, true)
	owner, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Exec(`CREATE TEMP TABLE constructor_occupied(k integer)`, 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script.Bytes())); err != nil {
		t.Fatal("fresh constructor Restore", err)
	}
	check(fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}, target.Memory)
	fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Select(`SELECT k FROM constructor_occupied`, []string{"k"}, []string{"INT4"}, nil))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, insert)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, after)
	var before bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &before); err != nil {
		t.Fatal(err)
	}
	retained, err := target.Memory.QueryContext(target.Ctx, after.Query, after.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	want := fixture.ControlsReferenceDiagnostic("42804", `operator class "text_ops" does not accept data type integer`)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(append(append([]byte(nil), script.Bytes()...), []byte(`CREATE TYPE constructor_late_invalid AS RANGE(SUBTYPE=int4,SUBTYPE_OPCLASS=text_ops);`)...))), &want)
	rangeConsumeHeld(t, retained, after)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, after)
	var unchanged bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &unchanged); err != nil || !bytes.Equal(before.Bytes(), unchanged.Bytes()) {
		t.Fatal("failed constructor Restore changed dump", err)
	}
	check(b, conn)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
		t.Fatal("same-engine constructor Restore", err)
	}
	rangeConsumeHeld(t, held, saved)
	check(b, conn)
	if p.Postgres != nil {
		clear := `DROP SCHEMA IF EXISTS range_ctor_alias CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public;`
		if raw32 {
			clear += `DROP CAST IF EXISTS (real AS xid); DROP CAST IF EXISTS (xid AS real);`
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, clear); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, script.String()); err != nil {
			t.Fatalf("actual constructor dump PostgreSQL replay: %v\n%s", err, script.String())
		}
		check(fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}, p.Postgres)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, insert)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, after)
		if _, err := p.Postgres.ExecContext(p.Ctx, clear); err != nil {
			t.Fatal(err)
		}
	}
}

// Restore has no PostgreSQL counterpart. These explicit native controls use the
// same public API as the shared constructor scenario and its actual Dump bytes.
func rangeTypeConstructorAdmission(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	}
	var script bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
		t.Fatal(err)
	}
	target := fixture.NewFixture(t, false, true)
	sentinel := fixture.Select(`SELECT k FROM admission_anchor`, []string{"k"}, []string{"INT4"}, [][]any{{int64(91)}})
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(`CREATE TABLE admission_anchor(k integer)`, 0))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(`INSERT INTO admission_anchor VALUES(91)`, 1))
	held, err := target.Memory.QueryContext(target.Ctx, sentinel.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var before bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &before); err != nil {
		t.Fatal(err)
	}
	prefix := script.String() + "\n" + `CREATE TYPE public.admission_owned AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=admission_many); CREATE TABLE public.admission_values(k integer); `
	for _, tc := range []struct {
		name, suffix string
		parameter    bool
	}{
		{"same_name_nonconstructor_overload", `CREATE FUNCTION public.admission_owned(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$; INSERT INTO public.admission_values VALUES(public.admission_owned(17));`, false},
		{"subscript_nonconstructor_overload", `CREATE FUNCTION public.admission_owned(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$; CREATE TABLE public.admission_array(k integer[]); INSERT INTO public.admission_array(k[public.admission_owned(1)]) VALUES(7);`, false},
		{"forged_namespace", `CREATE SCHEMA admission_forged; CREATE FUNCTION admission_forged.admission_owned(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$; INSERT INTO public.admission_values VALUES(admission_forged.admission_owned(1,2));`, false},
		{"nonconstructor", `CREATE FUNCTION public.admission_other(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$; INSERT INTO public.admission_values VALUES(public.admission_other(17));`, false},
		{"strict_null_nested_parameter", `INSERT INTO public.admission_values VALUES(public.admission_many(NULL::public.admission_owned,public.admission_owned($1::integer,2)));`, true},
	} {
		t.Run("restore_"+tc.name, func(t *testing.T) {
			input := prefix + tc.suffix
			err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(input))
			if tc.parameter {
				want := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
				want.Position = strconv.Itoa(utf8.RuneCountInString(input[:strings.LastIndex(input, "$1")]) + 1)
				fixture.AssertDiagnostic(t, err, &want)
			} else if err == nil || err.Error() != "stuntgres: invalid restore scalar" {
				t.Fatal("nonconstructor crossed Restore syntax/provider boundary", err)
			}
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, sentinel)
			var after bytes.Buffer
			if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("constructor admission failure changed target dump", err)
			}
		})
	}
	rangeConsumeHeld(t, held, sentinel)
}
