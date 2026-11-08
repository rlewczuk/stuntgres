package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func tempAsAdmissionHeap(s *fixture.TempReferenceSession) {
	s.T.Helper()
	tempAsRun(s, s.C, fixture.Select("SELECT id,k,payload FROM public.ta_restricted_heap ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(3), "c"}, {int64(2), int64(1), "a"}, {int64(3), int64(2), "b"}}))
}

func tempAsAdmissionIndex(s *fixture.TempReferenceSession, selected, clustered bool) {
	s.T.Helper()
	tempAsRun(s, s.C, fixture.Select("SELECT c.relname,c.oid=CASE c.relname WHEN 'ta_restricted_idx' THEN b.expr_oid WHEN 'ta_restricted_plain' THEN b.plain_oid ELSE b.pk_oid END AS same_oid,i.indisclustered,i.indisvalid,i.indisready,i.indislive FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid CROSS JOIN ta_restricted_identity b WHERE i.indrelid=b.heap_oid ORDER BY c.relname", []string{"relname", "same_oid", "indisclustered", "indisvalid", "indisready", "indislive"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"ta_restricted_heap_pkey", true, false, true, true, true}, {"ta_restricted_idx", true, clustered && selected, true, true, true}, {"ta_restricted_plain", true, clustered && !selected, true, true, true}}))
	tempAsRun(s, s.C, fixture.Select("SELECT h.oid=b.heap_oid AS same_heap,f.oid=b.function_oid AS same_function,f.provolatile::text AS volatility FROM pg_class h CROSS JOIN pg_proc f CROSS JOIN ta_restricted_identity b WHERE h.relname='ta_restricted_heap' AND f.proname='ta_restricted_key'", []string{"same_heap", "same_function", "volatility"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{true, true, "v"}}))
}

func tempAsAdmissionCaller(s *fixture.TempReferenceSession) {
	s.T.Helper()
	tempAsRun(s, s.C, fixture.Select("SELECT current_user=b.actor AS same_actor,session_user=b.authenticated AS same_session,current_setting('application_name') AS app,current_setting('search_path') AS path FROM ta_restricted_identity b", []string{"same_actor", "same_session", "app", "path"}, []string{"BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, "ctas caller", "public, pg_catalog"}}))
}

func tempAsAdmissionRestricted(s *fixture.TempReferenceSession, _ *[]fixture.Diagnostic, selected, noData, alias bool) {
	tempAsAdmissionSettings(s)
	var application, path string
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT current_setting('application_name'),current_setting('search_path')").Scan(&application, &path); err != nil {
		s.T.Fatal(err)
	}
	defer s.C.ExecContext(s.P.Ctx, "SELECT set_config('application_name',$1,false)", application)
	defer s.C.ExecContext(s.P.Ctx, "SELECT set_config('search_path',$1,false)", path)
	creation := "CREATE TEMP TABLE IF NOT EXISTS ta_restricted_leak AS SELECT 1::integer AS v"
	if alias {
		creation = "CREATE TABLE IF NOT EXISTS pg_temp.ta_restricted_leak AS SELECT 1::integer AS v"
	}
	if noData {
		creation += " WITH NO DATA"
	}
	// Build the immutable index with its side-effect branch inactive, then arm
	// the real volatile body only for existing-index maintenance.
	for _, q := range []string{
		"CREATE TEMP TABLE ta_restricted_sentinel(v INT)",
		"INSERT INTO ta_restricted_sentinel VALUES(73)",
		"CREATE TABLE public.ta_restricted_heap(id INT PRIMARY KEY,k INT,payload TEXT)",
		"INSERT INTO public.ta_restricted_heap VALUES(1,3,'c'),(2,1,'a'),(3,2,'b')",
		"SET application_name='ctas setup'",
		"CREATE INDEX ta_restricted_plain ON public.ta_restricted_heap(k)",
		"CREATE FUNCTION public.ta_restricted_effect(integer) RETURNS integer LANGUAGE SQL VOLATILE AS $$SELECT pg_catalog.set_config('application_name','ctas restricted',false); " + creation + "; SELECT $1$$",
		"CREATE FUNCTION public.ta_restricted_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT CASE WHEN pg_catalog.current_setting('application_name')='ctas caller' THEN public.ta_restricted_effect($1) ELSE $1 END$$",
		"CREATE INDEX ta_restricted_idx ON public.ta_restricted_heap(public.ta_restricted_key(k))",
		"CREATE TEMP TABLE ta_restricted_identity(heap_oid OID,expr_oid OID,plain_oid OID,pk_oid OID,function_oid OID,actor NAME,authenticated NAME)",
	} {
		n := int64(0)
		if q == "INSERT INTO ta_restricted_sentinel VALUES(73)" {
			n = 1
		} else if q == "INSERT INTO public.ta_restricted_heap VALUES(1,3,'c'),(2,1,'a'),(3,2,'b')" {
			n = 3
		}
		tempAsExec(s, s.C, q, n)
	}
	tempAsAbsent(s, "ta_restricted_leak")
	tempAsExec(s, s.C, "INSERT INTO ta_restricted_identity SELECT h.oid,x.oid,p.oid,k.oid,f.oid,current_user,session_user FROM pg_class h CROSS JOIN pg_class x CROSS JOIN pg_class p CROSS JOIN pg_class k CROSS JOIN pg_proc f WHERE h.relname='ta_restricted_heap' AND x.relname='ta_restricted_idx' AND p.relname='ta_restricted_plain' AND k.relname='ta_restricted_heap_pkey' AND f.proname='ta_restricted_key'", 1)
	tempAsExec(s, s.C, "ALTER FUNCTION public.ta_restricted_key(integer) VOLATILE", 0)
	tempAsAdmissionIndex(s, selected, false)
	tempAsExec(s, s.C, "SET application_name='ctas caller'", 0)
	tempAsExec(s, s.C, "SET search_path=public,pg_catalog", 0)
	tempAsAdmissionCaller(s)
	q := "CLUSTER public.ta_restricted_heap USING ta_restricted_plain"
	if selected {
		q = "CLUSTER public.ta_restricted_heap USING ta_restricted_idx"
	}
	d := fixture.ControlsReferenceDiagnostic("42501", "cannot create temporary table within security-restricted operation")
	d.Context = "SQL function \"ta_restricted_effect\" statement 2\nSQL function \"ta_restricted_key\" statement 2"
	step := fixture.ErrorDiagnostic(q, d)
	step.ErrorStage = "execute"
	tempAsRun(s, s.C, step)
	tempAsAbsent(s, "ta_restricted_leak")
	tempAsRows(s, s.C, "ta_restricted_sentinel", 73)
	tempAsAdmissionHeap(s)
	tempAsAdmissionIndex(s, selected, false)
	tempAsAdmissionCaller(s)
	// Restore the captured declaration before replacing its body, then return
	// to VOLATILE. The same OID/index succeeds with only the TEMP attempt removed.
	tempAsExec(s, s.C, "ALTER FUNCTION public.ta_restricted_key(integer) IMMUTABLE", 0)
	tempAsExec(s, s.C, "CREATE OR REPLACE FUNCTION public.ta_restricted_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$", 0)
	tempAsExec(s, s.C, "ALTER FUNCTION public.ta_restricted_key(integer) VOLATILE", 0)
	tempAsExec(s, s.C, q, 0)
	tempAsAbsent(s, "ta_restricted_leak")
	tempAsAdmissionHeap(s)
	tempAsAdmissionIndex(s, selected, true)
	tempAsAdmissionCaller(s)
}

func TestReferenceCreateTempTableAsRestricted(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, noData := range []bool{false, true} {
			for _, alias := range []bool{false, true} {
				t.Run(fmt.Sprintf("selected-%t/no-data-%t/pg-temp-%t", selected, noData, alias), func(t *testing.T) {
					tempAsAdmissionScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
						tempAsAdmissionRestricted(s, n, selected, noData, alias)
					})
				})
			}
		}
	}
}
