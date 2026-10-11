package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func ArrayReferenceConsumeHeld(t *testing.T, rows *sql.Rows, want [][]any, types []string) {
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
			values[i] = Normalize(value)
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

func ArrayReferenceNativeRoundtrip(t *testing.T, p *Pair, c *sql.Conn, b Backend, check Step, targetSetup ...Step) {
	t.Helper()
	if b.PG {
		t.Fatal("native controls called on PostgreSQL")
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	held, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if check.CheckMetadata != nil {
		check.CheckMetadata(t, held)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	RunStep(t, p.Ctx, c, b.Recorder, Exec("CREATE TABLE public.array_replacement (v TEXT[])", 0))
	RunStep(t, p.Ctx, c, b.Recorder, Exec("INSERT INTO public.array_replacement VALUES ('{new}')", 1))
	ArrayReferenceConsumeHeld(t, held, check.Rows, check.Types)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	RunStep(t, p.Ctx, c, b.Recorder, check)
	RunStep(t, p.Ctx, c, b.Recorder, Prepared(check))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatalf("dump changed after replacement\nfirst=%s\nsecond=%s", dump.String(), redump.String())
	}
	target := NewFixture(t, false, true)
	for _, step := range targetSetup {
		RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
	}
	// Deliberately advance the target's relation/domain allocator independently.
	for _, q := range []string{"CREATE TABLE public.array_spacer (v INT)", "CREATE DOMAIN public.array_spacer_domain AS TEXT"} {
		RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, Exec(q, 0))
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
	RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, Prepared(check))
	var targetDump bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &targetDump); err != nil {
		t.Fatal(err)
	}
	if targetDump.String() != dump.String() {
		t.Fatalf("independent target dump=%s want %s", targetDump.String(), dump.String())
	}
	RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, Exec("CREATE TABLE public.array_sentinel (v INT[])", 0))
	RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, Exec("INSERT INTO public.array_sentinel VALUES ('[0:1]={42,NULL}')", 1))
	sentinel := ArrayReferenceSelect("SELECT v FROM public.array_sentinel", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"[0:1]={42,NULL}"}}, false)
	for _, tc := range []struct{ input, code string }{{"{1,,2}", "22P02"}, {"{{1},{2,3}}", "22P02"}, {"{oops}", "22P02"}} {
		sql := "CREATE TABLE public.array_staged (a INT[]); INSERT INTO public.array_staged VALUES ('{7}'); INSERT INTO public.array_staged VALUES (" + ArrayReferenceQuote(tc.input) + ");"
		err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(sql))
		State(t, err, tc.code)
		RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, sentinel)
		RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
		RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, Error("SELECT a FROM public.array_staged", "42P01"))
	}
	ioFailure := errors.New("array control IO failure")
	if err := stuntgres.Dump(target.Ctx, target.Native, CatalogControlFailingWriter{ioFailure}); !errors.Is(err, ioFailure) {
		t.Fatalf("writer failure=%v", err)
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, CatalogControlFailingReader{ioFailure}); !errors.Is(err, ioFailure) {
		t.Fatalf("reader failure=%v", err)
	}
	ctx, cancel := context.WithCancel(target.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore=%v", err)
	}
	if _, err := target.Memory.ExecContext(ctx, "INSERT INTO public.array_sentinel VALUES ('{99}')"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled array insert=%v", err)
	}
	RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, sentinel)
	RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
}
