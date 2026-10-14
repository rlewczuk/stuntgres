package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRegCollationStoredControls(t *testing.T) {
	var emitted []byte
	regObjectScenario(t, []string{"rc_store", "rc_moved", "rn_store", "rn_store_renamed", "rn_dependency"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA rc_store", "CREATE SCHEMA rc_moved", "CREATE SCHEMA rn_store", "CREATE SCHEMA rn_dependency",
			`CREATE COLLATION rc_store.target FROM pg_catalog."C"`,
			`CREATE COLLATION rc_store.opaque FROM pg_catalog."C"`,
			`CREATE COLLATION rc_store.dependency FROM pg_catalog."C"`,
			"CREATE DOMAIN rc_store.identifier AS regcollation",
			`CREATE TABLE public.rc_stored(id integer,c regcollation DEFAULT 'rc_store.target'::regcollation,n regnamespace DEFAULT 'rn_store'::regnamespace,CHECK(c='rc_store.target'::regcollation))`,
			`CREATE TABLE public.rc_generated(c regcollation GENERATED ALWAYS AS ('rc_store.target'::regcollation) STORED)`,
			`CREATE TABLE public.rc_recursive(d rc_store.identifier,a regcollation[],n regnamespace[])`,
			`CREATE VIEW public.rc_view AS SELECT 'rc_store.target'::regcollation AS c,'rn_store'::regnamespace AS n`,
			`CREATE VIEW public.rn_dependency_view AS SELECT 'rn_dependency'::regnamespace AS n`,
			`CREATE VIEW public.rc_dependency_view AS SELECT 'rc_store.dependency'::regcollation AS c`,
			"CREATE TABLE public.rc_opaque(c regcollation)")
		for _, q := range []string{"INSERT INTO public.rc_stored(id) VALUES(1)", "INSERT INTO public.rc_generated DEFAULT VALUES", `INSERT INTO public.rc_recursive VALUES('rc_store.target','{rc_store.target,NULL,0,4294967295}','{rn_store,NULL,0,4294967295}')`, `INSERT INTO public.rc_opaque VALUES('rc_store.opaque')`} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 1))
		}
		// The dependency belongs to a scalar stored literal, not an ordinary row.
		_, err := c.ExecContext(p.Ctx, "DROP SCHEMA rn_dependency RESTRICT")
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop schema rn_dependency because other objects depend on it")
		want.Detail = "view rn_dependency_view depends on schema rn_dependency"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		fixture.AssertDiagnostic(t, err, &want)
		_, err = c.ExecContext(p.Ctx, "DROP COLLATION rc_store.dependency RESTRICT")
		want = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop collation rc_store.dependency because other objects depend on it")
		want.Detail = "view rc_dependency_view depends on collation rc_store.dependency"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		fixture.AssertDiagnostic(t, err, &want)
		var opaque int64
		if err := c.QueryRowContext(p.Ctx, "SELECT c::bigint FROM public.rc_opaque").Scan(&opaque); err != nil {
			t.Fatal(err)
		}
		regObjectExec(t, p, b, c, "DROP COLLATION rc_store.opaque", "ALTER COLLATION rc_store.target SET SCHEMA rc_moved", "ALTER COLLATION rc_moved.target RENAME TO renamed", "ALTER SCHEMA rn_store RENAME TO rn_store_renamed")
		regObjectBoth(t, p, b, c, regObjectSelect("SELECT c,c::bigint AS id FROM public.rc_opaque", []string{"c", "id"}, []string{"REGCOLLATION", "INT8"}, [][]any{{strconv.FormatInt(opaque, 10), opaque}}))
		// The dropped value proves opaque rows do not block DROP. Its old OID
		// can name a newly reminted collation on replay, so stable serialization
		// instead uses an independently unknown sentinel, not allocator numbers.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE public.rc_opaque SET c='4294967295'::regcollation", 1))
		check := func(db fixture.Executor) {
			for _, query := range []string{"SELECT c,n FROM public.rc_stored", "SELECT c,n FROM public.rc_view"} {
				regObjectBoth(t, p, b, db, regObjectSelect(query, []string{"c", "n"}, []string{"REGCOLLATION", "REGNAMESPACE"}, [][]any{{"rc_moved.renamed", "rn_store_renamed"}}))
			}
			regObjectBoth(t, p, b, db, regObjectSelect("SELECT c FROM public.rc_generated", []string{"c"}, []string{"REGCOLLATION"}, [][]any{{"rc_moved.renamed"}}))
			regObjectBoth(t, p, b, db, fixture.Select("SELECT (c::oid='rc_moved.renamed'::regcollation::oid) AS c,(n::oid='rn_store_renamed'::regnamespace::oid) AS n FROM public.rc_stored", []string{"c", "n"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			regObjectBoth(t, p, b, db, fixture.Select("SELECT d::text AS d,a::text AS a,n::text AS n FROM public.rc_recursive", []string{"d", "a", "n"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"rc_moved.renamed", "{rc_moved.renamed,NULL,-,4294967295}", "{rn_store_renamed,NULL,-,4294967295}"}}))
			regObjectBoth(t, p, b, db, regObjectSelect("SELECT c,c::bigint AS id FROM public.rc_opaque", []string{"c", "id"}, []string{"REGCOLLATION", "INT8"}, [][]any{{"4294967295", int64(4294967295)}}))
		}
		check(c)
		if b.PG {
			if emitted == nil {
				t.Fatal("actual native dump required for pinned replay")
			}
			regObjectExec(t, p, b, c, "DROP SCHEMA public CASCADE", "CREATE SCHEMA public")
			for _, schema := range []string{"rc_store", "rc_moved", "rn_store_renamed", "rn_dependency"} {
				regObjectExec(t, p, b, c, "DROP SCHEMA "+schema+" CASCADE")
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted replay", err)
			}
			check(c)
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			regObjectExec(t, p, b, c, "CREATE SCHEMA rc_allocator_spacer", "CREATE TABLE rc_allocator_spacer.t(v integer)")
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(emitted, again.Bytes()) {
				t.Fatalf("logical redump changed after remapping\noriginal dump:\n%s\nrestored dump:\n%s", emitted, again.Bytes())
			}
		}
		// Dependent definitions are actually removed by CASCADE, not just ignored.
		regObjectExec(t, p, b, c, "DROP SCHEMA rn_dependency CASCADE")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regObjectError("SELECT n FROM public.rn_dependency_view", "42P01", `relation "public.rn_dependency_view" does not exist`, "15", ""))
		regObjectExec(t, p, b, c, "DROP COLLATION rc_store.dependency CASCADE")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regObjectError("SELECT c FROM public.rc_dependency_view", "42P01", `relation "public.rc_dependency_view" does not exist`, "15", ""))
	})
}
