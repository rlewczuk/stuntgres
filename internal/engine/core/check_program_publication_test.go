package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestCheckProgramPublicationCurrentAuthority(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(map[bool]string{false: "row", true: "folded"}[folded]+"/"+map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
				e := engine.New()
				admin, finish := enginetest.RoleOperation(t, e, "postgres")
				defer finish()
				for _, q := range []string{
					"CREATE ROLE check_publication_reader LOGIN",
					"CREATE SEQUENCE public.check_publication_steps START 1",
					"CREATE TABLE public.check_publication_rows(k INT,j JSON,CONSTRAINT a_call CHECK(" + checkProgramExpression(folded) + "),CONSTRAINT z_truth CHECK(nextval('public.check_publication_steps'::regclass)>0))",
					"INSERT INTO public.check_publication_rows VALUES(99,'[9]')",
					"ALTER SEQUENCE public.check_publication_steps RESTART WITH 1",
					"GRANT USAGE ON SCHEMA public TO check_publication_reader",
					"GRANT USAGE ON SEQUENCE public.check_publication_steps TO check_publication_reader",
					"GRANT INSERT ON public.check_publication_rows TO check_publication_reader",
					"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC",
					"GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO check_publication_reader",
				} {
					enginetest.RoleExecute(t, e, admin, q)
				}
				reader, readerDone := enginetest.RoleOperation(t, e, "check_publication_reader")
				defer readerDone()
				if explicit {
					enginetest.RoleExecute(t, e, reader, "BEGIN")
				}
				scope, err := e.StartStatement(reader)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(nil)
				result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO public.check_publication_rows VALUES(7,'[1]')"), 0, nil)
				if err != nil || result.Count != 1 {
					t.Fatal("Execute", result, err)
				}
				sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "check_publication_steps")
				e.Mu.Lock()
				generator := e.Storage.Sequences[sequence.Address]
				before := *generator
				e.Mu.Unlock()
				if before.Last != 1 || !before.Called {
					t.Fatal("Execute did not evaluate genuine truth once", before)
				}
				enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM check_publication_reader")
				nullCountUnitDiagnostic(t, scope.Finish(nil), sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
				e.Mu.Lock()
				after := *generator
				e.Mu.Unlock()
				if after != before {
					t.Fatal("Finish replayed CHECK truth", before, after)
				}
				owner := reader.Session.Transactions
				owner.Mu.Lock()
				retainedWrites := len(owner.Writes)
				retainedDeletes := len(owner.Deletes)
				owner.Mu.Unlock()
				if retainedWrites != 0 || retainedDeletes != 0 {
					t.Fatal("failed Finish installed an explicit/private write stage", retainedWrites, retainedDeletes)
				}
				checkProgramRows(t, e, "SELECT k FROM public.check_publication_rows ORDER BY k", 99)
				if explicit {
					// End the refused explicit transaction before fresh recovery.
					enginetest.RoleExecute(t, e, reader, "ROLLBACK")
				}
				enginetest.RoleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO check_publication_reader")
				if r := enginetest.SessionSQL(t, reader.Session, "INSERT INTO public.check_publication_rows VALUES(7,'[1]')"); r.Count != 1 {
					t.Fatal(r)
				}
				e.Mu.Lock()
				last := generator.Last
				e.Mu.Unlock()
				if last != 2 {
					t.Fatal("fresh retry truth", last)
				}
				checkProgramRows(t, e, "SELECT k FROM public.check_publication_rows ORDER BY k", 7, 99)
			})
		}
	}
}
