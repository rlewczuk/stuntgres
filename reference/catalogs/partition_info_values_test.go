package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionInfoTree(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				input string
				rows  [][]any
			}{
				{"pinfo_r", partitionInfoTreeRows(false)},
				{"pinfo_m", [][]any{{"pinfo_m", "pinfo_r", false, int64(0)}, {"pinfo_l", "pinfo_m", true, int64(1)}}},
				{"pinfo_l", [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}},
				{"pinfo_e", [][]any{{"pinfo_e", "pinfo_r", false, int64(0)}}},
			} {
				s := partitionInfoTree("SELECT * FROM pg_catalog.pg_partition_tree($1::regclass)", tc.rows, tc.input)
				s.Prepared = prepared
				partitionInfoRun(t, p, b, c, s)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT k,payload FROM pinfo_r ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(11), "eleven"}}))
	})
}

func TestReferencePartitionInfoAncestorsRoot(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		for _, tc := range []struct {
			name      string
			ancestors []string
		}{
			{"pinfo_l", []string{"pinfo_l", "pinfo_m", "pinfo_r"}}, {"pinfo_m", []string{"pinfo_m", "pinfo_r"}},
			{"pinfo_r", []string{"pinfo_r"}}, {"pinfo_e", []string{"pinfo_e", "pinfo_r"}},
		} {
			partitionInfoAncestors(t, p, b, c, "'"+tc.name+"'::regclass", tc.ancestors...)
			partitionInfoRoot(t, p, b, c, "'"+tc.name+"'::regclass", "pinfo_r")
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT pg_catalog.pg_partition_root($1::regclass)", []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{"pinfo_r"}}, tc.name)
				s.Prepared = prepared
				partitionInfoRun(t, p, b, c, s)
			}
		}
	})
}

func TestReferencePartitionInfoIndexes(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		partitionInfoRun(t, p, b, c, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_r_key')", partitionInfoTreeRows(true)))
		partitionInfoRun(t, p, b, c, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_m_k_payload_key')", [][]any{{"pinfo_m_k_payload_key", "pinfo_r_key", false, int64(0)}, {"pinfo_l_k_payload_key", "pinfo_m_k_payload_key", true, int64(1)}}))
		partitionInfoAncestors(t, p, b, c, "'pinfo_l_k_payload_key'", "pinfo_l_k_payload_key", "pinfo_m_k_payload_key", "pinfo_r_key")
		partitionInfoRoot(t, p, b, c, "'pinfo_l_k_payload_key'", "pinfo_r_key")
		partitionInfoExec(t, p, b, c,
			"CREATE TABLE pinfo_manual(k INT) PARTITION BY RANGE(k)",
			"CREATE TABLE pinfo_a PARTITION OF pinfo_manual FOR VALUES FROM(0) TO(5)",
			"CREATE TABLE pinfo_b PARTITION OF pinfo_manual FOR VALUES FROM(5) TO(10)",
			"CREATE UNIQUE INDEX pinfo_manual_idx ON ONLY pinfo_manual(k)",
			"CREATE UNIQUE INDEX pinfo_a_idx ON pinfo_a(k)",
			"CREATE UNIQUE INDEX pinfo_b_idx ON pinfo_b(k)")
		partitionInfoEmpty(t, p, b, c, "'pinfo_a_idx'")
		partitionInfoExec(t, p, b, c, "ALTER INDEX pinfo_manual_idx ATTACH PARTITION pinfo_a_idx")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid='pinfo_manual_idx'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{false}}))
		partitionInfoRun(t, p, b, c, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_manual_idx')", [][]any{{"pinfo_manual_idx", nil, false, int64(0)}, {"pinfo_a_idx", "pinfo_manual_idx", true, int64(1)}}))
		partitionInfoAncestors(t, p, b, c, "'pinfo_a_idx'", "pinfo_a_idx", "pinfo_manual_idx")
		partitionInfoRoot(t, p, b, c, "'pinfo_a_idx'", "pinfo_manual_idx")
		partitionInfoEmpty(t, p, b, c, "'pinfo_b_idx'")
		partitionInfoExec(t, p, b, c, "ALTER INDEX pinfo_manual_idx ATTACH PARTITION pinfo_b_idx")
		partitionInfoRoot(t, p, b, c, "'pinfo_b_idx'", "pinfo_manual_idx")
	})
}

func TestReferencePartitionInfoBoundaries(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoExec(t, p, b, c, "CREATE TABLE pinfo_plain(k INT)", "CREATE INDEX pinfo_plain_idx ON pinfo_plain(k)",
			"CREATE TABLE pinfo_inherited() INHERITS(pinfo_plain)", "CREATE VIEW pinfo_view AS SELECT k FROM pinfo_plain",
			"CREATE MATERIALIZED VIEW pinfo_mv AS SELECT k FROM pinfo_plain", "CREATE SEQUENCE pinfo_seq")
		for _, name := range []string{"pinfo_plain", "pinfo_plain_idx", "pinfo_inherited", "pinfo_view", "pinfo_mv", "pinfo_seq"} {
			partitionInfoEmpty(t, p, b, c, "'"+name+"'::regclass")
		}
		for _, input := range []string{"0::regclass", "NULL::regclass", "NULL"} {
			partitionInfoEmpty(t, p, b, c, input)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT oid FROM pg_class WHERE oid=4294967295::oid", []string{"oid"}, []string{"OID"}, nil))
		partitionInfoEmpty(t, p, b, c, "4294967295::regclass")
		for _, q := range []string{"SELECT * FROM pg_partition_tree($1::regclass)", "SELECT * FROM pg_partition_ancestors($1::regclass)"} {
			var s fixture.Step
			if q == "SELECT * FROM pg_partition_tree($1::regclass)" {
				s = partitionInfoTree(q, nil, nil)
			} else {
				s = fixture.Select(q, []string{"relid"}, []string{"REGCLASS"}, nil, nil)
			}
			s.Prepared = true
			partitionInfoRun(t, p, b, c, s)
		}
	})
}

func TestReferencePartitionInfoComposition(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"SELECT * FROM pg_partition_tree(rootrelid => 'pinfo_r'::regclass)", "SELECT * FROM pg_partition_tree(rootrelid := 'pinfo_r'::regclass)"} {
			partitionInfoRun(t, p, b, c, partitionInfoTree(q, partitionInfoTreeRows(false)))
		}
		named := fixture.Select("SELECT * FROM pg_partition_ancestors(partitionid => 'pinfo_l'::regclass)", []string{"relid"}, []string{"REGCLASS"}, [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}})
		named.Bag = true
		partitionInfoRun(t, p, b, c, named)
		// Explicit ordering here is SQL ordering, never an inferred traversal order.
		run(fixture.Select("SELECT a::text AS name FROM pg_partition_ancestors('pinfo_l') AS t(a) ORDER BY name", []string{"name"}, []string{"TEXT"}, [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}}))
		s := fixture.Select("SELECT pg_partition_ancestors('pinfo_l')", []string{"pg_partition_ancestors"}, []string{"REGCLASS"}, [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}})
		s.Bag = true
		partitionInfoRun(t, p, b, c, s)
		run(fixture.Select("SELECT t.id::text AS name,t.up::text AS parent,t.leaf,t.depth FROM pg_partition_tree('pinfo_l') AS t(id,up,leaf,depth)", []string{"name", "parent", "leaf", "depth"}, []string{"TEXT", "TEXT", "BOOL", "INT4"}, [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}))
		run(fixture.Select("SELECT t.child::text AS name,t.parentrelid::text AS parent,t.isleaf,t.level FROM pg_partition_tree('pinfo_l') AS t(child)", []string{"name", "parent", "isleaf", "level"}, []string{"TEXT", "TEXT", "BOOL", "INT4"}, [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}))
		run(fixture.Select("SELECT c.relname FROM pg_partition_tree('pinfo_r') t JOIN pg_class c ON c.oid=t.relid ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"pinfo_e"}, {"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}, {"pinfo_s"}}))
		run(fixture.Select("SELECT v.n,t.relid::text AS name FROM (VALUES('pinfo_l'::regclass),('pinfo_e'::regclass)) v(n) CROSS JOIN LATERAL pg_partition_tree(v.n) t ORDER BY name", []string{"n", "name"}, []string{"REGCLASS", "TEXT"}, [][]any{{"pinfo_e", "pinfo_e"}, {"pinfo_l", "pinfo_l"}}))
		run(fixture.Select("SELECT min(ordinality) AS first,max(ordinality) AS last,count(*) AS n FROM pg_partition_tree('pinfo_r') WITH ORDINALITY", []string{"first", "last", "n"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(5), int64(5)}}))
		run(fixture.Select("SELECT v.n,min(t.ordinality) AS first,max(t.ordinality) AS last,count(*) AS n FROM (VALUES('pinfo_r'::regclass),('pinfo_m'::regclass)) v(n) CROSS JOIN LATERAL pg_partition_tree(v.n) WITH ORDINALITY t GROUP BY v.n ORDER BY v.n::text", []string{"n", "first", "last", "n"}, []string{"REGCLASS", "INT8", "INT8", "INT8"}, [][]any{{"pinfo_m", int64(1), int64(2), int64(2)}, {"pinfo_r", int64(1), int64(5), int64(5)}}))
		partitionInfoExec(t, p, b, c, "CREATE TABLE pinfo_plain(k INT)")
		run(fixture.Select("SELECT t.relid,t.parentrelid,t.isleaf,t.level FROM (VALUES('pinfo_plain'::regclass)) v(n) LEFT JOIN LATERAL pg_partition_tree(v.n) t ON true", []string{"relid", "parentrelid", "isleaf", "level"}, []string{"REGCLASS", "REGCLASS", "BOOL", "INT4"}, [][]any{{nil, nil, nil, nil}}))
	})
}
