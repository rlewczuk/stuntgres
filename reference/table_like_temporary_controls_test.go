package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceTableLikeTemporaryIdentity(t *testing.T) { tableLikeTemporaryIdentity(t) }
func TestReferenceTableLikeTemporaryControls(t *testing.T) { tableLikeTemporaryDefaultsLifecycle(t) }

func tableLikeTemporaryDefaultsLifecycle(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TABLE public.carrier(v INT)", nil)
		run(referenceExec("INSERT INTO public.carrier VALUES(7)", 1))
		s.utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		run(referenceExec("INSERT INTO temp_source DEFAULT VALUES", 1))
		s.utility("CREATE TEMP TABLE temp_copy(lead TEXT,LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		run(referenceExec("INSERT INTO temp_copy(lead) VALUES('x')", 1))
		run(referenceExec("INSERT INTO temp_source DEFAULT VALUES", 1))
		s.exec(s.peer, "CREATE TEMP SEQUENCE default_seed START 901 CACHE 1", 0)
		s.value(s.peer, "SELECT nextval('pg_temp.default_seed')", 901)
		s.state(s.c, "pg_temp.default_seed", 33, true)
		var dump bytes.Buffer
		var held *sql.Rows
		if !s.postgres {
			var err error
			held, err = s.c.QueryContext(context.WithValue(s.p.ctx, referenceRecorderKey{}, s.record), "SELECT * FROM temp_copy")
			if err != nil {
				s.t.Fatal(err)
			}
			defer held.Close()
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			s.state(s.c, "pg_temp.default_seed", 33, true)
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
			tableLikeAbsent(run, "public.carrier", s.prepared)
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			frozen := sequenceFrozenRows{referenceExecutor: s.c, rows: held, query: "SELECT * FROM temp_copy"}
			referenceRunStep(s.t, s.p.ctx, frozen, s.record, referenceSelect("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(32)}}))
		}
		run(referenceSelect("SELECT v FROM temp_source ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {int64(33)}}))
		run(referenceSelect("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(32)}}))
		run(referenceExec("INSERT INTO temp_copy(lead) VALUES('after')", 1))
		s.utility("CREATE TEMP TABLE retained_copy(LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		s.state(s.c, "pg_temp.default_seed", 34, true)
		run(referenceExec("INSERT INTO retained_copy DEFAULT VALUES", 1))
		run(referenceSelect("SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, [][]any{{int64(35)}}))
		referenceTableDDLMetadata(s.t, s.p.ctx, s.c, s.postgres, s.prepared, "SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, []bool{true}, 1)
		s.state(s.c, "pg_temp.default_seed", 35, true)
		s.value(s.peer, "SELECT nextval('pg_temp.default_seed')", 902)
		temporaryTableRows(s, s.c, "public.carrier", 7)
		if !s.postgres {
			for _, script := range []string{"CREATE TABLE replacement(LIKE missing)", "CREATE TABLE replacement(v no_such_type)"} {
				if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewBufferString(script)); err == nil {
					s.t.Fatal("invalid Restore accepted", script)
				}
				s.state(s.c, "pg_temp.default_seed", 35, true)
				run(referenceSelect("SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, [][]any{{int64(35)}}))
				temporaryTableRows(s, s.c, "public.carrier", 7)
			}
			canceled, cancel := context.WithCancel(s.p.ctx)
			cancel()
			if err := stuntgres.Restore(canceled, s.p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				s.t.Fatal("canceled Restore error", err)
			}
			s.state(s.c, "pg_temp.default_seed", 35, true)
			run(referenceSelect("SELECT v FROM retained_copy", []string{"v"}, []string{"INT8"}, [][]any{{int64(35)}}))
			temporaryTableRows(s, s.c, "public.carrier", 7)
			if s.p.postgres != nil {
				if _, err := s.p.postgres.ExecContext(s.p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.t.Fatal(err)
				}
				if _, err := s.p.postgres.ExecContext(s.p.ctx, dump.String()); err != nil {
					s.t.Fatal("actual temporary-copy permanent stream replay", err)
				}
				referenceRunStep(s.t, s.p.ctx, s.p.postgres, s.p.postgresRecord, referenceSelect("SELECT v FROM public.carrier", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				if _, err := s.p.postgres.ExecContext(s.p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.t.Fatal(err)
				}
			}
		}
	})
}

func tableLikeTemporaryIdentity(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TEMP TABLE identity_source(id BIGINT GENERATED BY DEFAULT AS IDENTITY(START 7 INCREMENT 3 MINVALUE 1 MAXVALUE 100 CACHE 1 NO CYCLE))", nil)
		run(referenceExec("INSERT INTO identity_source DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO identity_source DEFAULT VALUES", 1))
		s.utility("CREATE TABLE public.permanent_identity(LIKE pg_temp.identity_source INCLUDING IDENTITY)", nil)
		s.utility("CREATE TEMP TABLE temporary_identity(LIKE pg_temp.identity_source INCLUDING IDENTITY)", nil)
		s.utility("CREATE TEMP TABLE from_permanent(LIKE public.permanent_identity INCLUDING ALL)", nil)
		run(referenceSelect("SELECT pg_get_serial_sequence('pg_temp.identity_source','id')<>pg_get_serial_sequence('public.permanent_identity','id') AND pg_get_serial_sequence('pg_temp.identity_source','id')<>pg_get_serial_sequence('pg_temp.temporary_identity','id') AND pg_get_serial_sequence('public.permanent_identity','id')<>pg_get_serial_sequence('pg_temp.temporary_identity','id') AS fresh", []string{"fresh"}, []string{"BOOL"}, [][]any{{true}}))
		for _, name := range []string{"public.permanent_identity", "pg_temp.temporary_identity", "pg_temp.from_permanent"} {
			run(referenceSelect("SELECT id FROM "+name, []string{"id"}, []string{"INT8"}, nil))
			referenceTableDDLMetadata(s.t, s.p.ctx, s.c, s.postgres, s.prepared, "SELECT id FROM "+name, []string{"id"}, []string{"INT8"}, []bool{false}, 0)
			seq := name + "_id_seq"
			run(referenceSelect("SELECT last_value,is_called FROM "+seq, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), false}}))
			run(referenceSelect("SELECT seqstart,seqincrement,seqmin,seqmax,seqcache,seqcycle FROM pg_sequence WHERE seqrelid='"+seq+"'::regclass::oid", []string{"seqstart", "seqincrement", "seqmin", "seqmax", "seqcache", "seqcycle"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{int64(7), int64(3), int64(1), int64(100), int64(1), false}}))
			run(referenceSelect("SELECT refobjid='"+name+"'::regclass::oid AS owned,refobjsubid FROM pg_depend WHERE objid='"+seq+"'::regclass::oid AND deptype='i'", []string{"owned", "refobjsubid"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}))
			if s.postgres {
				persistence := "t"
				if name == "public.permanent_identity" {
					persistence = "p"
				}
				run(referenceSelect("SELECT relpersistence::text AS persistence FROM pg_class WHERE oid='"+seq+"'::regclass", []string{"persistence"}, []string{"TEXT"}, [][]any{{persistence}}))
			}
		}
		s.state(s.c, "pg_temp.identity_source_id_seq", 10, true)
		run(referenceExec("INSERT INTO identity_source DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO public.permanent_identity DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO temporary_identity DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO from_permanent DEFAULT VALUES", 1))
		s.utility("CREATE TEMP TABLE defaults_only(LIKE pg_temp.identity_source INCLUDING DEFAULTS)", nil)
		run(referenceSelect("SELECT pg_get_serial_sequence('pg_temp.defaults_only','id') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{nil}}))
		s.exec(s.c, "SET search_path TO pg_temp,public", 0)
		namespace := tempReferenceNamespace(s.t, s, false)
		diagnostic := tempReferenceDiagnostic("23502", `null value in column "id" of relation "defaults_only" violates not-null constraint`, "Failing row contains (null).", "")
		diagnostic.Schema, diagnostic.Table, diagnostic.Column = namespace, "defaults_only", "id"
		step := referenceExec("INSERT INTO defaults_only DEFAULT VALUES", 0)
		step.diagnostic = diagnostic
		step.state = diagnostic.Code
		step.errorStage = "execute"
		run(step)
		run(referenceExec("INSERT INTO defaults_only VALUES(99)", 1))
		var dump bytes.Buffer
		if !s.postgres {
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			s.state(s.c, "pg_temp.identity_source_id_seq", 13, true)
			s.state(s.c, "pg_temp.temporary_identity_id_seq", 7, true)
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
		}
		run(referenceExec("INSERT INTO identity_source DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO temporary_identity DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO public.permanent_identity DEFAULT VALUES", 1))
		s.utility("CREATE TEMP TABLE retained_identity(LIKE pg_temp.identity_source INCLUDING IDENTITY)", nil)
		run(referenceSelect("SELECT last_value,is_called FROM retained_identity_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), false}}))
		run(referenceExec("INSERT INTO retained_identity DEFAULT VALUES", 1))
		run(referenceSelect("SELECT id FROM identity_source ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}, {int64(16)}}))
		run(referenceSelect("SELECT id FROM temporary_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}}))
		run(referenceSelect("SELECT id FROM retained_identity", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}}))
		s.utility("DROP TABLE identity_source RESTRICT", nil)
		run(referenceExec("INSERT INTO temporary_identity DEFAULT VALUES", 1))
		run(referenceSelect("SELECT id FROM temporary_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}}))
		if !s.postgres {
			dump.Reset()
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if s.p.postgres != nil {
				if _, err := s.p.postgres.ExecContext(s.p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.t.Fatal(err)
				}
				if _, err := s.p.postgres.ExecContext(s.p.ctx, dump.String()); err != nil {
					s.t.Fatal("actual temporary identity permanent stream replay", err)
				}
				referenceRunStep(s.t, s.p.ctx, s.p.postgres, s.p.postgresRecord, referenceSelect("SELECT id FROM public.permanent_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}}))
				referenceRunStep(s.t, s.p.ctx, s.p.postgres, s.p.postgresRecord, referenceExec("INSERT INTO public.permanent_identity DEFAULT VALUES", 1))
				referenceRunStep(s.t, s.p.ctx, s.p.postgres, s.p.postgresRecord, referenceSelect("SELECT id FROM public.permanent_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}}))
				if _, err := s.p.postgres.ExecContext(s.p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.t.Fatal(err)
				}
			}
		}
		s.retire()
		s.exec(s.peer, "INSERT INTO public.permanent_identity DEFAULT VALUES", 1)
		referenceRunStep(s.t, s.p.ctx, s.peer, s.record, referenceSelect("SELECT id FROM public.permanent_identity ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(10)}, {int64(13)}}))
		s.missing(s.peer, namespace+".temporary_identity", "id", "16")
	})
}
