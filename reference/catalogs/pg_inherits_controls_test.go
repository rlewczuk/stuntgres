package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInheritsVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pi_reader"})
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pi_visible_p(a INT)", "CREATE TABLE pi_visible_c() INHERITS(pi_visible_p)")
		parent, child := pgInheritsID(t, p, b.DB, "pi_visible_p"), pgInheritsID(t, p, b.DB, "pi_visible_c")
		owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		held := pgInheritsHeld(t, p, owner)
		pgClassExec(t, p, b, owner, "CREATE ROLE pi_reader", "GRANT USAGE ON SCHEMA public TO pi_reader", "SET ROLE pi_reader")
		defer owner.ExecContext(p.Ctx, "RESET ROLE")
		pgInheritsHeldRows(t, p, b, held, child, [][]any{pgInheritsEdge(child, parent, 1)})
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT a FROM pi_visible_c", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pi_visible_c"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
		}
		pgClassExec(t, p, b, owner, "RESET ROLE", "REVOKE USAGE ON SCHEMA public FROM pi_reader", "DROP ROLE pi_reader", "CREATE TEMP TABLE pi_temp_p(a INT)", "CREATE TEMP TABLE pi_temp_c() INHERITS(pi_temp_p)")
		tp, tc := pgInheritsID(t, p, owner, "pi_temp_p"), pgInheritsID(t, p, owner, "pi_temp_c")
		ph := pgInheritsHeld(t, p, peer)
		pgInheritsHeldRows(t, p, b, ph, tc, [][]any{pgInheritsEdge(tc, tp, 1)})
		s := fixture.ImplicitError("SELECT 'pi_temp_c'::regclass::oid", fixture.Diagnostic{Code: "42P01", Message: `relation "pi_temp_c" does not exist`, Position: "8"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
		pgClassExec(t, p, b, owner, "DROP TABLE pi_temp_p CASCADE", "CREATE TABLE public.pg_inherits(marker INT)", "SET search_path TO public,pg_catalog")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select("SELECT marker FROM pg_inherits", []string{"marker"}, []string{"INT4"}, nil))
		pgInheritsHeldRows(t, p, b, held, child, [][]any{pgInheritsEdge(child, parent, 1)})
		pgInheritsBag(t, p, b, owner, []string{"pi_visible_p", "pi_visible_c"}, [][]any{pgInheritsEdge(child, parent, 1)})
	})
}

func TestReferencePGInheritsControls(t *testing.T) {
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		setup := []string{"CREATE TABLE pi_control_p(a INT)", "CREATE TABLE pi_control_c() INHERITS(pi_control_p)", "CREATE TABLE pi_control_root(k INT,payload TEXT,CONSTRAINT pi_control_key UNIQUE(k,payload)) PARTITION BY RANGE(k)", "CREATE TABLE pi_control_low PARTITION OF pi_control_root FOR VALUES FROM(0) TO(5)"}
		pgClassExec(t, p, b, b.DB, setup...)
		names := []string{"pi_control_p", "pi_control_c", "pi_control_root", "pi_control_low", "pi_control_key", "pi_control_low_k_payload_key"}
		identities := func() []int64 {
			ids := make([]int64, len(names))
			for i, name := range names {
				ids[i] = pgInheritsID(t, p, b.DB, name)
			}
			return ids
		}
		edges := func(ids []int64) [][]any {
			return [][]any{pgInheritsEdge(ids[1], ids[0], 1), pgInheritsEdge(ids[3], ids[2], 1), pgInheritsEdge(ids[5], ids[4], 1)}
		}
		before := identities()
		pgInheritsBag(t, p, b, b.DB, names, edges(before))
		if b.PG {
			pgClassExec(t, p, b, b.DB, "DROP TABLE pi_control_p CASCADE", "DROP TABLE pi_control_root")
			pgInheritsBag(t, p, b, b.DB, names, nil)
			pgClassExec(t, p, b, b.DB, setup...)
			pgInheritsBag(t, p, b, b.DB, names, edges(identities()))
			return
		}
		retained, err := b.DB.QueryContext(p.Ctx, pgInheritsSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		pgInheritsMetadata(t, retained, false)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "INSERT INTO pg_inherits") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_inherits") {
			t.Fatal("catalog rows in logical dump", dump.String())
		}
		if !strings.Contains(dump.String(), "INHERITS") || !strings.Contains(dump.String(), "PARTITION") {
			t.Fatal("missing hierarchy DDL", dump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.OidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select(pgInheritsSelect, pgInheritsColumns, pgInheritsTypes, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		after := identities()
		pgInheritsBag(t, p, b, b.DB, names, edges(after))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE pi_failed(a INT); INSERT INTO pi_failed VALUES('bad');")); err == nil {
			t.Fatal("invalid restore succeeded")
		}
		for i, id := range identities() {
			if id != after[i] {
				t.Fatal("failed restore changed identity", names[i])
			}
		}
		pgInheritsBag(t, p, b, b.DB, names, edges(after))
		var got [][]any
		for retained.Next() {
			var child, parent, ordinal, pending any
			if err := retained.Scan(&child, &parent, &ordinal, &pending); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{fixture.OidReferenceValue(t, false, "OID", child), fixture.OidReferenceValue(t, false, "OID", parent), fixture.OidReferenceValue(t, false, "INT4", ordinal), fixture.OidReferenceValue(t, false, "BOOL", pending)})
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		if _, ok := fixture.CompareRows(got, edges(before), true); !ok {
			t.Fatal("retained edges", got, edges(before))
		}
	})
}
