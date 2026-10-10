package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceTableLikeTemporaryDefaults(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		run(referenceExec("INSERT INTO temp_source DEFAULT VALUES", 1))
		s.state(s.c, "pg_temp.default_seed", 31, true)
		s.utility("CREATE TEMP TABLE temp_copy(lead TEXT,LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		run(referenceSelect("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, nil))
		referenceTableDDLMetadata(s.t, s.p.ctx, s.c, s.postgres, s.prepared, "SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, []bool{true, true}, 0)
		run(referenceSelect("SELECT pg_get_serial_sequence('pg_temp.temp_copy','v') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{nil}}))
		run(referenceSelect("SELECT a.oid<>b.oid AS fresh FROM pg_attrdef a,pg_attrdef b WHERE a.adrelid='pg_temp.temp_source'::regclass::oid AND b.adrelid='pg_temp.temp_copy'::regclass::oid", []string{"fresh"}, []string{"BOOL"}, [][]any{{true}}))
		run(referenceSelect("SELECT count(*) AS dependencies FROM pg_depend d,pg_attrdef a WHERE a.adrelid='pg_temp.temp_copy'::regclass::oid AND d.objid=a.oid AND d.refobjid='pg_temp.default_seed'::regclass::oid AND d.deptype='n'", []string{"dependencies"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.state(s.c, "pg_temp.default_seed", 31, true)
		run(referenceExec("INSERT INTO temp_copy(lead) VALUES('x')", 1))
		run(referenceExec("INSERT INTO temp_source DEFAULT VALUES", 1))
		run(referenceSelect("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(32)}}))
		run(referenceSelect("SELECT v FROM temp_source ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {int64(33)}}))
		next := int64(34)
		for i, options := range []string{"", " EXCLUDING DEFAULTS", " INCLUDING DEFAULTS EXCLUDING DEFAULTS", " INCLUDING ALL EXCLUDING DEFAULTS", " EXCLUDING DEFAULTS INCLUDING DEFAULTS", " INCLUDING ALL"} {
			name := "mask_" + strconv.Itoa(i)
			s.utility("CREATE TEMP TABLE "+name+"(LIKE pg_temp.temp_source"+options+")", nil)
			run(referenceExec("INSERT INTO "+name+" DEFAULT VALUES", 1))
			var value any
			if i >= 4 {
				value = next
				next++
			}
			run(referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{value}}))
		}
		s.utility("DROP TABLE pg_temp.temp_source RESTRICT", nil)
		run(referenceExec("INSERT INTO temp_copy(lead) VALUES('after')", 1))
		run(referenceSelect("SELECT v FROM temp_copy ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(32)}, {next}}))
	})
}

func TestReferenceTableLikeTemporaryProviderAuthority(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		s.utility("CREATE TEMP TABLE temp_copy(LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		run(referenceExec("INSERT INTO temp_copy DEFAULT VALUES", 1))
		s.utility("DROP TABLE temp_source RESTRICT", nil)
		dependency := tempReferenceDiagnostic("2BP01", "cannot drop sequence default_seed because other objects depend on it", "default value for column v of table temp_copy depends on sequence default_seed", "")
		dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		s.utility("DROP SEQUENCE default_seed RESTRICT", dependency)
		s.utility("DROP SEQUENCE default_seed CASCADE", nil)
		run(referenceExec("INSERT INTO temp_copy DEFAULT VALUES", 1))
		run(referenceSelect("SELECT v FROM temp_copy ORDER BY v NULLS LAST", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {nil}}))
		run(referenceSelect("SELECT count(*) AS defaults FROM pg_attrdef WHERE adrelid='pg_temp.temp_copy'::regclass::oid", []string{"defaults"}, []string{"INT8"}, [][]any{{int64(0)}}))
		s.utility("CREATE TEMP TABLE temp_serial(v SERIAL)", nil)
		s.utility("CREATE TEMP TABLE serial_copy(LIKE pg_temp.temp_serial INCLUDING DEFAULTS)", nil)
		run(referenceExec("INSERT INTO temp_serial DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO serial_copy DEFAULT VALUES", 1))
		temporaryTableRows(s, s.c, "temp_serial", 1)
		temporaryTableRows(s, s.c, "serial_copy", 2)
		run(referenceSelect("SELECT pg_get_serial_sequence('pg_temp.serial_copy','v') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{nil}}))
		dependency = tempReferenceDiagnostic("2BP01", "cannot drop table temp_serial because other objects depend on it", "default value for column v of table serial_copy depends on sequence temp_serial_v_seq", "")
		dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		s.utility("DROP TABLE temp_serial RESTRICT", dependency)
		s.utility("DROP TABLE serial_copy,temp_serial", nil)
	})
}

func TestReferenceTableLikeTemporaryPermanentDefaultBoundary(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		s.utility("CREATE TEMP TABLE combined(id BIGINT GENERATED BY DEFAULT AS IDENTITY(START 7 INCREMENT 3),v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		for _, tc := range []struct{ name, source, options string }{{"permanent_copy", "temp_source", " INCLUDING DEFAULTS"}, {"permanent_all", "combined", " INCLUDING ALL"}, {"permanent_last", "temp_source", " EXCLUDING DEFAULTS INCLUDING DEFAULTS"}} {
			q := "CREATE TABLE public." + tc.name + "(LIKE pg_temp." + tc.source + tc.options + ")"
			var diagnostic *referenceDiagnostic
			if !s.postgres {
				diagnostic = tempReferenceDiagnostic("0A000", "temporary sequence DEFAULT is unsupported", "", "")
			}
			s.utility(q, diagnostic)
			if s.postgres {
				run(referenceExec("INSERT INTO public."+tc.name+" DEFAULT VALUES", 1))
			} else {
				tableLikeAbsent(run, "public."+tc.name, s.prepared)
				if tc.source == "combined" {
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='permanent_all_id_seq'", []string{"relname"}, []string{"NAME"}, nil))
				}
				s.utility("CREATE TABLE public."+tc.name+"(v BIGINT DEFAULT 11)", nil)
				run(referenceExec("INSERT INTO public."+tc.name+" DEFAULT VALUES", 1))
				run(referenceSelect("SELECT v FROM public."+tc.name, []string{"v"}, []string{"INT8"}, [][]any{{int64(11)}}))
			}
		}
		raw := "CREATE TABLE public.raw_copy(v BIGINT DEFAULT nextval('pg_temp.default_seed'))"
		var diagnostic *referenceDiagnostic
		if !s.postgres {
			diagnostic = tempReferenceDiagnostic("0A000", "temporary sequence DEFAULT is unsupported", "", strconv.Itoa(strings.Index(raw, "nextval")+1))
		}
		s.utility(raw, diagnostic)
		expected := int64(31)
		if s.postgres {
			expected = 33
		}
		s.state(s.c, "pg_temp.default_seed", expected, s.postgres)
		for i, options := range []string{"", " EXCLUDING DEFAULTS", " INCLUDING DEFAULTS EXCLUDING DEFAULTS", " INCLUDING ALL EXCLUDING DEFAULTS"} {
			name := "public.allowed_" + strconv.Itoa(i)
			s.utility("CREATE TABLE "+name+"(LIKE pg_temp.temp_source"+options+")", nil)
			run(referenceExec("INSERT INTO "+name+" DEFAULT VALUES", 1))
			run(referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{nil}}))
		}
		s.utility("CREATE TABLE public.identity_only(LIKE pg_temp.combined INCLUDING ALL EXCLUDING DEFAULTS)", nil)
		run(referenceExec("INSERT INTO public.identity_only DEFAULT VALUES", 1))
		run(referenceSelect("SELECT id,v FROM public.identity_only", []string{"id", "v"}, []string{"INT8", "INT8"}, [][]any{{int64(7), nil}}))
		if s.postgres {
			s.utility("DROP SEQUENCE default_seed CASCADE", nil)
			run(referenceExec("INSERT INTO public.permanent_copy DEFAULT VALUES", 1))
			run(referenceSelect("SELECT v FROM public.permanent_copy ORDER BY v NULLS LAST", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {nil}}))
		}
	})
}

func TestReferenceTableLikeTemporaryAllowedDefaults(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TEMP TABLE constant_source(v INT DEFAULT 11)", nil)
		s.utility("CREATE TABLE public.constant_copy(LIKE pg_temp.constant_source INCLUDING DEFAULTS)", nil)
		run(referenceExec("INSERT INTO public.constant_copy DEFAULT VALUES", 1))
		s.utility("DROP TABLE constant_source", nil)
		run(referenceExec("INSERT INTO public.constant_copy DEFAULT VALUES", 1))
		temporaryTableRows(s, s.c, "public.constant_copy", 11, 11)
		s.utility("CREATE SEQUENCE public.permanent_seed START 51 CACHE 1", nil)
		s.utility("CREATE TEMP TABLE permanent_seed_source(v INT DEFAULT nextval('public.permanent_seed'))", nil)
		s.utility("CREATE TABLE public.permanent_seed_copy(LIKE pg_temp.permanent_seed_source INCLUDING DEFAULTS)", nil)
		s.utility("CREATE TEMP TABLE temporary_seed_copy(LIKE pg_temp.permanent_seed_source INCLUDING DEFAULTS)", nil)
		run(referenceExec("INSERT INTO permanent_seed_source DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO public.permanent_seed_copy DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO permanent_seed_source DEFAULT VALUES", 1))
		temporaryTableRows(s, s.c, "permanent_seed_source", 51, 53)
		temporaryTableRows(s, s.c, "public.permanent_seed_copy", 52)
		s.utility("DROP TABLE permanent_seed_source", nil)
		run(referenceExec("INSERT INTO public.permanent_seed_copy DEFAULT VALUES", 1))
		run(referenceExec("INSERT INTO temporary_seed_copy DEFAULT VALUES", 1))
		temporaryTableRows(s, s.c, "public.permanent_seed_copy", 52, 54)
		temporaryTableRows(s, s.c, "temporary_seed_copy", 55)
	})
}
