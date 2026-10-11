package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func extensionTempExitBarrier(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, key int64) {
	t.Helper()
	// Server exit completes TEMP cleanup before releasing session advisory locks.
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	fixture.RunStep(t, ctx, peer, r, fixture.Select("SELECT pg_advisory_lock($1::bigint)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}, key))
	fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT pg_advisory_unlock($1::bigint)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}, key))
}
func TestReferenceExtensionTemporaryClose(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE TEMP TABLE ext_peer_seed(v INT)", 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("INSERT INTO ext_peer_seed VALUES(73)", 1))
		for _, phase := range []string{"committed", "private_discard", "private_install"} {
			t.Run(phase, func(t *testing.T) {
				o := extensionTemporaryOwner(t, p, pg)
				name := extensionTempSeed(t, p, o)
				ownerRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, o.conn, o.recorder, s) }
				ownerRun(fixture.Select("SELECT pg_advisory_lock(30790001)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
				fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT pg_try_advisory_lock(30790001)", []string{"pg_try_advisory_lock"}, []string{"BOOL"}, [][]any{{false}}))
				var id extensionTempIdentity
				if phase == "private_install" {
					installer := extensionTemporaryOwner(t, p, pg)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, installer.conn, installer.recorder, s) }
					run(fixture.Select("SELECT pg_advisory_lock(30790002)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
					id = extensionTempCapture(t, p, installer.conn)
					extensionCount(t, p, peer, r, 0)
					if !installer.close(t) {
						t.FailNow()
					}
					extensionTempExitBarrier(t, p, peer, r, 30790002)
					extensionTempGone(t, p, peer, r, id)
				} else {
					fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
					id = extensionTempCapture(t, p, o.conn)
					extensionTempConsumers(t, p, o.conn, o.recorder, name)
					fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE VIEW public.ext_peer_late AS SELECT v FROM public.ext_temp_chain", 0))
					if phase == "private_discard" {
						ownerRun(fixture.Exec("BEGIN", 0))
						ownerRun(fixture.Exec("DISCARD TEMP", 0))
						extensionTempPresent(t, p, peer, r, id)
					}
				}
				if !o.close(t) {
					t.FailNow()
				}
				extensionTempExitBarrier(t, p, peer, r, 30790001)
				extensionTempGone(t, p, peer, r, id)
				fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT v FROM ext_peer_seed", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
				fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT count(*) FROM pg_views WHERE schemaname='public' AND viewname='ext_peer_late'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT count(*) FROM pg_tables WHERE tablename='ext_temp_seed'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if phase != "private_install" {
					extensionTempConsumerState(t, p, peer, r, false)
					extensionTempSurvivingWrites(t, p, peer, r)
					extensionTempMissingViews(t, p, peer, r)
					fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("DROP TABLE public.ext_temp_consumer", 0))
				}
				// PG retains an empty namespace shell; native retires its physical namespace.
				if !pg {
					fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid=2615 AND refobjid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, id.namespace))
				}
			})
		}
	})
}
func TestReferenceExtensionTemporaryControls(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		o := extensionTemporaryOwner(t, p, pg)
		defer o.close(t)
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE TABLE public.ext_temp_baseline(v INT)", 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("INSERT INTO public.ext_temp_baseline VALUES(7)", 1))
		var baseline bytes.Buffer
		if !pg {
			if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
				t.Fatal(err)
			}
		}
		name := extensionTempSeed(t, p, o)
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
		private := extensionTempCapture(t, p, peer)
		extensionCount(t, p, o.conn, o.recorder, 0)
		if !pg {
			var portable bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &portable); err != nil {
				t.Fatal("private TEMP install escaped committed dump", err)
			}
			if portable.String() != baseline.String() {
				t.Fatal("uncommitted TEMP inventory altered deterministic persistent dump")
			}
		}
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("ROLLBACK", 0))
		extensionTempGone(t, p, peer, r, private)
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
		id := extensionTempCapture(t, p, o.conn)
		extensionTempConsumers(t, p, o.conn, o.recorder, name)
		function := extensionTempIdentifier(name) + ".unaccent"
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("CREATE TEMP TABLE ext_temp_local(v TEXT DEFAULT "+function+"('Hôtel'::text),CHECK("+function+"(v)=v))", 0))
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("CREATE TEMP VIEW ext_temp_local_view AS SELECT "+function+"('Hôtel'::text) AS v", 0))
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("INSERT INTO ext_temp_local DEFAULT VALUES", 1))
		if !pg {
			var attempted bytes.Buffer
			err := stuntgres.Dump(p.Ctx, p.Native, &attempted)
			if err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" || attempted.Len() != 0 {
				t.Fatal("TEMP dump must reject atomically before output", attempted.String(), err)
			}
			extensionTempPresent(t, p, o.conn, o.recorder, id)
			extensionTempCall(t, p, o.conn, o.recorder, name)
			extensionTempConsumerState(t, p, o.conn, o.recorder, true)
			held, err := o.conn.QueryContext(p.Ctx, "SELECT v FROM ext_temp_local_view")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			metadata, err := held.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal("TEMP retained metadata", metadata, err)
			}
			for i, script := range []string{baseline.String() + "\nCREATE EXTENSION unaccent;", "CREATE EXTENSION unaccent SCHEMA " + extensionTempIdentifier(name) + ";"} {
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
				if err == nil {
					t.Fatal("replacement borrowed target TEMP authority or won its global name", script)
				}
				if i == 1 {
					fixture.AssertControlDiagnostic(t, err, fixture.ControlsReferenceDiagnostic("3F000", `schema "`+name+`" does not exist`))
				} else if err.Error() != `stuntgres: catalog replacement conflicts with live temporary extension "unaccent"` {
					t.Fatal("global retained name conflict", err)
				}
				extensionTempPresent(t, p, o.conn, o.recorder, id)
				extensionTempCall(t, p, o.conn, o.recorder, name)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled TEMP replacement", err)
			}
			extensionTempPresent(t, p, o.conn, o.recorder, id)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			extensionTempPresent(t, p, o.conn, o.recorder, id)
			extensionTempCall(t, p, o.conn, o.recorder, name)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline.String())); err != nil {
				t.Fatal(err)
			}
			extensionTempPresent(t, p, o.conn, o.recorder, id)
			extensionTempCall(t, p, o.conn, o.recorder, name)
			current, err := held.ColumnTypes()
			if err != nil || len(current) != 1 || current[0].DatabaseTypeName() != metadata[0].DatabaseTypeName() {
				t.Fatal("replacement altered TEMP retained metadata", current, err)
			}
			if !held.Next() {
				t.Fatal("replacement lost held TEMP row", held.Err())
			}
			var old string
			if err := held.Scan(&old); err != nil || old != "Hotel" {
				t.Fatal("replacement changed held TEMP row", old, err)
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Select("SELECT v FROM ext_temp_local", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Select("SELECT v FROM ext_temp_local_view", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("INSERT INTO ext_temp_local DEFAULT VALUES", 1))
		}
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("DISCARD TEMP", 0))
		extensionTempGone(t, p, peer, r, id)
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT v FROM public.ext_temp_baseline", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if !pg {
			var retired bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &retired); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(retired.String(), "unaccent") || strings.Contains(retired.String(), name) {
				t.Fatal("retired TEMP installation escaped dump", retired.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(retired.String())); err != nil {
				t.Fatal(err)
			}
			extensionCount(t, p, peer, r, 0)
			fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT v FROM public.ext_temp_baseline", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	})
}

func TestReferenceExtensionTemporaryOwnInstall(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		o := extensionTemporaryOwner(t, p, pg)
		name := extensionTempSeed(t, p, o)
		defer o.close(t)
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("INSERT INTO ext_temp_seed VALUES(9)", 1))
		query := "CREATE EXTENSION unaccent SCHEMA " + extensionTempIdentifier(name)
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42883", Message: "function unaccent_init(internal) does not exist", Context: "SQL statement \"CREATE TEXT SEARCH TEMPLATE unaccent (\n\tINIT = unaccent_init,\n\tLEXIZE = unaccent_lexize\n)\"\nextension script file \"unaccent--1.1.sql\", near line 26"}))
		extensionCount(t, p, o.conn, o.recorder, 0)
		extensionAbsentMembers(t, p, o.conn, o.recorder)
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Select("SELECT id FROM ext_temp_seed", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, o.conn, o.recorder, s) }
		run(fixture.ImplicitError("CREATE EXTENSION unaccent SCHEMA pg_temp", fixture.Diagnostic{Code: "3F000", Message: `schema "pg_temp" does not exist`}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT own_helper", 0))
		run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "42883", Message: "function unaccent_init(internal) does not exist", Context: "SQL statement \"CREATE TEXT SEARCH TEMPLATE unaccent (\n\tINIT = unaccent_init,\n\tLEXIZE = unaccent_lexize\n)\"\nextension script file \"unaccent--1.1.sql\", near line 26"}))
		run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK TO own_helper", 0))
		extensionCount(t, p, o.conn, o.recorder, 0)
		run(fixture.Select("SELECT id FROM ext_temp_seed", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		run(fixture.Exec("ROLLBACK", 0))
	})
}
