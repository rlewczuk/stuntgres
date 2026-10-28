package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInheritsInheritance(t *testing.T) {
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB,
			"CREATE TABLE pi_p(a INT)", "CREATE TABLE pi_q(a INT)",
			"CREATE TABLE pi_c() INHERITS(pi_q,pi_p)", "CREATE TABLE pi_leaf() INHERITS(pi_c)",
			"CREATE TABLE pi_like(LIKE pi_c)", "CREATE VIEW pi_view AS SELECT a FROM pi_c",
			"CREATE INDEX pi_local_idx ON pi_c(a)", "CREATE INDEX pi_standalone_idx ON pi_like(a)",
			"CREATE TYPE pi_record AS (a INT)", "CREATE TABLE pi_typed OF pi_record")
		parent, other := pgInheritsID(t, p, b.DB, "pi_p"), pgInheritsID(t, p, b.DB, "pi_q")
		child, leaf := pgInheritsID(t, p, b.DB, "pi_c"), pgInheritsID(t, p, b.DB, "pi_leaf")
		pgInheritsBag(t, p, b, b.DB, []string{"pi_p", "pi_q", "pi_c", "pi_leaf", "pi_like", "pi_view", "pi_local_idx", "pi_standalone_idx", "pi_typed"}, [][]any{pgInheritsEdge(child, other, 1), pgInheritsEdge(child, parent, 2), pgInheritsEdge(leaf, child, 1)})
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT c.relname AS child,p.relname AS parent,i.inhseqno FROM pg_catalog.pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_class p ON p.oid=i.inhparent WHERE c.oid=$1::oid ORDER BY i.inhseqno", []string{"child", "parent", "inhseqno"}, []string{"NAME", "NAME", "INT4"}, [][]any{{"pi_c", "pi_q", int64(1)}, {"pi_c", "pi_p", int64(2)}}, child)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			for _, id := range []int64{0, 4294967295} {
				s = fixture.Select(pgInheritsSelect+" WHERE inhrelid=$1::oid", fixture.PGInheritsColumns, pgInheritsTypes, nil, id)
				s.Prepared = prepared
				fixture.OidReferenceRows(t, p.Ctx, b, b.DB, s)
			}
		}
	})
}

func TestReferencePGInheritsPartitionsAndIndexes(t *testing.T) {
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB,
			"CREATE TABLE pi_root(k INT,payload TEXT,CONSTRAINT pi_root_key UNIQUE(k,payload)) PARTITION BY RANGE(k)",
			"CREATE TABLE pi_mid PARTITION OF pi_root FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(k)",
			"CREATE TABLE pi_low PARTITION OF pi_mid FOR VALUES FROM(0) TO(5)")
		root, mid, low := pgInheritsID(t, p, b.DB, "pi_root"), pgInheritsID(t, p, b.DB, "pi_mid"), pgInheritsID(t, p, b.DB, "pi_low")
		ri, mi, li := pgInheritsID(t, p, b.DB, "pi_root_key"), pgInheritsID(t, p, b.DB, "pi_mid_k_payload_key"), pgInheritsID(t, p, b.DB, "pi_low_k_payload_key")
		names := []string{"pi_root", "pi_mid", "pi_low", "pi_root_key", "pi_mid_k_payload_key", "pi_low_k_payload_key"}
		want := [][]any{pgInheritsEdge(mid, root, 1), pgInheritsEdge(low, mid, 1), pgInheritsEdge(mi, ri, 1), pgInheritsEdge(li, mi, 1)}
		pgInheritsBag(t, p, b, b.DB, names, want)
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pi_future PARTITION OF pi_mid FOR VALUES FROM(5) TO(10)")
		future, fi := pgInheritsID(t, p, b.DB, "pi_future"), pgInheritsID(t, p, b.DB, "pi_future_k_payload_key")
		names = append(names, "pi_future", "pi_future_k_payload_key")
		want = append(want, pgInheritsEdge(future, mid, 1), pgInheritsEdge(fi, mi, 1))
		pgInheritsBag(t, p, b, b.DB, names, want)
		// Detach severs immediate links only, preserving the nested subtree.
		nested := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer nested.ExecContext(p.Ctx, "ROLLBACK")
		fixture.PGClassExec(t, p, b, nested, "BEGIN", "SAVEPOINT pi_nested", "ALTER TABLE pi_root DETACH PARTITION pi_mid")
		pgInheritsBag(t, p, b, nested, names, [][]any{pgInheritsEdge(low, mid, 1), pgInheritsEdge(li, mi, 1), pgInheritsEdge(future, mid, 1), pgInheritsEdge(fi, mi, 1)})
		fixture.PGClassExec(t, p, b, nested, "ROLLBACK TO pi_nested", "COMMIT")
		pgInheritsBag(t, p, b, b.DB, names, want)

		pgInheritsManualTree(t, p, b)
		mr, ma, mb := pgInheritsID(t, p, b.DB, "pi_manual"), pgInheritsID(t, p, b.DB, "pi_a"), pgInheritsID(t, p, b.DB, "pi_b")
		mri, mai, mbi := pgInheritsID(t, p, b.DB, "pi_manual_idx"), pgInheritsID(t, p, b.DB, "pi_a_idx"), pgInheritsID(t, p, b.DB, "pi_b_idx")
		mn := []string{"pi_manual", "pi_a", "pi_b", "pi_manual_idx", "pi_a_idx", "pi_b_idx"}
		mw := [][]any{pgInheritsEdge(ma, mr, 1), pgInheritsEdge(mb, mr, 1)}
		pgInheritsBag(t, p, b, b.DB, mn, mw)
		fixture.PGClassExec(t, p, b, b.DB, "ALTER INDEX pi_manual_idx ATTACH PARTITION pi_a_idx")
		mw = append(mw, pgInheritsEdge(mai, mri, 1))
		pgInheritsBag(t, p, b, b.DB, mn, mw)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid='pi_manual_idx'::regclass::oid", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{false}}))
		fixture.PGClassExec(t, p, b, b.DB, "ALTER INDEX pi_manual_idx ATTACH PARTITION pi_a_idx")
		pgInheritsBag(t, p, b, b.DB, mn, mw)
		fixture.PGClassExec(t, p, b, b.DB, "ALTER INDEX pi_manual_idx ATTACH PARTITION pi_b_idx")
		mw = append(mw, pgInheritsEdge(mbi, mri, 1))
		pgInheritsBag(t, p, b, b.DB, mn, mw)
		fixture.PGClassExec(t, p, b, b.DB, "DROP INDEX pi_manual_idx")
		pgInheritsBag(t, p, b, b.DB, mn, mw[:2])
		fixture.PGClassExec(t, p, b, b.DB, "DROP TABLE pi_manual")
		pgInheritsBag(t, p, b, b.DB, mn, nil)
	})
}

func pgInheritsManualTree(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.PGClassExec(t, p, b, b.DB,
		"CREATE TABLE pi_manual(k INT,payload TEXT) PARTITION BY RANGE(k)",
		"CREATE TABLE pi_a PARTITION OF pi_manual FOR VALUES FROM(0) TO(5)",
		"CREATE TABLE pi_b PARTITION OF pi_manual FOR VALUES FROM(5) TO(10)",
		"CREATE UNIQUE INDEX pi_manual_idx ON ONLY pi_manual(k,payload)",
		"CREATE UNIQUE INDEX pi_a_idx ON pi_a(k,payload)",
		"CREATE UNIQUE INDEX pi_b_idx ON pi_b(k,payload)")
}
