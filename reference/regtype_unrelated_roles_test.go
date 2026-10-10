package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceRegtypeUnrelatedRoles(t *testing.T) {
	roleReferenceCleanup(t, []string{"regtype_unrelated_role"})
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		var original int64
		if err := conn.QueryRowContext(p.ctx, "SELECT 'regtype_a.same'::regtype::bigint").Scan(&original); err != nil {
			t.Fatal(err)
		}
		const query = "SELECT 'same'::regtype::bigint AS oid,'same'::regtype AS r"
		held, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}
		regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, "same"}}))
		const warningQuery = "SELECT 'timestamp(7)'::regtype AS a"
		*notices = nil
		warning, err := conn.PrepareContext(p.ctx, warningQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer warning.Close()
		regtypeAssertNotices(t, *notices, []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}})
		*notices = nil
		warningExecutor := referenceHeldStatement{referenceExecutor: conn, statement: warning, query: warningQuery}
		regtypeRunStep(t, p.ctx, warningExecutor, recorder, regtypeSelect(warningQuery, "timestamp without time zone"))
		regtypeAssertNotices(t, *notices, nil)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP DOMAIN regtype_a.same", 0))
		regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, fmt.Sprint(original)}}))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_a.same AS integer", 0))
		var recreated int64
		if err := conn.QueryRowContext(p.ctx, "SELECT 'regtype_a.same'::regtype::bigint").Scan(&recreated); err != nil {
			t.Fatal(err)
		}
		if original == 0 || recreated == 0 || original == recreated {
			t.Fatal("drop/recreate must allocate a distinct backend-local identity", original, recreated)
		}
		regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, fmt.Sprint(original)}}))
		for _, change := range []string{"CREATE ROLE regtype_unrelated_role", "DROP ROLE regtype_unrelated_role"} {
			*notices = nil
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(change, 0))
			regtypeAssertNotices(t, *notices, nil)
			// Principal, database and search_path are unchanged: unrelated role
			// mutations neither retarget the old literal nor repeat its warning.
			regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{original, fmt.Sprint(original)}}))
			regtypeRunStep(t, p.ctx, warningExecutor, recorder, regtypeSelect(warningQuery, "timestamp without time zone"))
			regtypeAssertNotices(t, *notices, nil)
			*notices = nil
			// A newly prepared literal still warns once; unrelated role changes
			// must not suppress validation on genuinely new analysis.
			fresh, err := conn.PrepareContext(p.ctx, warningQuery)
			if err != nil {
				t.Fatal(err)
			}
			regtypeAssertNotices(t, *notices, []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}})
			*notices = nil
			regtypeRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: fresh, query: warningQuery}, recorder, regtypeSelect(warningQuery, "timestamp without time zone"))
			regtypeAssertNotices(t, *notices, nil)
			fresh.Close()
		}
	})
}

func TestReferenceRegtypeUnrelatedRolesQualifiedAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"regtype_authority_owner_a", "regtype_authority_owner_b"})
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		for _, query := range []string{
			"CREATE ROLE regtype_authority_owner_a", "CREATE ROLE regtype_authority_owner_b",
			"CREATE SCHEMA regtype_private AUTHORIZATION regtype_authority_owner_a",
			"CREATE DOMAIN regtype_private.secret AS integer", "SET search_path TO public",
			"SET ROLE regtype_authority_owner_a",
		} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(query, 0))
		}
		defer conn.ExecContext(p.ctx, "RESET ROLE")
		const literal = "SELECT 'regtype_private.secret'::regtype AS a"
		const frame = "SELECT $1::regtype AS r"
		held, err := conn.PrepareContext(p.ctx, literal)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		input, err := conn.PrepareContext(p.ctx, frame)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		executor := referenceHeldStatement{referenceExecutor: conn, statement: held, query: literal}
		regtypeRunStep(t, p.ctx, executor, recorder, regtypeSelect(literal, "regtype_private.secret"))
		// Administrative peer mutates relevant qualified-schema USAGE without
		// changing the held connection's principal or effective search path.
		referenceRunStep(t, p.ctx, backend.db, recorder, referenceExec("ALTER SCHEMA regtype_private OWNER TO regtype_authority_owner_b", 0))
		want := controlsReferenceDiagnostic("42501", "permission denied for schema regtype_private")
		want.Position = "8"
		rows, err := held.QueryContext(p.ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("failed qualified literal exposed cursor")
		}
		referenceAssertDiagnostic(t, err, &want)
		rows, err = input.QueryContext(p.ctx, "regtype_private.secret")
		if rows != nil {
			rows.Close()
			t.Fatal("failed qualified frame exposed cursor")
		}
		frameWant := controlsReferenceDiagnostic("42501", "permission denied for schema regtype_private")
		frameWant.Context = "unnamed portal parameter $1 = '...'"
		referenceAssertDiagnostic(t, err, &frameWant)
		fresh, err := conn.PrepareContext(p.ctx, literal)
		if fresh != nil {
			fresh.Close()
			t.Fatal("new literal Prepare unexpectedly admitted inaccessible schema")
		}
		if err == nil {
			t.Fatal("new literal Prepare must enforce current schema USAGE")
		}
		referenceAssertDiagnostic(t, err, &want)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET ROLE regtype_authority_owner_b", 0))
		regtypeRunStep(t, p.ctx, executor, recorder, regtypeSelect(literal, "regtype_private.secret"))
		regtypeRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: input, query: frame}, recorder, referenceSelect(frame, []string{"r"}, []string{"REGTYPE"}, [][]any{{"regtype_private.secret"}}, "regtype_private.secret"))
		referenceRunStep(t, p.ctx, backend.db, recorder, referenceExec("ALTER SCHEMA regtype_private OWNER TO regtype_authority_owner_a", 0))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET ROLE regtype_authority_owner_a", 0))
		regtypeRunStep(t, p.ctx, executor, recorder, regtypeSelect(literal, "regtype_private.secret"))
	})
}
