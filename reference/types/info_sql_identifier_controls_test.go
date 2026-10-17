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

func infoSQLIdentifierDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	script := dump.String()
	if !strings.Contains(script, `"information_schema"."sql_identifier"`) || strings.Contains(strings.ToUpper(script), "CREATE DOMAIN") {
		t.Fatalf("Dump lost builtin domain provenance: %s", script)
	}
	return script
}

func TestReferenceInfoSQLIdentifierNativeControls(t *testing.T) {
	infoSQLIdentifierScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(fixture.Exec(
			"INSERT INTO public.info_sql_identifier_witness VALUES (1,$1),(2,$2),(3,$3),(4,$4),(5,$5),(6,$6)", 6,
			strings.Repeat("a", 64), strings.Repeat("a", 61)+"éZ", "MiXeD", "O'Brien\\tail\n", "", nil)))
		want := [][]any{{strings.Repeat("a", 63)}, {strings.Repeat("a", 61) + "é"}, {"MiXeD"}, {"O'Brien\\tail\n"}, {""}, {nil}}
		stored := infoSQLIdentifierStored(backend.PG, want)
		infoSQLIdentifierBoth(t, p, conn, recorder, stored)
		infoSQLIdentifierIdentity(t, p, backend, conn, recorder)
		// SQL acceptance is shared; PostgreSQL has no native control API.
		if backend.PG {
			return
		}
		script := infoSQLIdentifierDump(t, p)
		infoSQLIdentifierBoth(t, p, conn, recorder, stored)
		retained, err := backend.DB.QueryContext(p.Ctx, stored.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		infoSQLIdentifierColumnTypes(t, columns, false)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Error(stored.Query, "42P01"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoSQLIdentifierBoth(t, p, conn, recorder, stored)
		infoSQLIdentifierIdentity(t, p, backend, conn, recorder)
		var old [][]any
		for retained.Next() {
			var value any
			if err := retained.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if len(old) >= len(want) {
				t.Fatal("extra retained row")
			}
			stored.CheckRaw(t, 0, len(old), value)
			old = append(old, []any{fixture.Normalize(value)})
		}
		if err := retained.Err(); err != nil || !reflect.DeepEqual(old, want) {
			t.Fatal("retained rows", old, err)
		}
		infoSQLIdentifierColumnTypes(t, columns, false)
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		infoSQLIdentifierColumnTypes(t, columns, false)

		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE info_sql_identifier_remap_spacer", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		check := func() {
			infoSQLIdentifierBoth(t, target, target.Memory, target.MemoryRecord, stored)
			infoSQLIdentifierIdentity(t, target, fixture.Backend{PG: false}, target.Memory, target.MemoryRecord)
		}
		check()
		second := infoSQLIdentifierDump(t, target)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(second)); err != nil {
			t.Fatal(err)
		}
		check()
		infoSQLIdentifierBoth(t, p, conn, recorder, stored)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.info_sql_identifier_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.info_sql_identifier_sentinel VALUES (42)", 1))
		bad := "CREATE TABLE public.info_sql_identifier_staged(id integer,v information_schema.sql_identifier); INSERT INTO public.info_sql_identifier_staged VALUES (1,'valid'),('bad','later');"
		err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
		fixture.State(t, err, "22P02")
		var diagnostic *stuntgres.Error
		if !errors.As(err, &diagnostic) || diagnostic.Message != `invalid input syntax for type integer: "bad"` {
			t.Fatalf("failed Restore diagnostic=%v", err)
		}
		checkSentinel := func() {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.info_sql_identifier_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT v FROM public.info_sql_identifier_staged", "42P01"))
			check()
		}
		checkSentinel()
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(script)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		checkSentinel()
	})
}

func TestReferenceInfoSQLIdentifierFeatureSmoke(t *testing.T) {
	infoSQLIdentifierScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(fixture.Exec(
			"INSERT INTO public.info_sql_identifier_witness VALUES (1,$1),(2,$2),(3,$3)", 3,
			"MiXeD", strings.Repeat("a", 61)+"éZ", nil)))
		stored := infoSQLIdentifierStored(backend.PG, [][]any{{"MiXeD"}, {strings.Repeat("a", 61) + "é"}, {nil}})
		infoSQLIdentifierBoth(t, p, conn, recorder, stored)
		infoSQLIdentifierIdentity(t, p, backend, conn, recorder)
		s := infoSQLIdentifierInvalid("INSERT INTO public.info_sql_identifier_witness VALUES (90,'ok'),(91,$1)", "0xff", strings.Repeat("x", 63)+"\xff")
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
			infoSQLIdentifierBoth(t, p, conn, recorder, stored)
		}
		if backend.PG {
			return
		}
		script := infoSQLIdentifierDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoSQLIdentifierBoth(t, p, conn, recorder, stored)
		infoSQLIdentifierIdentity(t, p, backend, conn, recorder)
	})
}
