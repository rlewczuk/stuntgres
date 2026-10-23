package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleControls(t *testing.T) {
	t.Run("mixed_current_names_owners_and_executable_recipes", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tl_replay_owner", "tl_acl_reader"})
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			domainIdentityDDL(t, p, c, r, "CREATE ROLE tl_replay_owner", "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "SET search_path TO tl_src,public",
				"CREATE EXTENSION cube WITH SCHEMA tl_src", "CREATE EXTENSION intarray WITH SCHEMA tl_src", "CREATE EXTENSION citext WITH SCHEMA tl_src", "CREATE EXTENSION earthdistance WITH SCHEMA tl_src",
				"CREATE TYPE tl_src.mood AS ENUM ('z','a')", "CREATE DOMAIN tl_src.positive AS integer DEFAULT 7 CONSTRAINT positive CHECK(VALUE>0)", "CREATE DOMAIN tl_src.child AS tl_src.positive", "CREATE TYPE tl_src.pair AS(n tl_src.positive,note text)", "CREATE DOMAIN tl_src.box AS tl_src.pair", "CREATE DOMAIN tl_src.vector AS tl_src.positive[]",
				"CREATE TABLE public.tl_replay(id integer,m tl_src.mood,d tl_src.child,p tl_src.box,a tl_src.vector,c tl_src.cube,q tl_src.query_int,ci tl_src.citext,e tl_src.earth)",
				"CREATE TABLE public.tl_recipes(v integer DEFAULT 8::tl_src.positive CONSTRAINT valid CHECK(v>1::tl_src.positive))",
				"CREATE VIEW public.tl_live AS SELECT id,d+1 AS d FROM public.tl_replay",
				"CREATE FUNCTION public.tl_identity(tl_src.positive) RETURNS tl_src.positive LANGUAGE SQL AS 'SELECT $1'")
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.tl_replay VALUES(1,'z',7,'(7,x)','[0:1]={7,NULL}','(1,2)','1','MiXeD','(6378168,0,0)'),(2,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
			for _, name := range []string{"mood", "positive", "pair", "cube", "query_int", "citext", "earth"} {
				domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src."+name+" RENAME TO moved_"+name, "ALTER TYPE tl_src.moved_"+name+" SET SCHEMA tl_dst", "ALTER TYPE tl_dst.moved_"+name+" OWNER TO tl_replay_owner")
			}
			domainIdentityDDL(t, p, c, r, "SET search_path TO public")
			check := func(pair *fixture.Pair, x fixture.Executor, record *fixture.Recorder) {
				fixture.RunStep(t, pair.Ctx, x, record, typeLifecycleReplayRows())
				fixture.RunStep(t, pair.Ctx, x, record, fixture.Prepared(typeLifecycleReplayRows()))
				fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT id,d FROM public.tl_live ORDER BY id", []string{"id", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(2), nil}}))
				fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT public.tl_identity(9::tl_dst.moved_positive) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
				fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT typname,pg_get_userbyid(typowner) AS owner FROM pg_type WHERE typnamespace='tl_dst'::regnamespace::oid AND typname NOT LIKE '\\_%' ORDER BY typname", []string{"typname", "owner"}, []string{"NAME", "NAME"}, [][]any{{"moved_citext", "tl_replay_owner"}, {"moved_cube", "tl_replay_owner"}, {"moved_earth", "tl_replay_owner"}, {"moved_mood", "tl_replay_owner"}, {"moved_pair", "tl_replay_owner"}, {"moved_positive", "tl_replay_owner"}, {"moved_query_int", "tl_replay_owner"}}))
				fixture.RunStep(t, pair.Ctx, x, record, fixture.Exec("INSERT INTO public.tl_recipes DEFAULT VALUES", 1))
				fixture.RunStep(t, pair.Ctx, x, record, fixture.Select("SELECT v FROM public.tl_recipes ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
				fixture.RunStep(t, pair.Ctx, x, record, fixture.Exec("DELETE FROM public.tl_recipes", 1))
				want := fixture.ControlsReferenceDiagnostic("23514", `value for domain tl_src.child violates check constraint "positive"`)
				want.Schema = "tl_src"
				want.DataType = "child"
				want.Constraint = "positive"
				fixture.RunStep(t, pair.Ctx, x, record, fixture.ErrorDiagnostic("INSERT INTO public.tl_replay(id,d) VALUES(3,0)", want))
			}
			check(p, c, r)
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			// Restore executes dependency-ordered current-name recipes, not a
			// successful read of values saved before the identity changes.
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(p, c, r)
			var second bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
				t.Fatal(err)
			}
			if dump.String() != second.String() {
				t.Fatal("representable dump not stable after replay")
			}
			target := fixture.NewFixture(t, false, true)
			domainIdentityDDL(t, target, target.Memory, target.MemoryRecord, "CREATE ROLE tl_spacer", "CREATE ROLE tl_replay_owner")
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(target, target.Memory, target.MemoryRecord)
			domainIdentityDDL(t, target, target.Memory, target.MemoryRecord, "CREATE TABLE public.tl_sentinel(v integer)")
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.tl_sentinel VALUES(42)", 1))
			late := dump.String() + "\nCREATE TABLE public.tl_invalid(v integer); INSERT INTO public.tl_invalid VALUES('bad');"
			fixture.State(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(late)), "22P02")
			readErr := errors.New("type lifecycle reader failure")
			if err := stuntgres.Restore(target.Ctx, target.Native, io.MultiReader(strings.NewReader(dump.String()), typeLifecycleErrorReader{readErr})); !errors.Is(err, readErr) {
				t.Fatalf("reader error=%v", err)
			}
			ctx, cancel := context.WithCancel(target.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled restore=%v", err)
			}
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.tl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			check(target, target.Memory, target.MemoryRecord)
			typeLifecycleCount(t, target, target.Memory, target.MemoryRecord, "SELECT count(*) AS n FROM pg_class WHERE relnamespace='public'::regnamespace::oid AND relname='tl_invalid'", 0)
			// Explicit nonportable ACL refusal must be pre-output.
			domainIdentityDDL(t, p, c, r, "CREATE ROLE tl_acl_reader", "GRANT USAGE ON TYPE tl_dst.moved_mood TO tl_acl_reader")
			var rejected bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &rejected); err == nil || rejected.Len() != 0 {
				t.Fatalf("ACL dump error=%v bytes=%d", err, rejected.Len())
			}
		})
	})
	t.Run("extension_canonical_name_cycle_and_companion_displacement", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			domainIdentityDDL(t, p, c, r, "CREATE EXTENSION cube", "CREATE EXTENSION citext", "CREATE TYPE public._cube AS ENUM ()", "ALTER TYPE public.cube RENAME TO parked", "ALTER TYPE public.citext RENAME TO cube", "ALTER TYPE public.parked RENAME TO citext", "CREATE DOMAIN public.tl_cube_array AS public.citext[]", "CREATE TABLE public.tl_cycle(c public.citext,ci public.cube)")
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.tl_cycle VALUES('(1,2)','MiXeD')", 1))
			check := func() {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT c::text,ci::text FROM public.tl_cycle", []string{"c", "ci"}, []string{"TEXT", "TEXT"}, [][]any{{"(1, 2)", "MiXeD"}}))
				nominal := fixture.Select("SELECT t.typtype,a.typelem='public.citext'::regtype::oid AS element_identity,a.oid=e.typarray AS reciprocal FROM pg_type t JOIN pg_type a ON a.oid=t.typbasetype JOIN pg_type e ON e.oid=a.typelem WHERE t.oid='public.tl_cube_array'::regtype::oid", []string{"typtype", "element_identity", "reciprocal"}, []string{"CHAR", "BOOL", "BOOL"}, [][]any{{"d", true, true}})
				fixture.RunStep(t, p.Ctx, c, r, nominal)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(nominal))
			}
			check()
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			var second bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), second.Bytes()) {
				t.Fatal("nominal domain/canonical-cycle replay changed dump recipes")
			}
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='public'::regnamespace::oid AND typname IN ('parked','_parked')", 0)
		})
	})
	for _, kind := range []string{"missing_owner", "recreated_owner", "reader_cancellation", "writer_failure"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"tl_restore_recipient"})
			typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				domainIdentityDDL(t, p, c, r, "CREATE ROLE tl_restore_recipient", "CREATE TYPE public.tl_saved AS ENUM ('z')", "ALTER TYPE public.tl_saved RENAME TO tl_current", "CREATE TABLE public.tl_sentinel(v integer,m public.tl_current)")
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.tl_sentinel VALUES(42,'z')", 1))
				check := func() {
					fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT v,m::text FROM public.tl_sentinel", []string{"v", "m"}, []string{"INT4", "TEXT"}, [][]any{{int64(42), "z"}}))
				}
				check()
				if b.PG {
					return
				}
				var before bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
				stream := "SET SESSION AUTHORIZATION 'tl_restore_recipient'; CREATE TYPE public.tl_incoming AS ENUM ('a'); ALTER TYPE public.tl_incoming RENAME TO tl_final;"
				ctx := p.Ctx
				reader := &typeLifecycleBeforeReader{reader: strings.NewReader(stream)}
				switch kind {
				case "missing_owner":
					reader.reader = strings.NewReader(strings.ReplaceAll(stream, "tl_restore_recipient", "tl_absent_owner"))
				case "recreated_owner":
					reader.before = func() {
						domainIdentityDDL(t, p, c, r, "DROP ROLE tl_restore_recipient", "CREATE ROLE tl_restore_recipient")
					}
				case "reader_cancellation":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(p.Ctx)
					defer cancel()
					reader.before = cancel
				case "writer_failure":
					want := errors.New("type lifecycle writer failure")
					if err := stuntgres.Dump(p.Ctx, p.Native, typeLifecycleErrorWriter{want}); !errors.Is(err, want) {
						t.Fatalf("writer failure=%v", err)
					}
					check()
					return
				}
				err := stuntgres.Restore(ctx, p.Native, reader)
				if err == nil {
					t.Fatal("invalid replacement succeeded", kind)
				}
				if kind == "missing_owner" {
					want := fixture.ControlsReferenceDiagnostic("22023", `role "tl_absent_owner" does not exist`)
					fixture.AssertDiagnostic(t, err, &want)
				}
				if kind == "reader_cancellation" && !errors.Is(err, context.Canceled) {
					t.Fatalf("reader cancellation=%v", err)
				}
				check()
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if before.String() != after.String() {
					t.Fatal("failed replacement changed owned type graph")
				}
			})
		})
	}
}

func typeLifecycleReplayRows() fixture.Step {
	return fixture.Select("SELECT id,m::text,d,p::text,a::text,c::text,q::text,ci::text,e::text FROM public.tl_replay ORDER BY id", []string{"id", "m", "d", "p", "a", "c", "q", "ci", "e"}, []string{"INT4", "TEXT", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "z", int64(7), "(7,x)", "[0:1]={7,NULL}", "(1, 2)", "1", "MiXeD", "(6378168, 0, 0)"}, {int64(2), nil, nil, nil, nil, nil, nil, nil, nil}})
}

type typeLifecycleErrorReader struct{ err error }

func (r typeLifecycleErrorReader) Read([]byte) (int, error) { return 0, r.err }

type typeLifecycleBeforeReader struct {
	reader io.Reader
	before func()
}

func (r *typeLifecycleBeforeReader) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.reader.Read(p)
}

type typeLifecycleErrorWriter struct{ err error }

func (w typeLifecycleErrorWriter) Write([]byte) (int, error) { return 0, w.err }
