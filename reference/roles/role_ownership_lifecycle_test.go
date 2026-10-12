package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

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
	ownershipCases(t, func(s *membershipScenario) {
		ownershipSeed(s, "own_fresh", "own_source")
		for _, tc := range ownershipFreshFamilies {
			if tc.create != "" {
				ownershipDefinition(s, tc.create)
				s.exec(tc.alter + " OWNER TO own_source")
			}
		}
		ownershipFailure(s, "ALTER TABLE own_missing OWNER TO own_absent", fixture.Diagnostic{Code: "42P01", Message: `relation "own_missing" does not exist`})
		ownershipFailure(s, "ALTER DOMAIN own_fresh_enum OWNER TO own_target", fixture.Diagnostic{Code: "42809", Message: "own_fresh_enum is not a domain"})
		ownershipFailure(s, "ALTER TYPE own_fresh OWNER TO own_target", fixture.Diagnostic{Code: "42809", Message: "own_fresh is a table's row type", Hint: "Use ALTER TABLE instead."})
		ownershipFailure(s, "ALTER TYPE _own_fresh_enum OWNER TO own_target", fixture.Diagnostic{Code: "42809", Message: "cannot alter array type own_fresh_enum[]", Hint: "You can alter type own_fresh_enum, which will alter the array type as well."})
		q := "ALTER TABLE own_late OWNER TO own_late_role"
		stmt, err := s.c.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal("effect-free prepare before names exist", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: q}
		missingObject := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P01", `relation "own_late" does not exist`))
		missingObject.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, missingObject)
		s.exec("CREATE TABLE own_late(id INT)")
		want := fixture.ControlsReferenceDiagnostic("42704", `role "own_late_role" does not exist`)
		step := fixture.ErrorDiagnostic(q, want)
		step.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
		s.exec("CREATE ROLE own_late_role")
		defer func() { s.actor(""); s.exec("DROP OWNED BY own_late_role CASCADE"); s.exec("DROP ROLE own_late_role") }()
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Exec(q, 0))
		ownershipOwner(s, "own_late", "own_late_role")
		s.exec("REASSIGN OWNED BY own_late_role TO own_source")
		s.exec("DROP ROLE own_late_role")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
		s.exec("CREATE ROLE own_late_role")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Exec(q, 0))
		ownershipOwner(s, "own_late", "own_late_role")
		s.exec("DROP TABLE own_late")
		s.exec("CREATE TABLE own_late(id INT)")
		s.run(fixture.Exec("INSERT INTO own_late VALUES(7)", 1))
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Exec(q, 0))
		ownershipRows(s, "own_late")
		selectorQ := "ALTER TABLE own_fresh OWNER TO CURRENT_USER"
		selector := predefinedHeld(s, selectorQ)
		s.actor("own_source")
		fixture.RunStep(s.t, s.p.Ctx, selector, s.record, fixture.Exec(selectorQ, 0))
		s.actor("")
		fixture.RunStep(s.t, s.p.Ctx, selector, s.record, fixture.Exec(selectorQ, 0))
		ownershipOwner(s, "own_fresh", s.bootstrap)
		for _, q := range []string{"ALTER TABLE own_fresh OWNER TO own_target trailing", "REASSIGN OWNED BY own_source TO own_target trailing", "DROP OWNED BY own_source CASCADE trailing"} {
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "trailing"`)
			d.Position = strconv.Itoa(strings.Index(q, "trailing") + 1)
			step := fixture.ErrorDiagnostic(q, d)
			step.Prepared = s.prepared
			if s.prepared {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, step)
			ownershipOwner(s, "own_fresh", s.bootstrap)
			ownershipRows(s, "own_fresh")
		}
		s.exec("ALTER TABLE own_fresh OWNER TO own_source")
		s.actor("own_source")
		graphQ := "ALTER TABLE own_fresh OWNER TO own_target"
		graphHeld := predefinedHeld(s, graphQ)
		s.actor("")
		s.exec("GRANT own_target TO own_source WITH INHERIT FALSE, SET FALSE")
		s.actor("own_source")
		denied := fixture.ErrorDiagnostic(graphQ, fixture.ControlsReferenceDiagnostic("42501", `must be able to SET ROLE "own_target"`))
		denied.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, graphHeld, s.record, denied)
		s.actor("")
		s.exec("GRANT own_target TO own_source WITH SET TRUE")
		s.actor("own_source")
		fixture.RunStep(s.t, s.p.Ctx, graphHeld, s.record, fixture.Exec(graphQ, 0))
		s.actor("")
		ownershipOwner(s, "own_fresh", "own_target")
	})
}

func TestReferenceRoleOwnershipTransactions(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		s.exec("CREATE SEQUENCE own_tx_sequence")
		peer, err := s.backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		peerOwner := func(owner string) {
			fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname='own_tx_sequence'", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}}))
		}
		s.exec("BEGIN")
		s.exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_source")
		ownershipOwner(s, "own_tx_sequence", "own_source")
		peerOwner(s.bootstrap)
		s.exec("SAVEPOINT owner_save")
		s.exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_target")
		ownershipOwner(s, "own_tx_sequence", "own_target")
		s.exec("ROLLBACK TO owner_save")
		ownershipOwner(s, "own_tx_sequence", "own_source")
		s.exec("COMMIT")
		peerOwner("own_source")
		s.exec("BEGIN")
		s.exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_target")
		s.exec("ROLLBACK")
		ownershipOwner(s, "own_tx_sequence", "own_source")
		ownershipSeed(s, "own_tx_table", "own_source")
		for _, tc := range []struct{ q, tag string }{
			{"ALTER TABLE own_tx_table OWNER TO own_target", "ALTER TABLE"},
			{"REASSIGN OWNED BY own_source TO own_target", "REASSIGN OWNED"},
			{"DROP OWNED BY own_source CASCADE", "DROP OWNED"},
		} {
			// Preparing outside the block preserves the separate execution-time
			// read-only refusal even where explicit-transaction Prepare is excluded.
			held, err := s.c.PrepareContext(s.p.Ctx, tc.q)
			if err != nil {
				s.t.Fatal(err)
			}
			s.exec("BEGIN READ ONLY")
			result, err := held.ExecContext(s.p.Ctx)
			if result != nil {
				s.t.Fatal("failed held utility published result", result)
			}
			fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
			ownershipAborted(s, tc.q)
			s.exec("ROLLBACK")
			if err := held.Close(); err != nil {
				s.t.Fatal(err)
			}
			ownershipOwner(s, "own_tx_table", "own_source")
			s.exec("BEGIN READ ONLY")
			if !s.backend.PG && s.prepared {
				predefinedFailure(s, tc.q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}, true, "prepare")
			} else {
				ownershipFailure(s, tc.q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
			}
			ownershipAborted(s, tc.q)
			s.exec("ROLLBACK")
			ownershipOwner(s, "own_tx_table", "own_source")
			s.exec("BEGIN")
			if s.backend.PG {
				s.exec(tc.q)
			} else {
				stage := "execute"
				if s.prepared {
					stage = "prepare"
				}
				predefinedFailure(s, tc.q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}, s.prepared, stage)
				ownershipAborted(s, tc.q)
			}
			s.exec("ROLLBACK")
			ownershipOwner(s, "own_tx_table", "own_source")
			ownershipRows(s, "own_tx_table")
			if s.backend.PG {
				s.exec("BEGIN")
				s.exec(tc.q)
				s.exec("COMMIT")
				if tc.tag == "DROP OWNED" {
					ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_tx_table' OR relname='own_tx_sequence'", 0)
					ownershipBool(s, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='own_source')", true)
					ownershipSeed(s, "own_tx_table", "own_source")
					s.exec("CREATE SEQUENCE own_tx_sequence")
					s.exec("ALTER SEQUENCE own_tx_sequence OWNER TO own_source")
				} else {
					ownershipOwner(s, "own_tx_table", "own_target")
					ownershipRows(s, "own_tx_table")
					if tc.tag == "REASSIGN OWNED" {
						ownershipOwner(s, "own_tx_sequence", "own_target")
						s.exec("REASSIGN OWNED BY own_target TO own_source")
					} else {
						s.exec("ALTER TABLE own_tx_table OWNER TO own_source")
					}
				}
			}
		}
		s.exec("BEGIN READ ONLY")
		ownershipFailure(s, "ALTER SEQUENCE own_absent OWNER TO own_absent", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER SEQUENCE in a read-only transaction"})
		s.exec("ROLLBACK")
	})
}

func TestReferenceRoleOwnershipLifecycle(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipSeed(s, "own_lifecycle", "own_source")
		q := "ALTER TABLE own_lifecycle OWNER TO own_target"
		if !s.backend.PG {
			ctx, cancel := context.WithCancel(s.p.Ctx)
			cancel()
			_, err := s.c.ExecContext(ctx, q)
			if !errors.Is(err, context.Canceled) {
				s.t.Fatal("native pre-cancel", err)
			}
			ownershipOwner(s, "own_lifecycle", "own_source")
		}
		reader := predefinedHeld(s, "SELECT id FROM own_lifecycle ORDER BY id")
		fixture.RunStep(s.t, s.p.Ctx, reader, s.record, fixture.Select(reader.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		var retained *sql.Rows
		if !s.backend.PG {
			var err error
			retained, err = s.backend.DB.QueryContext(s.p.Ctx, reader.SQL)
			if err != nil {
				s.t.Fatal(err)
			}
			defer retained.Close()
		}
		holder, err := s.backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer holder.Close()
		if _, err = holder.ExecContext(s.p.Ctx, "BEGIN"); err != nil {
			s.t.Fatal(err)
		}
		defer holder.ExecContext(context.Background(), "ROLLBACK")
		// Completed SELECT inside the open transaction is the acquired AccessShare
		// barrier; it remains held until we join the canceled owner operation.
		rows, err := holder.QueryContext(s.p.Ctx, "SELECT id FROM own_lifecycle")
		if err != nil {
			s.t.Fatal(err)
		}
		if !rows.Next() {
			s.t.Fatal(rows.Err())
		}
		var id int64
		if err = rows.Scan(&id); err != nil || id != 7 {
			s.t.Fatal(id, err)
		}
		rows.Close()
		ctx, cancel := context.WithTimeout(s.p.Ctx, 2*time.Second)
		defer cancel()
		worker, err := s.backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer worker.Close()
		execute := func() error { _, err := worker.ExecContext(ctx, q); return err }
		if s.prepared {
			stmt, err := worker.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			defer stmt.Close()
			execute = func() error { _, err := stmt.ExecContext(ctx); return err }
		}
		done := make(chan error, 1)
		go func() { done <- execute() }()
		select {
		case err := <-done:
			if s.backend.PG {
				d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(s.t, err, &d)
			} else if !errors.Is(err, context.DeadlineExceeded) {
				s.t.Fatal("blocked ownership cancellation", err)
			}
		case <-time.After(5 * time.Second):
			cancel()
			if _, err := holder.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				s.t.Error(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				s.t.Fatal("ownership worker did not join")
			}
			s.t.Fatal("ownership cancellation did not complete")
		}
		if _, err = holder.ExecContext(s.p.Ctx, "ROLLBACK"); err != nil {
			s.t.Fatal(err)
		}
		ownershipOwner(s, "own_lifecycle", "own_source")
		ownershipRows(s, "own_lifecycle")
		s.exec(q)
		ownershipOwner(s, "own_lifecycle", "own_target")
		s.actor("own_source")
		denied := fixture.ErrorDiagnostic(reader.SQL, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table own_lifecycle"))
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, reader, s.record, denied)
		s.actor("own_target")
		fixture.RunStep(s.t, s.p.Ctx, reader, s.record, fixture.Select(reader.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.actor("")
		s.exec("DROP OWNED BY own_target")
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_lifecycle'", 0)
		if retained != nil {
			metadata, err := retained.ColumnTypes()
			if err != nil {
				s.t.Fatal(err)
			}
			fixture.ScalarAssertDescription(s.t, metadata, []string{"id"}, []string{"INT4"})
			fixture.TransactionRowValues(s.t, retained, 7)
			if err := retained.Close(); err != nil {
				s.t.Fatal(err)
			}
		}
	})
}
