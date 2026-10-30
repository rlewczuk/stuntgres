package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 clones parent reltablespace=0 as an unspecified destination. A
// synthesized index calls DefineIndex with check_rights=false: it still rejects
// pg_global, but an ordinary actor must not get the CREATE ACL error instead.
// Reusing an equivalent index attaches an existing descriptor and creates none.
func TestReferenceDefaultTablespacePartitionIndexAttachment(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		actor := "superuser"
		if ordinary { actor = "ordinary" }
		t.Run(actor, func(t *testing.T) {
			for _, reuse := range []bool{false, true} {
				name := "synthesize"
				if reuse { name = "reuse" }
				t.Run(name, func(t *testing.T) {
					physicalCases(t, []string{"ts_attach_login"}, func(s *physicalCase) {
						owner := s
						if ordinary {
							s.run(fixture.Exec("CREATE ROLE ts_attach_login LOGIN PASSWORD 'role-core-fixture'", 0))
							s.run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ts_attach_login", 0))
							owner = s.login("ts_attach_login")
						}
						for _, q := range []string{
							"CREATE TABLE ts_attach_parent(v INT) PARTITION BY RANGE(v)",
							"CREATE UNIQUE INDEX ts_attach_parent_idx ON ts_attach_parent(v)",
							"CREATE TABLE ts_attach_child(v INT)",
						} { owner.run(fixture.Exec(q, 0)) }
						owner.run(fixture.Exec("INSERT INTO ts_attach_child VALUES(73)", 1))
						if reuse { owner.run(fixture.Exec("CREATE UNIQUE INDEX ts_attach_existing ON ts_attach_child(v)", 0)) }
						// Capture local identities without pinning PostgreSQL's allocator.
						// Subsequent fixture steps assert relationships, not backend OID parity.
						var parentIndex, existingIndex int64
						if err := owner.conn.QueryRowContext(s.p.Ctx, "SELECT indexrelid::bigint FROM pg_index WHERE indexrelid='ts_attach_parent_idx'::regclass").Scan(&parentIndex); err != nil || parentIndex <= 0 {
							s.t.Fatal("parent index identity", parentIndex, err)
						}
						if reuse {
							if err := owner.conn.QueryRowContext(s.p.Ctx, "SELECT indexrelid::bigint FROM pg_index WHERE indexrelid='ts_attach_existing'::regclass").Scan(&existingIndex); err != nil || existingIndex <= 0 || existingIndex == parentIndex {
								s.t.Fatal("existing child index identity", existingIndex, parentIndex, err)
							}
						}
						parentState := func() {
							owner.run(fixture.Select("SELECT indexrelid=$1::oid AS same_index,indrelid='ts_attach_parent'::regclass AS same_table,indisunique,indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='ts_attach_parent_idx'::regclass", []string{"same_index", "same_table", "indisunique", "indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}}, parentIndex))
						}
						parentState()
						owner.run(fixture.Select("SELECT count(*) FROM pg_inherits WHERE inhparent=$1::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, parentIndex))
						owner.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
						const query = "ALTER TABLE ts_attach_parent ATTACH PARTITION ts_attach_child DEFAULT"
						var attached int64
						if reuse {
							owner.run(fixture.Exec(query, 0))
							attached = 1
						} else {
							owner.run(physicalError(query, defaultTablespaceGlobal(), false, false))
							owner.tablespaceAbsent("pg_class", "relname", "ts_attach_child_v_idx")
						}
						owner.run(fixture.Select("SELECT count(*) FROM pg_inherits WHERE inhrelid='ts_attach_child'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{attached}}))
						parentState()
						// Both topology and inventory are exact: failed synthesis leaves
						// no index; reuse retains exactly the one captured child identity.
						owner.run(fixture.Select("SELECT count(*) FROM pg_index WHERE indrelid='ts_attach_child'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{attached}}))
						owner.run(fixture.Select("SELECT count(*) FROM pg_inherits WHERE inhparent=$1::oid", []string{"count"}, []string{"INT8"}, [][]any{{attached}}, parentIndex))
						owner.run(fixture.Select("SELECT count(*) FROM pg_inherits h JOIN pg_index i ON i.indexrelid=h.inhrelid WHERE i.indrelid='ts_attach_child'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{attached}}))
						if reuse {
							owner.run(fixture.Select("SELECT indexrelid=$1::oid AS same_index,indisunique,indisvalid,indisready,indislive FROM pg_index WHERE indrelid='ts_attach_child'::regclass", []string{"same_index", "indisunique", "indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}}, existingIndex))
							owner.run(fixture.Select("SELECT inhparent=$1::oid AS same_parent,inhseqno FROM pg_inherits WHERE inhrelid=$2::oid", []string{"same_parent", "inhseqno"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}, parentIndex, existingIndex))
						}
						owner.run(fixture.Select("SELECT v FROM ts_attach_child", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
						if ordinary { s.run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM ts_attach_login", 0)) }
					})
				})
			}
		})
	}
}
