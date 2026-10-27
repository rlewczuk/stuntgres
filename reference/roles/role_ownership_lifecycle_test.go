package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var ownershipFreshFamilies = []struct{ create, alter, noun string }{
	{"", "ALTER TABLE own_fresh", "table own_fresh"},
	{"CREATE VIEW own_fresh_view AS SELECT 7::INT AS id", "ALTER VIEW own_fresh_view", "view own_fresh_view"},
	{"CREATE MATERIALIZED VIEW own_fresh_mv AS SELECT 7::INT AS id", "ALTER MATERIALIZED VIEW own_fresh_mv", "materialized view own_fresh_mv"},
	{"CREATE SEQUENCE own_fresh_seq", "ALTER SEQUENCE own_fresh_seq", "sequence own_fresh_seq"},
	{"CREATE TYPE own_fresh_enum AS ENUM('a')", "ALTER TYPE own_fresh_enum", "type own_fresh_enum"},
	{"CREATE DOMAIN own_fresh_domain AS INT", "ALTER DOMAIN own_fresh_domain", "type own_fresh_domain"},
	{"CREATE TYPE own_fresh_composite AS(id INT)", "ALTER TYPE own_fresh_composite", "type own_fresh_composite"},
	{"CREATE COLLATION own_fresh_coll FROM \"C\"", "ALTER COLLATION own_fresh_coll", "collation own_fresh_coll"},
	{"CREATE SCHEMA own_schema AUTHORIZATION own_source", "ALTER SCHEMA own_schema", "schema own_schema"},
}

func TestReferenceRoleOwnershipDiagnosticsAndFreshness(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipSeed(s, "own_fresh", "own_source")
		for _, tc := range ownershipFreshFamilies {
			if tc.create != "" {
				ownershipDefinition(s, tc.create)
				s.Exec(tc.alter + " OWNER TO own_source")
			}
		}
		ownershipFailure(s, "ALTER TABLE own_missing OWNER TO own_absent", fixture.Diagnostic{Code: "42P01", Message: `relation "own_missing" does not exist`})
		ownershipFailure(s, "ALTER DOMAIN own_fresh_enum OWNER TO own_target", fixture.Diagnostic{Code: "42809", Message: "own_fresh_enum is not a domain"})
		ownershipFailure(s, "ALTER TYPE own_fresh OWNER TO own_target", fixture.Diagnostic{Code: "42809", Message: "own_fresh is a table's row type", Hint: "Use ALTER TABLE instead."})
		ownershipFailure(s, "ALTER TYPE _own_fresh_enum OWNER TO own_target", fixture.Diagnostic{Code: "42809", Message: "cannot alter array type own_fresh_enum[]", Hint: "You can alter type own_fresh_enum, which will alter the array type as well."})
		q := "ALTER TABLE own_late OWNER TO own_late_role"
		stmt, err := s.C.PrepareContext(s.P.Ctx, q)
		if err != nil {
			s.T.Fatal("effect-free prepare before names exist", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: q}
		missingObject := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P01", `relation "own_late" does not exist`))
		missingObject.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, missingObject)
		s.Exec("CREATE TABLE own_late(id INT)")
		want := fixture.ControlsReferenceDiagnostic("42704", `role "own_late_role" does not exist`)
		step := fixture.ErrorDiagnostic(q, want)
		step.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		s.Exec("CREATE ROLE own_late_role")
		defer func() { s.Actor(""); s.Exec("DROP OWNED BY own_late_role CASCADE"); s.Exec("DROP ROLE own_late_role") }()
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 0))
		ownershipOwner(s, "own_late", "own_late_role")
		s.Exec("REASSIGN OWNED BY own_late_role TO own_source")
		s.Exec("DROP ROLE own_late_role")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		s.Exec("CREATE ROLE own_late_role")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 0))
		ownershipOwner(s, "own_late", "own_late_role")
		s.Exec("DROP TABLE own_late")
		s.Exec("CREATE TABLE own_late(id INT)")
		s.Run(fixture.Exec("INSERT INTO own_late VALUES(7)", 1))
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 0))
		ownershipRows(s, "own_late")
		selectorQ := "ALTER TABLE own_fresh OWNER TO CURRENT_USER"
		selector := predefinedHeld(s, selectorQ)
		s.Actor("own_source")
		fixture.RunStep(s.T, s.P.Ctx, selector, s.Record, fixture.Exec(selectorQ, 0))
		s.Actor("")
		fixture.RunStep(s.T, s.P.Ctx, selector, s.Record, fixture.Exec(selectorQ, 0))
		ownershipOwner(s, "own_fresh", s.Bootstrap)
		for _, q := range []string{"ALTER TABLE own_fresh OWNER TO own_target trailing", "REASSIGN OWNED BY own_source TO own_target trailing", "DROP OWNED BY own_source CASCADE trailing"} {
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "trailing"`)
			d.Position = strconv.Itoa(strings.Index(q, "trailing") + 1)
			step := fixture.ErrorDiagnostic(q, d)
			step.Prepared = s.Prepared
			if s.Prepared {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
			ownershipOwner(s, "own_fresh", s.Bootstrap)
			ownershipRows(s, "own_fresh")
		}
		s.Exec("ALTER TABLE own_fresh OWNER TO own_source")
		s.Actor("own_source")
		graphQ := "ALTER TABLE own_fresh OWNER TO own_target"
		graphHeld := predefinedHeld(s, graphQ)
		s.Actor("")
		s.Exec("GRANT own_target TO own_source WITH INHERIT FALSE, SET FALSE")
		s.Actor("own_source")
		denied := fixture.ErrorDiagnostic(graphQ, fixture.ControlsReferenceDiagnostic("42501", `must be able to SET ROLE "own_target"`))
		denied.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, graphHeld, s.Record, denied)
		s.Actor("")
		s.Exec("GRANT own_target TO own_source WITH SET TRUE")
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_target", func() { fixture.RunStep(s.T, s.P.Ctx, graphHeld, s.Record, fixture.Exec(graphQ, 0)) })
		s.Actor("")
		ownershipOwner(s, "own_fresh", "own_target")
	})
}

func TestReferenceRoleOwnershipTransactions(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE SEQUENCE own_tx_sequence")
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		peerOwner := func(owner string) {
			fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname='own_tx_sequence'", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}}))
		}
		s.Exec("BEGIN")
		s.Exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_source")
		ownershipOwner(s, "own_tx_sequence", "own_source")
		peerOwner(s.Bootstrap)
		s.Exec("SAVEPOINT owner_save")
		s.Exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_target")
		ownershipOwner(s, "own_tx_sequence", "own_target")
		s.Exec("ROLLBACK TO owner_save")
		ownershipOwner(s, "own_tx_sequence", "own_source")
		s.Exec("COMMIT")
		peerOwner("own_source")
		s.Exec("BEGIN")
		s.Exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_target")
		s.Exec("ROLLBACK")
		ownershipOwner(s, "own_tx_sequence", "own_source")
		ownershipSeed(s, "own_tx_table", "own_source")
		for _, tc := range []struct{ q, tag string }{
			{"ALTER TABLE own_tx_table OWNER TO own_target", "ALTER TABLE"},
			{"REASSIGN OWNED BY own_source TO own_target", "REASSIGN OWNED"},
			{"DROP OWNED BY own_source CASCADE", "DROP OWNED"},
		} {
			// The same held definition must reach execution-time read-only checks.
			held, err := s.C.PrepareContext(s.P.Ctx, tc.q)
			if err != nil {
				s.T.Fatal(err)
			}
			s.Exec("BEGIN READ ONLY")
			result, err := held.ExecContext(s.P.Ctx)
			if result != nil {
				s.T.Fatal("failed held utility published result", result)
			}
			fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
			ownershipAborted(s, tc.q)
			s.Exec("ROLLBACK")
			if err := held.Close(); err != nil {
				s.T.Fatal(err)
			}
			ownershipOwner(s, "own_tx_table", "own_source")
			s.Exec("BEGIN READ ONLY")
			ownershipFailure(s, tc.q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
			ownershipAborted(s, tc.q)
			s.Exec("ROLLBACK")
			ownershipOwner(s, "own_tx_table", "own_source")
			s.Exec("BEGIN")
			s.Exec(tc.q)
			s.Exec("ROLLBACK")
			ownershipOwner(s, "own_tx_table", "own_source")
			ownershipRows(s, "own_tx_table")
			s.Exec("BEGIN")
			s.Exec(tc.q)
			s.Exec("COMMIT")
			if tc.tag == "DROP OWNED" {
				ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_tx_table' OR relname='own_tx_sequence'", 0)
				ownershipBool(s, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='own_source')", true)
				ownershipSeed(s, "own_tx_table", "own_source")
				s.Exec("CREATE SEQUENCE own_tx_sequence")
				s.Exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_source")
			} else {
				ownershipOwner(s, "own_tx_table", "own_target")
				ownershipRows(s, "own_tx_table")
				if tc.tag == "REASSIGN OWNED" {
					ownershipOwner(s, "own_tx_sequence", "own_target")
					s.Exec("REASSIGN OWNED BY own_target TO own_source")
				} else {
					s.Exec("ALTER TABLE own_tx_table OWNER TO own_source")
				}
			}
		}
		s.Exec("BEGIN READ ONLY")
		ownershipFailure(s, "ALTER SEQUENCE own_absent OWNER TO own_absent", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER SEQUENCE in a read-only transaction"})
		s.Exec("ROLLBACK")
	})
}

func TestReferenceRoleOwnershipLifecycle(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipSeed(s, "own_lifecycle", "own_source")
		q := "ALTER TABLE own_lifecycle OWNER TO own_target"
		if !s.Backend.PG {
			ctx, cancel := context.WithCancel(s.P.Ctx)
			cancel()
			_, err := s.C.ExecContext(ctx, q)
			if !errors.Is(err, context.Canceled) {
				s.T.Fatal("native pre-cancel", err)
			}
			ownershipOwner(s, "own_lifecycle", "own_source")
		}
		reader := predefinedHeld(s, "SELECT id FROM own_lifecycle ORDER BY id")
		fixture.RunStep(s.T, s.P.Ctx, reader, s.Record, fixture.Select(reader.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		var retained *sql.Rows
		if !s.Backend.PG {
			var err error
			retained, err = s.Backend.DB.QueryContext(s.P.Ctx, reader.SQL)
			if err != nil {
				s.T.Fatal(err)
			}
			defer retained.Close()
		}
		holder, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer holder.Close()
		if _, err = holder.ExecContext(s.P.Ctx, "BEGIN"); err != nil {
			s.T.Fatal(err)
		}
		defer holder.ExecContext(context.Background(), "ROLLBACK")
		// Completed SELECT inside the open transaction is the acquired AccessShare
		// barrier; it remains held until we join the canceled owner operation.
		rows, err := holder.QueryContext(s.P.Ctx, "SELECT id FROM own_lifecycle")
		if err != nil {
			s.T.Fatal(err)
		}
		if !rows.Next() {
			s.T.Fatal(rows.Err())
		}
		var id int64
		if err = rows.Scan(&id); err != nil || id != 7 {
			s.T.Fatal(id, err)
		}
		rows.Close()
		worker, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer worker.Close()
		var stmt *sql.Stmt
		if s.Prepared {
			if stmt, err = worker.PrepareContext(s.P.Ctx, q); err != nil {
				s.T.Fatal(err)
			}
			defer stmt.Close()
		}
		var oid int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 'own_lifecycle'::regclass::oid::BIGINT").Scan(&oid); err != nil || oid <= 0 {
			s.T.Fatal(oid, err)
		}
		workerPID, holderPID := 0, 0
		if s.Backend.PG {
			workerPID = fixture.LockPID(s.T, s.P.Ctx, worker)
			holderPID = fixture.LockPID(s.T, s.P.Ctx, holder)
		}
		ctx, cancel := context.WithCancel(s.P.Ctx)
		defer cancel()
		finished := make(chan struct{})
		join := fixture.LockOperation(s.T, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			var out fixture.RelationOutcome
			if stmt != nil {
				out.Result, out.Err = stmt.ExecContext(ctx)
			} else {
				out.Result, out.Err = worker.ExecContext(ctx, q)
			}
			return out
		})
		// Cancel only after observing the owner change queued behind AccessShare.
		fixture.RelationLockPending(s.T, s.P, s.Backend, workerPID, holderPID, oid, "AccessExclusiveLock", finished)
		cancel()
		out := join()
		if out.Result != nil {
			s.T.Fatal("canceled ownership change returned result", out.Result)
		}
		if s.Backend.PG {
			d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(s.T, out.Err, &d)
		} else if !errors.Is(out.Err, context.Canceled) {
			s.T.Fatal("blocked ownership cancellation", out.Err)
		}
		if _, err = holder.ExecContext(s.P.Ctx, "ROLLBACK"); err != nil {
			s.T.Fatal(err)
		}
		ownershipOwner(s, "own_lifecycle", "own_source")
		ownershipRows(s, "own_lifecycle")
		s.Exec(q)
		ownershipOwner(s, "own_lifecycle", "own_target")
		s.Actor("own_source")
		denied := fixture.ErrorDiagnostic(reader.SQL, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table own_lifecycle"))
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, reader, s.Record, denied)
		s.Actor("own_target")
		fixture.RunStep(s.T, s.P.Ctx, reader, s.Record, fixture.Select(reader.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.Actor("")
		s.Exec("DROP OWNED BY own_target")
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_lifecycle'", 0)
		if retained != nil {
			metadata, err := retained.ColumnTypes()
			if err != nil {
				s.T.Fatal(err)
			}
			fixture.ScalarAssertDescription(s.T, metadata, []string{"id"}, []string{"INT4"})
			fixture.TransactionRowValues(s.T, retained, 7)
			if err := retained.Close(); err != nil {
				s.T.Fatal(err)
			}
		}
	})
}
