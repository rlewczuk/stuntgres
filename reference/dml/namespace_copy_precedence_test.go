package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGNamespaceCopyStoragePrecedence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ns18_copy_actor", "ns18_copy_reader"})
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, peer copyTextConnection) {
		db := p.Native
		if b.PG {
			db = p.CopyPostgres
		}
		ownerConn, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer ownerConn.Close()
		owner := copyTextConnection{conn: ownerConn, pg: b.PG, ctx: p.Ctx}
		owner.exec(t, "CREATE ROLE ns18_copy_actor")
		owner.exec(t, "CREATE ROLE ns18_copy_reader")
		owner.exec(t, "CREATE TABLE public.ns18_copy_persistent(v INT)")
		owner.exec(t, "INSERT INTO public.ns18_copy_persistent VALUES(7)")
		owner.exec(t, "SET ROLE ns18_copy_actor")
		owner.exec(t, "CREATE TEMP TABLE ns18_copy_owned(v INT)")
		owner.exec(t, "INSERT INTO ns18_copy_owned VALUES(99)")
		var name string
		if err := owner.conn.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.relname='ns18_copy_owned'").Scan(&name); err != nil {
			t.Fatal(err)
		}
		defer func() {
			peer.exec(t, "ROLLBACK")
			peer.exec(t, "RESET ROLE")
			peer.exec(t, "DROP TABLE IF EXISTS public.ns18_copy_persistent")
		}()
		deny := func(query string, want fixture.Diagnostic) {
			t.Helper()
			reader := &copyTextNoRead{}
			result, err := peer.from(query, reader)
			copyTextDiagnostic(t, result, err, want)
			// Native admission must not consume a stream before either denied gate.
			// PostgreSQL's client transport owns its independent reader scheduling.
			if !b.PG && reader.calls != 0 {
				t.Fatal("denied native COPY consumed input", reader.calls)
			}
			peer.exec(t, "ROLLBACK")
			owner.selectRows(t, "SELECT v FROM ns18_copy_owned", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
		}
		// Actual bootstrap namespace/column access succeeds, so read-only must
		// precede the native foreign-storage capability boundary.
		peer.exec(t, "BEGIN READ ONLY")
		deny("COPY "+name+".ns18_copy_owned FROM STDIN", fixture.Diagnostic{Code: "25006", Message: "cannot execute COPY FROM in a read-only transaction"})
		// Ordinary schema denial stays ahead of read-only/storage admission.
		peer.exec(t, "SET ROLE ns18_copy_reader")
		peer.exec(t, "BEGIN READ ONLY")
		deny("COPY "+name+".ns18_copy_owned FROM STDIN", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema " + name})
		if b.PG {
			// Positive namespace USAGE reaches the independent table INSERT
			// denial; never grant CREATE or provision the denied TEMP schema.
			peer.exec(t, "RESET ROLE")
			peer.exec(t, "GRANT USAGE ON SCHEMA public TO ns18_copy_reader")
			peer.exec(t, "SET ROLE ns18_copy_reader")
		}
		// A valid PUBLIC namespace does not grant table INSERT: column/table ACL
		// denial also remains ahead of the read-only check.
		peer.exec(t, "BEGIN READ ONLY")
		deny("COPY public.ns18_copy_persistent FROM STDIN", fixture.Diagnostic{Code: "42501", Message: "permission denied for table ns18_copy_persistent"})
		peer.exec(t, "RESET ROLE")
		peer.exec(t, "GRANT INSERT ON public.ns18_copy_persistent TO ns18_copy_reader")
		peer.exec(t, "SET ROLE ns18_copy_reader")
		peer.exec(t, "BEGIN READ ONLY")
		deny("COPY public.ns18_copy_persistent FROM STDIN", fixture.Diagnostic{Code: "25006", Message: "cannot execute COPY FROM in a read-only transaction"})
		peer.exec(t, "RESET ROLE")
		// The physical owner may COPY its own TEMP table even in read-only TX.
		owner.exec(t, "BEGIN READ ONLY")
		result, err := owner.from("COPY ns18_copy_owned FROM STDIN", strings.NewReader("100\n"))
		copyTextSuccess(t, result, err, 1)
		owner.exec(t, "COMMIT")
		owner.selectRows(t, "SELECT v FROM ns18_copy_owned ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}, {int64(100)}})
		result, err = peer.from("COPY public.ns18_copy_persistent FROM STDIN", strings.NewReader("8\n"))
		copyTextSuccess(t, result, err, 1)
		peer.selectRows(t, "SELECT v FROM public.ns18_copy_persistent ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}})
		// Keep ordinary owner column ACL precedence independently observable.
		owner.exec(t, "REVOKE INSERT ON ns18_copy_owned FROM ns18_copy_actor")
		owner.exec(t, "BEGIN READ ONLY")
		reader := &copyTextNoRead{}
		result, err = owner.from("COPY ns18_copy_owned FROM STDIN", reader)
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ns18_copy_owned"})
		if !b.PG && reader.calls != 0 {
			t.Fatal("column-denied native COPY consumed input", reader.calls)
		}
		owner.exec(t, "ROLLBACK")
		owner.exec(t, "GRANT INSERT ON ns18_copy_owned TO ns18_copy_actor")
		owner.selectRows(t, "SELECT v FROM ns18_copy_owned ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}, {int64(100)}})
	})
}
