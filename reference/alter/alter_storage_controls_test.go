package reference_test

import (
	"bytes"
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

func storageDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
	return out.Bytes()
}

func storageSavedSteps() []fixture.Step {
	return []fixture.Step{
		storageMetadata("storage_case", []any{"i", "p", ""}, []any{"renamed", "p", "p"}, []any{"n", "e", "l"}, []any{"retired", "x", ""}),
		storageMetadata("storage_dup", []any{"t", "p", "p"}, []any{"t1", "x", ""}),
		storageMetadata("storage_new_dup", []any{"renamed", "p", "p"}, []any{"renamed1", "p", "p"}),
		storageMetadata("storage_parent", []any{"t", "e", "p"}),
		storageMetadata("storage_child", []any{"t", "x", ""}),
		storageMetadata("storage_child_idx", []any{"t", "x", ""}),
		storageMetadata("storage_swapped", []any{"b", "x", ""}, []any{"a", "x", ""}),
		storageMetadata("storage_swapped_idx", []any{"a", "x", ""}, []any{"b", "x", ""}),
		fixture.Select("SELECT i,renamed,n::text,retired FROM storage_case ORDER BY i", []string{"i", "renamed", "n", "retired"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(7), "payload", "12", nil}}),
		fixture.Select("SELECT attnum,attname,attisdropped,attstorage::text,attcompression::text FROM pg_attribute WHERE attrelid='storage_case'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attisdropped", "attstorage", "attcompression"}, []string{"INT2", "NAME", "BOOL", "TEXT", "TEXT"}, [][]any{{int64(1), "i", false, "p", ""}, {int64(2), "renamed", false, "p", "p"}, {int64(3), "n", false, "e", "l"}, {int64(4), "........pg.dropped.4........", true, "e", "p"}, {int64(5), "retired", false, "x", ""}}),
		fixture.Select("SELECT column_name::text,is_nullable::text,column_default::text FROM information_schema.columns WHERE table_schema='public' AND table_name='storage_case' ORDER BY ordinal_position", []string{"column_name", "is_nullable", "column_default"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"i", "NO", "7"}, {"renamed", "YES", nil}, {"n", "YES", nil}, {"retired", "YES", nil}}),
		fixture.Select("SELECT has_table_privilege('storage_control_reader','storage_case','SELECT') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}),
	}
}

func TestReferenceAlterStorageControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"storage_control_reader"})
	p := fixture.NewScenario(t)
	for _, q := range []string{
		"CREATE ROLE storage_control_reader",
		"CREATE TABLE storage_case(i integer NOT NULL DEFAULT 7,t text,n numeric,retired text)",
		"INSERT INTO storage_case(t,n,retired) VALUES('payload',12,'old')",
		"CREATE INDEX storage_dup ON storage_case(t,t)",
		"ALTER TABLE storage_case ALTER t SET STORAGE PLAIN, ALTER t SET COMPRESSION pglz, ALTER n SET STORAGE EXTERNAL, ALTER n SET COMPRESSION lz4, ALTER retired SET STORAGE EXTERNAL, ALTER retired SET COMPRESSION pglz",
		"ALTER TABLE storage_case RENAME COLUMN t TO renamed",
		"ALTER TABLE storage_case DROP COLUMN retired, ADD COLUMN retired text",
		"CREATE INDEX storage_new_dup ON storage_case(renamed,renamed)",
		"GRANT SELECT ON storage_case TO storage_control_reader",
		"CREATE TABLE storage_parent(t text)",
		"ALTER TABLE storage_parent ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz",
		"CREATE TABLE storage_child() INHERITS(storage_parent)",
		"ALTER TABLE ONLY storage_child ALTER t SET STORAGE DEFAULT, ALTER t SET COMPRESSION default",
		"CREATE INDEX storage_child_idx ON storage_child(t)",
		"CREATE TABLE storage_swapped(a text,b text)",
		"CREATE INDEX storage_swapped_idx ON storage_swapped(a,b)",
		"ALTER TABLE storage_swapped RENAME COLUMN a TO storage_swap_tmp",
		"ALTER TABLE storage_swapped RENAME COLUMN b TO a",
		"ALTER TABLE storage_swapped RENAME COLUMN storage_swap_tmp TO b",
	} {
		n := int64(0); if strings.HasPrefix(q, "INSERT ") { n = 1 }; p.Step(t, fixture.Exec(q, n))
	}
	for _, s := range storageSavedSteps() { p.Step(t, s) }
	before := storageDump(t, p)
	private, err := p.Memory.Conn(p.Ctx); if err != nil { t.Fatal(err) }
	if _, err := private.ExecContext(p.Ctx, "BEGIN"); err != nil { t.Fatal(err) }
	if _, err := private.ExecContext(p.Ctx, "ALTER TABLE storage_case ALTER renamed SET STORAGE MAIN"); err != nil { t.Fatal(err) }
	if got := storageDump(t, p); !bytes.Equal(got, before) { t.Fatal("Dump exposed private properties") }
	if _, err := private.ExecContext(p.Ctx, "ROLLBACK"); err != nil { t.Fatal(err) }
	if err := private.Close(); err != nil { t.Fatal(err) }
	unchanged := func() {
		t.Helper()
		if got := storageDump(t, p); !bytes.Equal(got, before) { t.Fatal("failed Restore changed committed dump") }
		for _, s := range storageSavedSteps() { fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, s) }
	}
	prefix := `CREATE TABLE public.restore_partial(t text); ALTER TABLE ONLY public.restore_partial ALTER COLUMN t SET STORAGE EXTERNAL; `
	for i, suffix := range []string{
		`ALTER TABLE ONLY public.restore_partial ALTER COLUMN t SET COMPRESSION bogus;`,
		`ALTER TABLE ONLY public.restore_partial ALTER COLUMN absent SET STORAGE MAIN;`,
		`UPDATE pg_catalog.pg_attribute SET attstorage='e',attcompression='p' WHERE attrelid='"public"."restore_partial"'::regclass AND attnum=1;`,
		`CREATE INDEX restore_idx ON public.restore_partial(t); UPDATE pg_catalog.pg_attribute SET attstorage='z',attcompression='p' WHERE attrelid='"public"."restore_idx"'::regclass AND attnum=1;`,
		`CREATE INDEX restore_idx ON public.restore_partial(t); UPDATE pg_catalog.pg_attribute SET attstorage='e',attcompression='p',attacl=NULL WHERE attrelid='"public"."restore_idx"'::regclass AND attnum=1;`,
		`UPDATE pg_catalog.pg_attribute SET attstorage='e',attcompression='p' WHERE attnum>0;`,
		`UPDATE pg_catalog.pg_attribute SET attstorage=$1,attcompression='p' WHERE attrelid='"public"."restore_idx"'::regclass AND attnum=1;`,
		`UPDATE pg_catalog.pg_attribute AS a SET attstorage='e',attcompression='p' WHERE attrelid='"public"."restore_idx"'::regclass AND attnum=1 RETURNING attnum;`,
		`CREATE INDEX restore_idx ON public.restore_partial(t); UPDATE pg_catalog.pg_attribute SET attname=$1 WHERE attrelid='"public"."restore_idx"'::regclass AND attnum=1;`,
		`CREATE INDEX restore_idx ON public.restore_partial(t); UPDATE pg_catalog.pg_attribute SET attstorage='e',attcompression='p',attname='saved',attacl=NULL WHERE attrelid='"public"."restore_idx"'::regclass AND attnum=1;`,
		`UPDATE pg_catalog.pg_attribute SET attname='saved' WHERE attrelid='"public"."restore_partial"'::regclass AND attnum=1;`,
	} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+suffix))
		if err == nil { t.Fatalf("invalid Restore accepted: %s", suffix) }
		if i < 2 {
			d := fixture.ControlsReferenceDiagnostic("22023", `invalid compression method "bogus"`)
			if i == 1 { d = fixture.ControlsReferenceDiagnostic("42703", `column "absent" of relation "restore_partial" does not exist`) }
			fixture.AssertDiagnostic(t, err, &d)
		}
		unchanged()
	}
	readErr := errors.New("storage restore reader failure")
	if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader(prefix), addConstraintFailedReader{readErr})); !errors.Is(err, readErr) { t.Fatal(err) }
	unchanged()
	ctx, cancel := context.WithCancel(p.Ctx); cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before)); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	unchanged()
	old, err := p.Native.QueryContext(p.Ctx, "SELECT i,renamed FROM storage_case ORDER BY i"); if err != nil { t.Fatal(err) }; defer old.Close()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name()+"/independent"); if err != nil { t.Fatal(err) }
	cloneNative := sql.OpenDB(connector)
	defer cloneNative.Close()
	clone, recorder := fixture.RecordedNativePool(t, connector)
	fixture.RunStep(t, p.Ctx, clone, recorder, fixture.Exec("CREATE ROLE storage_control_reader", 0))
	if err := stuntgres.Restore(p.Ctx, cloneNative, bytes.NewReader(before)); err != nil { t.Fatal(err) }
	for _, s := range storageSavedSteps() { fixture.RunStep(t, p.Ctx, clone, recorder, s) }
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil { t.Fatal(err) }
	ct, err := old.ColumnTypes(); if err != nil { t.Fatal(err) }
	fixture.TableDDLColumnTypes(t, false, ct, []string{"i", "renamed"}, []string{"INT4", "TEXT"}, []bool{false, true})
	if !old.Next() { t.Fatal("lost pre-Reset row", old.Err()) }
	var i int64; var value string
	if err := old.Scan(&i, &value); err != nil || !reflect.DeepEqual([]any{i, value}, []any{int64(7), "payload"}) { t.Fatal(i, value, err) }
	if old.Next() || old.Err() != nil { t.Fatal("old cursor termination", old.Err()) }
	if err := old.Close(); err != nil { t.Fatal(err) }
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE storage_case,storage_child,storage_parent,storage_swapped CASCADE"); err != nil { t.Fatal(err) }
		if _, err := p.Postgres.ExecContext(p.Ctx, string(before)); err != nil { t.Fatal("PostgreSQL replay", err) }
	}
	if got := storageDump(t, p); !bytes.Equal(got, before) { t.Fatal("unstable property redump") }
	for _, s := range storageSavedSteps() { p.Step(t, s) }
}

