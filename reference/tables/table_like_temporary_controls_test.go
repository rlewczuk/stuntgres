package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeTemporaryIdentity(t *testing.T) { tableLikeTemporaryIdentity(t) }
func TestReferenceTableLikeTemporaryControls(t *testing.T) { tableLikeTemporaryDefaultsLifecycle(t) }

func tableLikeTemporaryDefaultsLifecycle(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TABLE public.carrier(v INT)", nil)
		run(fixture.Exec("INSERT INTO public.carrier VALUES(7)", 1))
		s.Utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.Utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		run(fixture.Exec("INSERT INTO temp_source DEFAULT VALUES", 1))
		s.Utility("CREATE TEMP TABLE temp_copy(lead TEXT,LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		run(fixture.Exec("INSERT INTO temp_copy(lead) VALUES('x')", 1))
		run(fixture.Exec("INSERT INTO temp_source DEFAULT VALUES", 1))
		s.Exec(s.Peer, "CREATE TEMP SEQUENCE default_seed START 901 CACHE 1", 0)
		s.Value(s.Peer, "SELECT nextval('pg_temp.default_seed')", 901)
		s.State(s.C, "pg_temp.default_seed", 33, true)
		var dump bytes.Buffer
		var held *sql.Rows
		if !s.Postgres {
			var err error
			held, err = s.C.QueryContext(context.WithValue(s.P.Ctx, fixture.RecorderKey{}, s.Record), "SELECT * FROM temp_copy")
			if err != nil {
				s.T.Fatal(err)
			}
			defer held.Close()
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			s.State(s.C, "pg_temp.default_seed", 33, true)
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			fixture.TableLikeAbsent(run, "public.carrier", s.Prepared)
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			frozen := fixture.SequenceFrozenRows{Executor: s.C, Rows: held, Query: "SELECT * FROM temp_copy"}
			fixture.RunStep(s.T, s.P.Ctx, frozen, s.Record, fixture.Select("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(32)}}))
		}
		run(fixture.Select("SELECT v FROM temp_source ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {int64(33)}}))
		run(fixture.Select("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(32)}}))
		run(fixture.Exec("INSERT INTO temp_copy(lead) VALUES('after')", 1))
		s.Utility("CREATE TEMP TABLE retained_copy(LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		s.State(s.C, "pg_temp.default_seed", 34, true)
		run(fixture.Exec("INSERT INTO retained_copy DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, [][]any{{int64(35)}}))
		fixture.TableDDLMetadata(s.T, s.P.Ctx, s.C, s.Postgres, s.Prepared, "SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, []bool{true}, 1)
		s.State(s.C, "pg_temp.default_seed", 35, true)
		s.Value(s.Peer, "SELECT nextval('pg_temp.default_seed')", 902)
		fixture.TemporaryTableRows(s, s.C, "public.carrier", 7)
		if !s.Postgres {
			for _, script := range []string{"CREATE TABLE replacement(LIKE missing)", "CREATE TABLE replacement(v no_such_type)"} {
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewBufferString(script)); err == nil {
					s.T.Fatal("invalid Restore accepted", script)
				}
				s.State(s.C, "pg_temp.default_seed", 35, true)
				run(fixture.Select("SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, [][]any{{int64(35)}}))
				fixture.TemporaryTableRows(s, s.C, "public.carrier", 7)
			}
			canceled, cancel := context.WithCancel(s.P.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, s.P.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				s.T.Fatal("canceled Restore error", err)
			}
			s.State(s.C, "pg_temp.default_seed", 35, true)
			run(fixture.Select("SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, [][]any{{int64(35)}}))
			fixture.TemporaryTableRows(s, s.C, "public.carrier", 7)
			if s.P.Postgres != nil {
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.T.Fatal(err)
				}
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, dump.String()); err != nil {
					s.T.Fatal("actual temporary-copy permanent stream replay", err)
				}
				fixture.RunStep(s.T, s.P.Ctx, s.P.Postgres, s.P.PostgresRecord, fixture.Select("SELECT v FROM public.carrier", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.T.Fatal(err)
				}
			}
		}
	})
}

func tableLikeTemporaryIdentity(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TEMP TABLE identity_source(id BIGINT GENERATED BY DEFAULT AS IDENTITY(START 7 INCREMENT 3 MINVALUE 1 MAXVALUE 100 CACHE 1 NO CYCLE))", nil)
		run(fixture.Exec("INSERT INTO identity_source DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO identity_source DEFAULT VALUES", 1))
		s.Utility("CREATE TABLE public.permanent_identity(LIKE pg_temp.identity_source INCLUDING IDENTITY)", nil)
		s.Utility("CREATE TEMP TABLE temporary_identity(LIKE pg_temp.identity_source INCLUDING IDENTITY)", nil)
		s.Utility("CREATE TEMP TABLE from_permanent(LIKE public.permanent_identity INCLUDING ALL)", nil)
		run(fixture.Select("SELECT pg_get_serial_sequence('pg_temp.identity_source','id')<>pg_get_serial_sequence('public.permanent_identity','id') AND pg_get_serial_sequence('pg_temp.identity_source','id')<>pg_get_serial_sequence('pg_temp.temporary_identity','id') AND pg_get_serial_sequence('public.permanent_identity','id')<>pg_get_serial_sequence('pg_temp.temporary_identity','id') AS fresh", []string{"fresh"}, []string{"BOOL"}, [][]any{{true}}))
		for _, name := range []string{"public.permanent_identity", "pg_temp.temporary_identity", "pg_temp.from_permanent"} {
			run(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT8"}, nil))
			fixture.TableDDLMetadata(s.T, s.P.Ctx, s.C, s.Postgres, s.Prepared, "SELECT id FROM "+name, []string{"id"}, []string{"INT8"}, []bool{false}, 0)
			seq := name + "_id_seq"
			run(fixture.Select("SELECT last_value,is_called FROM "+seq, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), false}}))
			run(fixture.Select("SELECT seqstart,seqincrement,seqmin,seqmax,seqcache,seqcycle FROM pg_sequence WHERE seqrelid='"+seq+"'::regclass::oid", []string{"seqstart", "seqincrement", "seqmin", "seqmax", "seqcache", "seqcycle"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{int64(7), int64(3), int64(1), int64(100), int64(1), false}}))
			run(fixture.Select("SELECT refobjid='"+name+"'::regclass::oid AS owned,refobjsubid FROM pg_depend WHERE objid='"+seq+"'::regclass::oid AND deptype='i'", []string{"owned", "refobjsubid"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}))
			if s.Postgres {
				persistence := "t"
				if name == "public.permanent_identity" {
					persistence = "p"
				}
				run(fixture.Select("SELECT relpersistence::text AS persistence FROM pg_class WHERE oid='"+seq+"'::regclass", []string{"persistence"}, []string{"TEXT"}, [][]any{{persistence}}))
			}
		}
		s.State(s.C, "pg_temp.identity_source_id_seq", 10, true)
		run(fixture.Exec("INSERT INTO identity_source DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO public.permanent_identity DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO temporary_identity DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO from_permanent DEFAULT VALUES", 1))
		s.Utility("CREATE TEMP TABLE defaults_only(LIKE pg_temp.identity_source INCLUDING DEFAULTS)", nil)
		run(fixture.Select("SELECT pg_get_serial_sequence('pg_temp.defaults_only','id') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{nil}}))
		s.Exec(s.C, "SET search_path TO pg_temp,public", 0)
		namespace := fixture.TempReferenceNamespace(s.T, s, false)
		diagnostic := fixture.TempReferenceDiagnostic("23502", `null value in column "id" of relation "defaults_only" violates not-null constraint`, "Failing row contains (null).", "")
		diagnostic.Schema, diagnostic.Table, diagnostic.Column = namespace, "defaults_only", "id"
		step := fixture.Exec("INSERT INTO defaults_only DEFAULT VALUES", 0)
		step.Diagnostic = diagnostic
		step.State = diagnostic.Code
		step.ErrorStage = "execute"
		run(step)
		run(fixture.Exec("INSERT INTO defaults_only VALUES(99)", 1))
		var dump bytes.Buffer
		if !s.Postgres {
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			s.State(s.C, "pg_temp.identity_source_id_seq", 13, true)
			s.State(s.C, "pg_temp.temporary_identity_id_seq", 7, true)
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
		}
		run(fixture.Exec("INSERT INTO identity_source DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO temporary_identity DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO public.permanent_identity DEFAULT VALUES", 1))
		s.Utility("CREATE TEMP TABLE retained_identity(LIKE pg_temp.identity_source INCLUDING IDENTITY)", nil)
		run(fixture.Select("SELECT last_value,is_called FROM retained_identity_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), false}}))
		run(fixture.Exec("INSERT INTO retained_identity DEFAULT VALUES", 1))
		run(fixture.Select("SELECT id FROM identity_source ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}, {int64(16)}}))
		run(fixture.Select("SELECT id FROM temporary_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}}))
		run(fixture.Select("SELECT id FROM retained_identity", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}}))
		s.Utility("DROP TABLE identity_source RESTRICT", nil)
		run(fixture.Exec("INSERT INTO temporary_identity DEFAULT VALUES", 1))
		run(fixture.Select("SELECT id FROM temporary_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}}))
		if !s.Postgres {
			dump.Reset()
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if s.P.Postgres != nil {
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.T.Fatal(err)
				}
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, dump.String()); err != nil {
					s.T.Fatal("actual temporary identity permanent stream replay", err)
				}
				fixture.RunStep(s.T, s.P.Ctx, s.P.Postgres, s.P.PostgresRecord, fixture.Select("SELECT id FROM public.permanent_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}}))
				fixture.RunStep(s.T, s.P.Ctx, s.P.Postgres, s.P.PostgresRecord, fixture.Exec("INSERT INTO public.permanent_identity DEFAULT VALUES", 1))
				fixture.RunStep(s.T, s.P.Ctx, s.P.Postgres, s.P.PostgresRecord, fixture.Select("SELECT id FROM public.permanent_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}}))
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.T.Fatal(err)
				}
			}
		}
		s.Retire()
		s.Exec(s.Peer, "INSERT INTO public.permanent_identity DEFAULT VALUES", 1)
		fixture.RunStep(s.T, s.P.Ctx, s.Peer, s.Record, fixture.Select("SELECT id FROM public.permanent_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}}))
		s.Missing(s.Peer, namespace+".temporary_identity", "id", "16")
	})
}
