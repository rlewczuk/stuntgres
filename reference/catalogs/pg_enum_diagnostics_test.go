package reference_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGEnumDiagnosticsAndPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pge_diag_reader"})
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, message, pos string }{
			{"SELECT missing FROM pg_catalog.pg_enum", "42703", `column "missing" does not exist`, "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_enum", "42703", `column "missing" does not exist`, "14"},
			{"SELECT 'bad'::oid FROM pg_catalog.pg_enum WHERE FALSE", "22P02", `invalid input syntax for type oid: "bad"`, "8"},
			{"SELECT '4294967296'::oid FROM pg_catalog.pg_enum", "22003", `value "4294967296" is out of range for type oid`, "8"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, pgEnumProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgEnumProbe}
		for _, value := range []string{"bad", ""} {
			portal := "unnamed portal parameter $1 = '...'"
			if value == "" {
				portal = "unnamed portal parameter $1 = ''"
			}
			// Capture the full driver record via the established fixture before
			// its permitted native/PG portal-context normalization.
			s := fixture.ImplicitError(pgEnumProbe, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"" + value + "\"", Context: portal}, value)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		a, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		pgEnumExec(t, p, b, a, "CREATE SCHEMA pge_diag_private")
		defer a.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS pge_diag_private CASCADE")
		id := pgEnumCreate(t, p, b, a, "pge_diag_private.secret", "'hidden','visible'")
		original := pgEnumQuery(t, p, b, a, pgEnumProbe, id)
		if b.PG {
			// Native schema/type GRANT/REVOKE syntax is outside this part.
			// Its freshly created private schema already denies reader USAGE.
			pgEnumExec(t, p, b, a, "REVOKE ALL ON SCHEMA pge_diag_private FROM PUBLIC")
			pgEnumExec(t, p, b, a, "REVOKE ALL ON TYPE pge_diag_private.secret FROM PUBLIC")
		}
		pgEnumExec(t, p, b, a, "CREATE ROLE pge_diag_reader")
		pgEnumExec(t, p, b, reader, "SET ROLE pge_diag_reader")
		defer reader.ExecContext(p.Ctx, "RESET ROLE")
		// Prove missing schema access independently of successful catalog
		// discovery. PostgreSQL additionally reports the revoked type USAGE.
		for _, prepared := range []bool{false, true} {
			denied := fixture.ImplicitError("SELECT NULL::pge_diag_private.secret", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pge_diag_private", Position: "14"})
			denied.SelectRows, denied.Prepared, denied.ErrorStage = true, prepared, "execute"
			if prepared {
				denied.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, denied)
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT has_schema_privilege('pge_diag_private','USAGE') AS schema_usage,has_type_privilege($1::oid,'USAGE') AS type_usage", []string{"schema_usage", "type_usage"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}, id))
		}
		if got := pgEnumQuery(t, p, b, reader, pgEnumProbe, id); !reflect.DeepEqual(got, original) {
			t.Fatal("PUBLIC metadata discovery", got, original)
		}
		{
			// The registered source ACL is independent of enum/type visibility.
			pgEnumExec(t, p, b, a, "REVOKE SELECT ON pg_catalog.pg_enum FROM PUBLIC")
			defer a.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog.pg_enum TO PUBLIC")
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(pgEnumProbe, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_enum"}, id)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
			}
			pgEnumExec(t, p, b, a, "GRANT SELECT ON pg_catalog.pg_enum TO PUBLIC")
		}
		pgEnumExec(t, p, b, reader, "RESET ROLE")
		if !b.PG {
			for _, tc := range []struct{ q, stage string }{
				{"INSERT INTO pg_catalog.pg_enum VALUES(1,1,1,'x')", "execute"},
				{"UPDATE pg_catalog.pg_enum SET enumlabel='x'", "prepare"},
				{"DELETE FROM pg_catalog.pg_enum", "execute"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = tc.stage
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_enum WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(canceled, pgEnumProbe, id)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("query cancellation", err)
		}
		cancelStmt, err := b.DB.PrepareContext(canceled, pgEnumProbe)
		if cancelStmt != nil {
			cancelStmt.Close()
			t.Fatal("canceled prepare published handle")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("prepare cancellation", err)
		}
		rows, err = stmt.QueryContext(canceled, id)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled execution published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("execute cancellation", err)
		}
		if got := pgEnumQuery(t, p, b, a, pgEnumProbe, id); !reflect.DeepEqual(got, original) {
			t.Fatal("failed operations changed catalog", got, original)
		}
	})
}
