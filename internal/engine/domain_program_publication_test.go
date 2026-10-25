package engine

import (
	"context"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The actual Scope.Execute/Finish boundary exposes a deterministic authority
// change after CHECK truth but before either implicit publication or explicit
// private-stage installation. No provider callback or truth replay is involved.
func TestDomainProgramPublicationCurrentAuthority(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			admin, finish := roleOperation(t, e, "postgres")
			defer finish()
			for _, sql := range []string{
				"CREATE ROLE publication_reader LOGIN",
				"CREATE SEQUENCE public.publication_steps START 1",
				"CREATE DOMAIN public.publication_checked AS integer CHECK(nextval('public.publication_steps'::regclass)>0 AND VALUE>0)",
				"CREATE TABLE public.publication_rows(v public.publication_checked)",
				"GRANT USAGE ON SCHEMA public TO publication_reader",
				"GRANT USAGE ON DOMAIN public.publication_checked TO publication_reader",
				"GRANT USAGE ON SEQUENCE public.publication_steps TO publication_reader",
				"GRANT INSERT ON public.publication_rows TO publication_reader",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.int4gt(integer,integer) FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION pg_catalog.int4gt(integer,integer) TO publication_reader",
			} {
				roleExecute(t, e, admin, sql)
			}
			reader, readerDone := roleOperation(t, e, "publication_reader")
			defer readerDone()
			if explicit {
				roleExecute(t, e, reader, "BEGIN")
			}
			scope, err := e.StartStatement(reader)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if _, err := scope.Execute(statement(t, "INSERT INTO public.publication_rows VALUES(1::integer)"), 0, nil); err != nil {
				t.Fatal(err)
			}
			sequence := catalogRef(t, e, e.CatalogRead(), "publication_steps")
			e.Mu.Lock()
			generator := e.Storage.Sequences[sequence.Address]
			before := *generator
			e.Mu.Unlock()
			if before.Last != 1 {
				t.Fatal("actual CHECK truth did not run exactly once", before.Last)
			}
			roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.int4gt(integer,integer) FROM publication_reader")
			nullCountUnitDiagnostic(t, scope.Finish(nil), sqlstate.Error{Code: "42501", Message: "permission denied for function int4gt"})
			e.Mu.Lock()
			after := *generator
			e.Mu.Unlock()
			if after != before {
				t.Fatal("publication replayed volatile CHECK truth", before, after)
			}
			rows := sessionSQL(t, admin.Session, "SELECT v FROM public.publication_rows")
			var values [1]sqltype.Value
			if more, err := rows.Rows.Next(context.Background(), values[:]); err != nil || more {
				t.Fatal("denied private stage published", more, err)
			}
			rows.Rows.Close()
			if explicit {
				roleExecute(t, e, reader, "ROLLBACK")
			}
			roleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.int4gt(integer,integer) TO publication_reader")
			if result := sessionSQL(t, reader.Session, "INSERT INTO public.publication_rows VALUES(1::integer)"); result.Count != 1 {
				t.Fatal("fresh regrant retry", result)
			}
			e.Mu.Lock()
			last := generator.Last
			e.Mu.Unlock()
			if last != 2 {
				t.Fatal("fresh retry repeated old CHECK truth", last)
			}
		})
	}
}
