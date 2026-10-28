package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func arrayCompareStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		"CREATE DOMAIN public.ac_consumer_array AS INT[]",
		"CREATE TABLE public.ac_consumer (id INT,x INT,a public.ac_consumer_array,d BOOL DEFAULT (2 IN (1,2,NULL)),g BOOL GENERATED ALWAYS AS (x=ANY(a)) STORED,h BOOL GENERATED ALWAYS AS (x NOT IN (1,2,NULL)) STORED)",
		"CREATE INDEX ac_consumer_index ON public.ac_consumer ((x=ANY(a)))",
		"CREATE INDEX ac_consumer_list_index ON public.ac_consumer ((x IN (1,2,NULL)))",
		"CREATE TABLE public.ac_guard (id INT,x INT,CONSTRAINT ac_guard_valid CHECK (x IN (1,2)))",
		"CREATE TABLE public.ac_array_guard (id INT,x INT,a INT[],CONSTRAINT ac_array_guard_valid CHECK (x=ANY(a)))",
		"CREATE VIEW public.ac_consumer_view AS SELECT id,x IN (1,2,NULL) AS listed,x=SOME(a) AS matched,x<>ALL(a) AS absent,d,g,h FROM public.ac_consumer",
		"CREATE TABLE public.ac_text_consumer (v TEXT,d BOOL DEFAULT ('a' COLLATE \"C\" IN ('a','b')),g BOOL GENERATED ALWAYS AS (v COLLATE \"C\"=ANY(ARRAY['a','b'])) STORED)",
		"CREATE TYPE public.ac_frozen_enum AS ENUM ('1','2')",
		"CREATE TABLE public.ac_frozen (id INT,e public.ac_frozen_enum,i INT,d BOOL DEFAULT ('1' IN ('2'::public.ac_frozen_enum,1)),g BOOL GENERATED ALWAYS AS ('1' IN (e,i)) STORED,h BOOL GENERATED ALWAYS AS ('1' NOT IN (e,i)) STORED)",
		"CREATE VIEW public.ac_frozen_view AS SELECT id,'1' IN (e,i) AS listed,'1' NOT IN (e,i) AS unlisted,d,g,h FROM public.ac_frozen",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO public.ac_consumer(id,x,a) VALUES (1,1,'{1,2,NULL}'),(2,3,'{1,2,NULL}')", 2))
	run(fixture.Prepared(fixture.Exec("INSERT INTO public.ac_consumer(id,x,a) VALUES ($1,$2,$3)", 1, 3, nil, "{}")))
	run(fixture.Exec("INSERT INTO public.ac_consumer(id,x,a) VALUES (4,NULL,'{1}'),(5,1,NULL)", 2))
	run(fixture.Exec("INSERT INTO public.ac_guard VALUES (1,1),(2,NULL)", 2))
	run(fixture.Exec("INSERT INTO public.ac_frozen(id,e,i) VALUES (1,'2',1),(2,'1',2),(3,'2',2),(4,NULL,2)", 4))
	run(fixture.Exec("INSERT INTO public.ac_array_guard VALUES (1,1,'{1}'),(2,NULL,'{1}')", 2))
	run(fixture.Exec("INSERT INTO public.ac_text_consumer(v) VALUES ('a'),('z'),(NULL)", 3))
	return fixture.Select("SELECT id,listed,matched,absent,d,g,h FROM public.ac_consumer_view ORDER BY id",
		[]string{"id", "listed", "matched", "absent", "d", "g", "h"},
		[]string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
		[][]any{
			{int64(1), true, true, false, true, true, false},
			{int64(2), nil, nil, nil, true, nil, nil},
			{int64(3), nil, false, true, true, false, nil},
			{int64(4), nil, nil, nil, true, nil, nil},
			{int64(5), true, nil, nil, true, nil, false},
		})
}

// Each copied unknown left literal is independently enum- or integer-typed.
// Frozen rendering must preserve those distinct scalar authorities.
func arrayCompareFrozenCheck() fixture.Step {
	return fixture.Select("SELECT id,listed,unlisted,d,g,h FROM public.ac_frozen_view ORDER BY id",
		[]string{"id", "listed", "unlisted", "d", "g", "h"},
		[]string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
		[][]any{{int64(1), true, false, true, true, false}, {int64(2), true, false, true, true, false}, {int64(3), false, true, true, false, true}, {int64(4), nil, nil, true, nil, nil}})
}

func TestReferenceArrayCompareStoredConsumers(t *testing.T) {
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		check := arrayCompareStoredSetup(t, p, b, c)
		run(check)
		run(fixture.Prepared(check))
		stmt, err := c.PrepareContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: check.Query}
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT x IN (1,2,NULL) AS listed,count(*) AS n FROM public.ac_consumer GROUP BY x IN (1,2,NULL) ORDER BY listed", []string{"listed", "n"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(2)}, {nil, int64(3)}}),
			fixture.Select("SELECT count(x=ANY(a)) AS n,count(*) FILTER (WHERE x=ANY(a)) AS matched FROM public.ac_consumer", []string{"n", "matched"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(1)}}),
			fixture.Select("SELECT (count(*) IN (5,6)) AS listed,(count(*)=ANY(ARRAY[5::bigint,6::bigint])) AS matched FROM public.ac_consumer", []string{"listed", "matched"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
			fixture.Select("SELECT l.id AS id FROM public.ac_consumer AS l JOIN public.ac_guard AS r ON l.x IN (r.x,2) WHERE l.id IN (1,5) ORDER BY l.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}}),
			fixture.Select("SELECT c.id FROM public.ac_consumer AS c WHERE EXISTS (SELECT 1 FROM public.ac_guard AS r WHERE r.x=ANY(c.a)) ORDER BY c.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		text := fixture.Select("SELECT v,d,g FROM public.ac_text_consumer ORDER BY v", []string{"v", "d", "g"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"a", true, true}, {"z", true, false}, {nil, true, nil}})
		run(text)
		run(fixture.Prepared(text))
		frozen := arrayCompareFrozenCheck()
		run(frozen)
		run(fixture.Prepared(frozen))
		frozenStmt, err := c.PrepareContext(p.Ctx, frozen.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer frozenStmt.Close()
		for range 2 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: frozenStmt, SQL: frozen.Query}, b.Recorder, frozen)
		}
		for _, tc := range []struct{ query, table, constraint, detail string }{
			{"INSERT INTO public.ac_guard VALUES (9,3)", "ac_guard", "ac_guard_valid", "Failing row contains (9, 3)."},
			{"INSERT INTO public.ac_array_guard VALUES (9,3,'{1,2}')", "ac_array_guard", "ac_array_guard_valid", "Failing row contains (9, 3, {1,2})."},
		} {
			d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "`+tc.table+`" violates check constraint "`+tc.constraint+`"`)
			d.Detail, d.Schema, d.Table, d.Constraint = tc.detail, "public", tc.table, tc.constraint
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
			}
		}
		run(fixture.Select("UPDATE public.ac_consumer SET x=2 WHERE x=ANY(a) RETURNING id,x IN (1,2) AS listed,g,h", []string{"id", "listed", "g", "h"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, false}}))
		run(fixture.Prepared(fixture.Select("DELETE FROM public.ac_consumer WHERE x=ALL(a) RETURNING id,x IN (1,2) AS listed", []string{"id", "listed"}, []string{"INT4", "BOOL"}, [][]any{{int64(3), nil}})))
		check.Rows = [][]any{{int64(1), true, true, false, true, true, false}, {int64(2), nil, nil, nil, true, nil, nil}, {int64(4), nil, nil, nil, true, nil, nil}, {int64(5), true, nil, nil, true, nil, false}}
		run(check)
		run(fixture.Exec("ALTER TABLE public.ac_consumer RENAME COLUMN a TO choices", 0))
		run(fixture.Exec("ALTER TABLE public.ac_consumer RENAME TO ac_consumer_renamed", 0))
		run(check)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
	})
}

func TestReferenceArrayCompareAuthorityAndControls(t *testing.T) {
	t.Run("authority", arrayCompareConsumerAuthority)
	t.Run("controls", arrayCompareConsumerControls)
}

func arrayCompareConsumerAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ac_consumer_reader"})
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		for _, q := range []string{"CREATE ROLE ac_consumer_reader", "CREATE TABLE public.ac_acl (visible INT,hidden INT,choices INT[])", "GRANT SELECT(visible) ON public.ac_acl TO ac_consumer_reader"} {
			run(fixture.Exec(q, 0))
		}
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ac_consumer_reader", 0))
		}
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec("INSERT INTO public.ac_acl VALUES (1,2,'{1}')", 1))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION ac_consumer_reader", 0))
			for _, q := range []string{
				"SELECT visible IN (visible,hidden) AS v FROM public.ac_acl",
				"SELECT visible=ANY(choices) AS v FROM public.ac_acl",
				"SELECT visible=ANY(ARRAY[visible,hidden]) AS v FROM public.ac_acl WHERE FALSE",
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ac_acl"))
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					run(s)
				}
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT SELECT(hidden,choices) ON public.ac_acl TO ac_consumer_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ac_consumer_reader", 0))
		for _, q := range []string{"SELECT visible IN (visible,hidden) AS v FROM public.ac_acl", "SELECT visible=ANY(choices) AS v FROM public.ac_acl", "SELECT visible=ANY(ARRAY[visible,hidden]) AS v FROM public.ac_acl"} {
			s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, q := range []string{"SELECT 1 IN (1,missing) FROM public.ac_acl WHERE FALSE", "SELECT 1=ANY(ARRAY[1,missing]) FROM public.ac_acl WHERE FALSE"} {
			d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			d.Position = fixture.Position(strings.Index(q, "missing") + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows, s.Prepared = true, prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		for _, q := range []string{
			"CREATE TABLE public.ac_dependency (v INT)",
			"CREATE TABLE public.ac_dependency_owner (v INT,d BOOL DEFAULT (0::oid IN (0::oid,'public.ac_dependency'::regclass::oid)),g BOOL GENERATED ALWAYS AS (0::oid=ANY(ARRAY[0::oid,'public.ac_dependency'::regclass::oid])) STORED)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO public.ac_dependency_owner(v) VALUES (1)", 1))
		dep := fixture.Select("SELECT d,g FROM public.ac_dependency_owner", []string{"d", "g"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		run(dep)
		run(fixture.Exec("ALTER TABLE public.ac_dependency RENAME TO ac_dependency_renamed", 0))
		run(dep)
		run(fixture.Error("DROP TABLE public.ac_dependency_renamed", "2BP01"))
		run(dep)
	})
}

func arrayCompareConsumerControls(t *testing.T) {
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		check := arrayCompareStoredSetup(t, p, b, c)
		run(check)
		run(fixture.Exec("CREATE TABLE public.ac_atomic (id INT,v BOOL)", 0))
		run(fixture.Exec("INSERT INTO public.ac_atomic VALUES (0,TRUE)", 1))
		unchanged := fixture.Select("SELECT id,v FROM public.ac_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), true}})
		run(fixture.Exec("CREATE TABLE public.ac_atomic_source (id INT,n TEXT,a TEXT)", 0))
		run(fixture.Exec("INSERT INTO public.ac_atomic_source VALUES (1,'1','{1}'),(2,'oops','{1,,2}')", 2))
		for _, tc := range []struct {
			query      string
			diagnostic fixture.Diagnostic
		}{
			{"INSERT INTO public.ac_atomic SELECT id,id IN (0,n::int) FROM public.ac_atomic_source ORDER BY id", fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "oops"`)},
			{"INSERT INTO public.ac_atomic SELECT id,1=ANY(a::int[]) FROM public.ac_atomic_source ORDER BY id", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `malformed array literal: "{1,,2}"`, Detail: `Unexpected "," character.`}},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, tc.diagnostic)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(unchanged)
			}
		}
		// Bounded VALUES scalar replay admits the existing qualified-operator seam.
		q := "INSERT INTO public.ac_atomic VALUES (1,1 OPERATOR(pg_catalog.=) ANY($1::int[])),(2,1 OPERATOR(pg_catalog.=) ANY($2::int[]))"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		d.Detail = `Unexpected "," character.`
		d.Context = "unnamed portal parameter $2 = '...'"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d, "{1}", "{1,,2}")
			s.ErrorStage = "execute"
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			} else {
				run(s)
			}
			run(unchanged)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "{1}", "{2,NULL}"))
		unchanged.Rows = [][]any{{int64(0), true}, {int64(1), true}, {int64(2), nil}}
		run(unchanged)
		// Pool acquisition rejects precanceled work deterministically in both modes;
		// the physical native owner is additionally exercised below.
		cancelStmt, err := b.DB.PrepareContext(p.Ctx, "SELECT 1=ANY($1::int[]) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer cancelStmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, "SELECT 1 IN (1,2) AS v")
		if rows != nil {
			rows.Close()
			t.Fatal("precanceled query published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("precanceled query", err)
		}
		rows, err = cancelStmt.QueryContext(ctx, "{1}")
		if rows != nil {
			rows.Close()
			t.Fatal("precanceled held query published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("precanceled held query", err)
		}
		run(unchanged)
		run(check)
		// Allocate and remove source-only relations before capturing the owner OID.
		// Dump excludes these holes; Restore builds a fresh catalog, so destination
		// spacers would be discarded and could not establish the remapping witness.
		for i := range 16 {
			name := fmt.Sprintf("public.ac_source_spacer_%d", i)
			run(fixture.Exec("CREATE TABLE "+name+" (v INT)", 0))
			run(fixture.Exec("DROP TABLE "+name, 0))
		}
		run(fixture.Exec("CREATE TABLE public.ac_owner (marker OID,g BOOL GENERATED ALWAYS AS (marker IN (0::oid,tableoid)) STORED,h BOOL GENERATED ALWAYS AS (marker=ANY(ARRAY[0::oid,tableoid])) STORED)", 0))
		var owner int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='ac_owner'").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("INSERT INTO public.ac_owner(marker) VALUES ($1)", 1, owner))
		ownerCheck := fixture.Select("SELECT g,h FROM public.ac_owner", []string{"g", "h"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		run(ownerCheck)
		if b.PG {
			return // Native catalog replacement has no PostgreSQL control API.
		}
		rows, err = c.QueryContext(ctx, check.Query)
		if rows != nil {
			rows.Close()
			t.Fatal("native precanceled owner published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("native precanceled owner", err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		// Replay the actual unmodified emitted SQL, not a handcrafted PG analogue.
		if p.Postgres != nil {
			defer func() {
				for _, q := range []string{"DROP TABLE IF EXISTS public.ac_owner,public.ac_atomic,public.ac_atomic_source,public.ac_consumer,public.ac_guard,public.ac_array_guard,public.ac_text_consumer,public.ac_frozen CASCADE", "DROP DOMAIN IF EXISTS public.ac_consumer_array CASCADE", "DROP TYPE IF EXISTS public.ac_frozen_enum CASCADE"} {
					if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL emitted-SQL replay", err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(check))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, arrayCompareFrozenCheck())
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(arrayCompareFrozenCheck()))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, unchanged)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO public.ac_consumer(id,x,a) VALUES (6,1,'{1}')", 1))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT d,g,h FROM public.ac_consumer WHERE id=6", []string{"d", "g", "h"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}))
		}
		retained, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var restoredOwner int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='ac_owner'").Scan(&restoredOwner); err != nil {
			t.Fatal(err)
		}
		if restoredOwner == owner {
			t.Fatal("owner-remapping witness requires a different restored relation identity")
		}
		fixture.ArrayReferenceConsumeHeld(t, retained, check.Rows, check.Types)
		run(check)
		run(fixture.Prepared(check))
		run(arrayCompareFrozenCheck())
		run(fixture.Prepared(arrayCompareFrozenCheck()))
		run(unchanged)
		// Unlike numeric marker data, tableoid authority belongs to the restored owner.
		ownerCheck.Rows = [][]any{{false, false}}
		run(ownerCheck)
		run(fixture.Exec("INSERT INTO public.ac_consumer(id,x,a) VALUES (6,1,'{1}')", 1))
		run(fixture.Select("SELECT d,g,h FROM public.ac_consumer WHERE id=6", []string{"d", "g", "h"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		for _, script := range []string{
			"CREATE TABLE public.ac_rejected (v BOOL DEFAULT (1 IN (1,$1)));",
			"CREATE TABLE public.ac_rejected (v BOOL); INSERT INTO public.ac_rejected VALUES (1 OPERATOR(pg_catalog.=) ANY(ARRAY[1,$1]));",
		} {
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
			d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
			d.Position = fixture.Position(strings.Index(script, "$1") + 1)
			fixture.AssertDiagnostic(t, err, &d)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("rejected predicate Restore changed catalog")
			}
		}
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled predicate Restore", err)
		}
		run(ownerCheck)
		run(unchanged)
	})
}
