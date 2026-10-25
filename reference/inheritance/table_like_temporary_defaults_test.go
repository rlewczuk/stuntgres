package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeTemporaryDefaults(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.Utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		run(fixture.Exec("INSERT INTO temp_source DEFAULT VALUES", 1))
		s.State(s.C, "pg_temp.default_seed", 31, true)
		s.Utility("CREATE TEMP TABLE temp_copy(lead TEXT,LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		run(fixture.Select("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, nil))
		fixture.TableDDLMetadata(s.T, s.P.Ctx, s.C, s.Postgres, s.Prepared, "SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, []bool{true, true}, 0)
		run(fixture.Select("SELECT pg_get_serial_sequence('pg_temp.temp_copy','v') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Select("SELECT a.oid<>b.oid AS fresh FROM pg_attrdef a,pg_attrdef b WHERE a.adrelid='pg_temp.temp_source'::regclass::oid AND b.adrelid='pg_temp.temp_copy'::regclass::oid", []string{"fresh"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Select("SELECT count(*) AS dependencies FROM pg_depend d,pg_attrdef a WHERE a.adrelid='pg_temp.temp_copy'::regclass::oid AND d.objid=a.oid AND d.refobjid='pg_temp.default_seed'::regclass::oid AND d.deptype='n'", []string{"dependencies"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.State(s.C, "pg_temp.default_seed", 31, true)
		run(fixture.Exec("INSERT INTO temp_copy(lead) VALUES('x')", 1))
		run(fixture.Exec("INSERT INTO temp_source DEFAULT VALUES", 1))
		run(fixture.Select("SELECT * FROM temp_copy", []string{"lead", "v"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(32)}}))
		run(fixture.Select("SELECT v FROM temp_source ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {int64(33)}}))
		next := int64(34)
		for i, options := range []string{"", " EXCLUDING DEFAULTS", " INCLUDING DEFAULTS EXCLUDING DEFAULTS", " INCLUDING ALL EXCLUDING DEFAULTS", " EXCLUDING DEFAULTS INCLUDING DEFAULTS", " INCLUDING ALL"} {
			name := "mask_" + strconv.Itoa(i)
			s.Utility("CREATE TEMP TABLE "+name+"(LIKE pg_temp.temp_source"+options+")", nil)
			run(fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1))
			var value any
			if i >= 4 {
				value = next
				next++
			}
			run(fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{value}}))
		}
		s.Utility("DROP TABLE pg_temp.temp_source RESTRICT", nil)
		run(fixture.Exec("INSERT INTO temp_copy(lead) VALUES('after')", 1))
		run(fixture.Select("SELECT v FROM temp_copy ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(32)}, {next}}))
	})
}

func TestReferenceTableLikeTemporaryProviderAuthority(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.Utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		s.Utility("CREATE TEMP TABLE temp_copy(LIKE pg_temp.temp_source INCLUDING DEFAULTS)", nil)
		run(fixture.Exec("INSERT INTO temp_copy DEFAULT VALUES", 1))
		s.Utility("DROP TABLE temp_source RESTRICT", nil)
		dependency := fixture.TempReferenceDiagnostic("2BP01", "cannot drop sequence default_seed because other objects depend on it", "default value for column v of table temp_copy depends on sequence default_seed", "")
		dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		s.Utility("DROP SEQUENCE default_seed RESTRICT", dependency)
		s.Utility("DROP SEQUENCE default_seed CASCADE", nil)
		run(fixture.Exec("INSERT INTO temp_copy DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM temp_copy ORDER BY v NULLS LAST", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {nil}}))
		run(fixture.Select("SELECT count(*) AS defaults FROM pg_attrdef WHERE adrelid='pg_temp.temp_copy'::regclass::oid", []string{"defaults"}, []string{"INT8"}, [][]any{{int64(0)}}))
		s.Utility("CREATE TEMP TABLE temp_serial(v SERIAL)", nil)
		s.Utility("CREATE TEMP TABLE serial_copy(LIKE pg_temp.temp_serial INCLUDING DEFAULTS)", nil)
		run(fixture.Exec("INSERT INTO temp_serial DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO serial_copy DEFAULT VALUES", 1))
		fixture.TemporaryTableRows(s, s.C, "temp_serial", 1)
		fixture.TemporaryTableRows(s, s.C, "serial_copy", 2)
		run(fixture.Select("SELECT pg_get_serial_sequence('pg_temp.serial_copy','v') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{nil}}))
		dependency = fixture.TempReferenceDiagnostic("2BP01", "cannot drop table temp_serial because other objects depend on it", "default value for column v of table serial_copy depends on sequence temp_serial_v_seq", "")
		dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		s.Utility("DROP TABLE temp_serial RESTRICT", dependency)
		s.Utility("DROP TABLE serial_copy,temp_serial", nil)
	})
}

func TestReferenceTableLikeTemporaryPermanentDefaultBoundary(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TEMP SEQUENCE default_seed START 31 CACHE 1", nil)
		s.Utility("CREATE TEMP TABLE temp_source(v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		s.Utility("CREATE TEMP TABLE combined(id BIGINT GENERATED BY DEFAULT AS IDENTITY(START 7 INCREMENT 3),v BIGINT DEFAULT nextval('pg_temp.default_seed'))", nil)
		for _, tc := range []struct{ name, source, options string }{{"permanent_copy", "temp_source", " INCLUDING DEFAULTS"}, {"permanent_all", "combined", " INCLUDING ALL"}, {"permanent_last", "temp_source", " EXCLUDING DEFAULTS INCLUDING DEFAULTS"}} {
			q := "CREATE TABLE public." + tc.name + "(LIKE pg_temp." + tc.source + tc.options + ")"
			var diagnostic *fixture.Diagnostic
			if !s.Postgres {
				diagnostic = fixture.TempReferenceDiagnostic("0A000", "temporary sequence DEFAULT is unsupported", "", "")
			}
			s.Utility(q, diagnostic)
			if s.Postgres {
				run(fixture.Exec("INSERT INTO public."+tc.name+" DEFAULT VALUES", 1))
			} else {
				fixture.TableLikeAbsent(run, "public."+tc.name, s.Prepared)
				if tc.source == "combined" {
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='permanent_all_id_seq'", []string{"relname"}, []string{"NAME"}, nil))
				}
				s.Utility("CREATE TABLE public."+tc.name+"(v BIGINT DEFAULT 11)", nil)
				run(fixture.Exec("INSERT INTO public."+tc.name+" DEFAULT VALUES", 1))
				run(fixture.Select("SELECT v FROM public."+tc.name, []string{"v"}, []string{"INT8"}, [][]any{{int64(11)}}))
			}
		}
		raw := "CREATE TABLE public.raw_copy(v BIGINT DEFAULT nextval('pg_temp.default_seed'))"
		var diagnostic *fixture.Diagnostic
		if !s.Postgres {
			diagnostic = fixture.TempReferenceDiagnostic("0A000", "temporary sequence DEFAULT is unsupported", "", strconv.Itoa(strings.Index(raw, "nextval")+1))
		}
		s.Utility(raw, diagnostic)
		expected := int64(31)
		if s.Postgres {
			expected = 33
		}
		s.State(s.C, "pg_temp.default_seed", expected, s.Postgres)
		for i, options := range []string{"", " EXCLUDING DEFAULTS", " INCLUDING DEFAULTS EXCLUDING DEFAULTS", " INCLUDING ALL EXCLUDING DEFAULTS"} {
			name := "public.allowed_" + strconv.Itoa(i)
			s.Utility("CREATE TABLE "+name+"(LIKE pg_temp.temp_source"+options+")", nil)
			run(fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1))
			run(fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{nil}}))
		}
		s.Utility("CREATE TABLE public.identity_only(LIKE pg_temp.combined INCLUDING ALL EXCLUDING DEFAULTS)", nil)
		run(fixture.Exec("INSERT INTO public.identity_only DEFAULT VALUES", 1))
		run(fixture.Select("SELECT id,v FROM public.identity_only", []string{"id", "v"}, []string{"INT8", "INT8"}, [][]any{{int64(7), nil}}))
		if s.Postgres {
			s.Utility("DROP SEQUENCE default_seed CASCADE", nil)
			run(fixture.Exec("INSERT INTO public.permanent_copy DEFAULT VALUES", 1))
			run(fixture.Select("SELECT v FROM public.permanent_copy ORDER BY v NULLS LAST", []string{"v"}, []string{"INT8"}, [][]any{{int64(31)}, {nil}}))
		}
	})
}

func TestReferenceTableLikeTemporaryAllowedDefaults(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TEMP TABLE constant_source(v INT DEFAULT 11)", nil)
		s.Utility("CREATE TABLE public.constant_copy(LIKE pg_temp.constant_source INCLUDING DEFAULTS)", nil)
		run(fixture.Exec("INSERT INTO public.constant_copy DEFAULT VALUES", 1))
		s.Utility("DROP TABLE constant_source", nil)
		run(fixture.Exec("INSERT INTO public.constant_copy DEFAULT VALUES", 1))
		fixture.TemporaryTableRows(s, s.C, "public.constant_copy", 11, 11)
		s.Utility("CREATE SEQUENCE public.permanent_seed START 51 CACHE 1", nil)
		s.Utility("CREATE TEMP TABLE permanent_seed_source(v INT DEFAULT nextval('public.permanent_seed'))", nil)
		s.Utility("CREATE TABLE public.permanent_seed_copy(LIKE pg_temp.permanent_seed_source INCLUDING DEFAULTS)", nil)
		s.Utility("CREATE TEMP TABLE temporary_seed_copy(LIKE pg_temp.permanent_seed_source INCLUDING DEFAULTS)", nil)
		run(fixture.Exec("INSERT INTO permanent_seed_source DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO public.permanent_seed_copy DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO permanent_seed_source DEFAULT VALUES", 1))
		fixture.TemporaryTableRows(s, s.C, "permanent_seed_source", 51, 53)
		fixture.TemporaryTableRows(s, s.C, "public.permanent_seed_copy", 52)
		s.Utility("DROP TABLE permanent_seed_source", nil)
		run(fixture.Exec("INSERT INTO public.permanent_seed_copy DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO temporary_seed_copy DEFAULT VALUES", 1))
		fixture.TemporaryTableRows(s, s.C, "public.permanent_seed_copy", 52, 54)
		fixture.TemporaryTableRows(s, s.C, "temporary_seed_copy", 55)
	})
}
