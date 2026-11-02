package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINNetworkDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, clause, code, message string }{
		{"missing", "INDEX network_bad ON network_diag USING gin(n network_missing_ops)", "42704", `operator class "network_missing_ops" does not exist for access method "gin"`},
		{"qualified_missing", "INDEX network_bad ON network_diag USING gin(n public.network_missing_ops)", "42704", `operator class "public.network_missing_ops" does not exist for access method "gin"`},
		{"inet_cidr", "INDEX network_bad ON network_diag USING gin(n cidr_ops)", "42804", `operator class "cidr_ops" does not accept data type inet`},
		{"uuid_macaddr", "INDEX network_bad ON network_diag USING gin(u macaddr_ops)", "42804", `operator class "macaddr_ops" does not accept data type uuid`},
		{"late_key", "INDEX network_bad ON network_diag USING gin(n inet_ops,u macaddr_ops)", "42804", `operator class "macaddr_ops" does not accept data type uuid`},
		{"unique", "UNIQUE INDEX network_bad ON network_diag USING gin(n)", "0A000", `access method "gin" does not support unique indexes`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
				run(fixture.Exec("CREATE TABLE network_diag(id integer,n inet,u uuid)", 0))
				run(fixture.Exec("INSERT INTO network_diag VALUES(1,'10.0.0.0/24','80000000-0000-0000-0000-000000000000'),(2,NULL,NULL)", 2))
				run(fixture.Exec("CREATE INDEX network_good ON network_diag USING gin(n inet_ops)", 0))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE "+tc.clause, d)
					compoundIndexAbsent(run, "network_diag", "network_bad", 1)
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM network_diag ORDER BY id", []int64{1, 2}))
					run(ginIDs("SELECT id FROM network_diag WHERE n='10.0.0.0/24'::inet ORDER BY id", []int64{1}))
					run(fixture.Exec("CREATE INDEX network_bad ON network_diag USING gin(n inet_ops,u uuid_ops)", 0))
					run(fixture.Exec("DROP INDEX network_bad", 0))
				}
			})
		})
	}
	t.Run("distance_boundaries", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			for _, tc := range networkCases[:5] {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT " + networkLiteral(tc.lower, tc.typ) + " <-> " + networkLiteral(tc.boundary, tc.typ)
					d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.typ+" <-> "+tc.typ)
					d.Hint = compoundOperatorHint
					d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "<->")]) + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
					s.Prepared = true
					s.ErrorStage = "prepare"
					run(s)
				}
			}
			run(amCount("SELECT count(*) FROM pg_opclass c JOIN pg_depend d ON d.objid=c.oid AND d.classid='pg_opclass'::regclass WHERE c.opcmethod=783 AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin')", 0))
		})
	})
	t.Run("savepoint_and_cancel", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			run(fixture.Exec("CREATE TABLE network_atomic(id integer,n inet,u uuid)", 0))
			run(fixture.Exec("INSERT INTO network_atomic VALUES(1,'10.0.0.0/24','80000000-0000-0000-0000-000000000000'),(2,NULL,NULL)", 2))
			const bad = "CREATE INDEX network_atomic_i ON network_atomic USING gin(n inet_ops,u macaddr_ops)"
			d := fixture.ControlsReferenceDiagnostic("42804", `operator class "macaddr_ops" does not accept data type uuid`)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT network_before", 0))
			s := fixture.ErrorDiagnostic(bad, d)
			s.ErrorStage = "execute"
			run(s)
			aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			run(fixture.ErrorDiagnostic("SELECT 1", aborted))
			run(fixture.Exec("ROLLBACK TO network_before", 0))
			compoundIndexAbsent(run, "network_atomic", "network_atomic_i", 0)
			run(fixture.Exec("CREATE INDEX network_atomic_i ON network_atomic USING gin(n inet_ops,u uuid_ops)", 0))
			run(fixture.Exec("INSERT INTO network_atomic VALUES(3,'10.0.0.0/24',NULL)", 1))
			run(ginIDs("SELECT id FROM network_atomic WHERE n='10.0.0.0/24'::inet ORDER BY id", []int64{1, 3}))
			run(fixture.Exec("ROLLBACK", 0))
			compoundIndexAbsent(run, "network_atomic", "network_atomic_i", 0)
			run(ginIDs("SELECT id FROM network_atomic ORDER BY id", []int64{1, 2}))
			// A fast pq command may complete before its cancellation reaches the
			// server and may retire its borrower. Pre-canceled ordinary-context
			// semantics are a native-control boundary, not a pq timing contract.
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := conn.ExecContext(ctx, "CREATE INDEX network_atomic_i ON network_atomic USING gin(n inet_ops,u uuid_ops)")
				if !errors.Is(err, context.Canceled) || result != nil {
					t.Fatalf("canceled creation result=%v err=%v", result, err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatalf("context error acquired SQLSTATE: %v", err)
				}
				compoundIndexAbsent(run, "network_atomic", "network_atomic_i", 0)
			}
			run(fixture.Exec("CREATE INDEX network_atomic_i ON network_atomic USING gin(n inet_ops,u uuid_ops)", 0))
		})
	})
}
