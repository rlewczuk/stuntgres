package engine

import (
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCheckProgramPublicationCurrentAuthority(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(map[bool]string{false: "row", true: "folded"}[folded]+"/"+map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
				e := New()
				admin, finish := roleOperation(t, e, "postgres")
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
					roleExecute(t, e, admin, q)
				}
				reader, readerDone := roleOperation(t, e, "check_publication_reader")
				defer readerDone()
				if explicit {
					roleExecute(t, e, reader, "BEGIN")
				}
				scope, err := e.StartStatement(reader)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(nil)
				result, err := scope.Execute(statement(t, "INSERT INTO public.check_publication_rows VALUES(7,'[1]')"), 0, nil)
				if err != nil || result.Count != 1 {
					t.Fatal("Execute", result, err)
				}
				sequence := catalogRef(t, e, e.CatalogRead(), "check_publication_steps")
				e.mu.Lock()
				generator := e.storage.Sequences[sequence.Address]
				before := *generator
				e.mu.Unlock()
				if before.Last != 1 || !before.Called {
					t.Fatal("Execute did not evaluate genuine truth once", before)
				}
				roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM check_publication_reader")
				nullCountUnitDiagnostic(t, scope.Finish(nil), sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
				e.mu.Lock()
				after := *generator
				e.mu.Unlock()
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
					roleExecute(t, e, reader, "ROLLBACK")
				}
				roleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO check_publication_reader")
				if r := sessionSQL(t, reader.Session, "INSERT INTO public.check_publication_rows VALUES(7,'[1]')"); r.Count != 1 {
					t.Fatal(r)
				}
				e.mu.Lock()
				last := generator.Last
				e.mu.Unlock()
				if last != 2 {
					t.Fatal("fresh retry truth", last)
				}
				checkProgramRows(t, e, "SELECT k FROM public.check_publication_rows ORDER BY k", 7, 99)
			})
		}
	}
}
