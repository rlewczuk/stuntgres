package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryViewControls(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE control_public(v INT)", nil)
		s.Exec(s.C, "INSERT INTO control_public VALUES(7)", 1)
		s.Utility("CREATE VIEW control_public_v AS SELECT v FROM control_public", nil)
		s.Utility("CREATE TEMP TABLE control_base(v INT,txt TEXT)", nil)
		s.Exec(s.C, "INSERT INTO control_base VALUES(11,'a')", 1)
		s.Utility("CREATE VIEW control_v AS SELECT v FROM control_base WHERE txt='a'", nil)
		s.Utility("CREATE VIEW control_w AS SELECT v FROM control_v WHERE v>0", nil)
		s.Exec(s.Peer, "CREATE TEMP VIEW control_peer AS SELECT 22 AS v", 0)
		temporaryViewNotices(t, n, "control_v", "control_w")
		fixture.TemporaryTableRows(s, s.C, "control_w", 11)
		if s.Postgres {
			return
		} // Native APIs are intentionally not fictitious PostgreSQL SQL.
		dump := fixture.ViewDump(t, s.P)
		for _, name := range []string{"control_base", "control_w", "control_peer"} {
			if strings.Contains(string(dump), name) {
				t.Fatalf("TEMP artifact serialized: %s", name)
			}
		}
		fresh := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(s.P.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, s.P.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Select("SELECT v FROM control_public_v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.RunStep(t, s.P.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("INSERT INTO control_public VALUES(8)", 1))
		fixture.RunStep(t, s.P.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Select("SELECT v FROM control_public_v ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
		var oid, rule int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='control_w'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
			t.Fatal(err)
		}
		held, err := s.C.PrepareContext(s.P.Ctx, "SELECT v FROM control_w ORDER BY v")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		rows, err := s.C.QueryContext(s.P.Ctx, "SELECT v FROM control_w")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		var afterOID, afterRule int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='control_w'").Scan(&afterOID); err != nil {
			t.Fatal(err)
		}
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class=$1", afterOID).Scan(&afterRule); err != nil {
			t.Fatal(err)
		}
		if afterOID != oid || afterRule != rule {
			t.Fatal("retained identities changed", oid, afterOID, rule, afterRule)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var v int64
		if err := rows.Scan(&v); err != nil || v != 11 {
			t.Fatal(v, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
		result, err := held.QueryContext(s.P.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Next() {
			t.Fatal(result.Err())
		}
		if err := result.Scan(&v); err != nil || v != 11 {
			t.Fatal(v, err)
		}
		if result.Next() || result.Err() != nil {
			t.Fatal(result.Err())
		}
		result.Close()
		fixture.TemporaryTableRows(s, s.C, "control_w", 11)
		fixture.TemporaryTableRows(s, s.Peer, "control_peer", 22)
		s.Exec(s.C, "INSERT INTO control_base VALUES(12,'a'),(99,'b')", 2)
		fixture.TemporaryTableRows(s, s.C, "control_w", 11, 12)
		s.Utility("CREATE TABLE control_after(v INT)", nil)
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		fixture.TemporaryTableRows(s, s.C, "control_w", 11, 12)
	})
}

func TestReferenceTemporaryViewControlBoundaries(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE boundary_public(v INT)", nil)
		s.Exec(s.C, "INSERT INTO boundary_public VALUES(7)", 1)
		s.Utility("CREATE TEMP VIEW boundary_persistent_provider AS SELECT v FROM boundary_public", nil)
		temporaryViewNotices(t, n)
		fixture.TemporaryTableRows(s, s.C, "boundary_persistent_provider", 7)
		if s.Postgres {
			return
		}
		dump := fixture.ViewDump(t, s.P)
		for _, replace := range []func() error{func() error { return stuntgres.Reset(s.P.Ctx, s.P.Native) }, func() error { return stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)) }} {
			if err := replace(); err == nil {
				t.Fatal("invalidated persistent provider silently retained/rebound")
			}
			fixture.TemporaryTableRows(s, s.C, "boundary_persistent_provider", 7)
			fixture.TemporaryTableRows(s, s.C, "boundary_public", 7)
		}
		s.Utility("DROP VIEW boundary_persistent_provider", nil)
		s.Utility("CREATE TEMP VIEW boundary_constant AS SELECT 11 AS v", nil)
		for _, script := range []string{"CREATE TEMP VIEW illegal AS SELECT 1 AS v;", "CREATE VIEW pg_temp.illegal AS SELECT 1 AS v;"} {
			err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewBufferString(script))
			fixture.AssertDiagnostic(t, err, fixture.TempReferenceDiagnostic("0A000", "restore cannot stage non-retractable effects", "", ""))
			fixture.TemporaryTableRows(s, s.C, "boundary_public", 7)
			fixture.TemporaryTableRows(s, s.C, "boundary_constant", 11)
		}
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewBufferString("CREATE TABLE leaked(v INT); CREATE VIEW broken AS SELECT missing FROM leaked;")); err == nil {
			t.Fatal("invalid suffix accepted")
		}
		ctx, cancel := context.WithCancel(s.P.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, s.P.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		reading, stopReading := context.WithCancel(s.P.Ctx)
		if err := stuntgres.Restore(reading, s.P.Native, &fixture.CatalogControlCancelReader{Cancel: stopReading}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stopReading()
		fixture.TemporaryTableRows(s, s.C, "boundary_public", 7)
		fixture.TemporaryTableRows(s, s.C, "boundary_constant", 11)
		temporaryViewSelect(s, s.C, "SELECT relname FROM pg_catalog.pg_class WHERE relname='illegal' OR relname='leaked' OR relname='replacement'", []string{"relname"}, []string{"NAME"}, [][]any{})
	})
}

func TestReferenceTemporaryViewCatalogRetention(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TEMP VIEW catalog_retention_v AS SELECT 11 AS v FROM pg_catalog.pg_views WHERE viewname='catalog_retention_v'", nil)
		temporaryViewNotices(t, n)
		fixture.TemporaryTableRows(s, s.C, "catalog_retention_v", 11)
		if s.Postgres {
			return
		}
		var oid int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='catalog_retention_v'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
			t.Fatal(err)
		}
		fixture.TemporaryTableRows(s, s.C, "catalog_retention_v", 11)
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewBufferString("CREATE TABLE catalog_retention_public(v INT); INSERT INTO catalog_retention_public VALUES(7);")); err != nil {
			t.Fatal(err)
		}
		fixture.TemporaryTableRows(s, s.C, "catalog_retention_v", 11)
		var after int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='catalog_retention_v'").Scan(&after); err != nil || after != oid {
			t.Fatal(oid, after, err)
		}
		s.Utility("ALTER VIEW catalog_retention_v SET(security_barrier=true)", nil)
		fixture.TemporaryTableRows(s, s.C, "catalog_retention_v", 11)
	})
}
