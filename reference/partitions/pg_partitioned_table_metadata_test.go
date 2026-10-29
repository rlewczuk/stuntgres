package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPartitionedTableMetadata(t *testing.T) {
	pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := pptRun(t, p, b, b.DB)
		pptEmpty(run, b, "SELECT * FROM pg_catalog.pg_partitioned_table WHERE FALSE")
		pptEmpty(run, b, "SELECT * FROM pg_catalog.pg_partitioned_table WHERE partrelid=$1::oid", int64(4294967295))
		pptBoth(run, fixture.Select(`SELECT c.oid::bigint,c.reltype::bigint,t.typarray::bigint,n.nspname,c.relkind,c.relhasindex FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='pg_catalog.pg_partitioned_table'::regclass`,
			[]string{"oid", "reltype", "typarray", "nspname", "relkind", "relhasindex"}, []string{"INT8", "INT8", "INT8", "NAME", "CHAR", "BOOL"}, [][]any{{int64(3350), int64(10099), int64(10098), "pg_catalog", "r", true}}))
		typeIDs := []int64{26, 18, 21, 26, 22, 30, 30, 194}
		var attributes [][]any
		for i, name := range pptColumns {
			dims, coll := int64(0), int64(0)
			if i >= 4 && i <= 6 {
				dims = 1
			}
			if i == 7 {
				coll = 950
			}
			attributes = append(attributes, []any{int64(i + 1), name, typeIDs[i], int64(-1), i < 7, dims, coll, false})
		}
		pptBoth(run, fixture.Select(`SELECT attnum,attname,atttypid::bigint,atttypmod,attnotnull,attndims,attcollation::bigint,attisdropped FROM pg_attribute WHERE attrelid=3350 AND attnum>0 ORDER BY attnum`,
			[]string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attndims", "attcollation", "attisdropped"}, []string{"INT2", "NAME", "INT8", "INT4", "BOOL", "INT2", "INT8", "BOOL"}, attributes))
		for _, q := range []string{
			"CREATE TABLE ppt_plain(a INT)", "CREATE TABLE ppt_inherit(a INT)", "CREATE TABLE ppt_inherited() INHERITS(ppt_inherit)",
			"CREATE INDEX ppt_index ON ppt_plain(a)", "CREATE VIEW ppt_view AS SELECT a FROM ppt_plain", "CREATE SEQUENCE ppt_sequence",
			"CREATE TABLE ppt_parent(a INT) PARTITION BY RANGE(a)",
			"CREATE TABLE ppt_leaf PARTITION OF ppt_parent FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE ppt_mid PARTITION OF ppt_parent FOR VALUES FROM(10) TO(20) PARTITION BY LIST(a)",
		} {
			run(fixture.Exec(q, 0))
		}
		pptBoth(run, fixture.Select(`SELECT c.relname FROM pg_partitioned_table p JOIN pg_class c ON c.oid=p.partrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname IN ('ppt_plain','ppt_inherit','ppt_inherited','ppt_index','ppt_view','ppt_sequence','ppt_parent','ppt_leaf','ppt_mid') ORDER BY c.relname`, []string{"relname"}, []string{"NAME"}, [][]any{{"ppt_mid"}, {"ppt_parent"}}))
		id := pptID(t, p, b.DB, "ppt_parent")
		identity, zero := any(id), any(int64(0))
		if b.PG {
			identity, zero = strconv.FormatInt(id, 10), "0"
		}
		s := fixture.Select("SELECT * FROM pg_catalog.pg_partitioned_table WHERE partrelid=$1::oid", pptColumns, pptTypes, [][]any{{identity, "r", int64(1), zero, "1", "1978", "0", nil}}, id)
		s.CheckMetadata = pptMetadata(b.PG)
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if column >= 4 && column <= 6 {
				if b.PG {
					if _, ok := value.([]byte); !ok {
						t.Fatalf("pq vector %d: %T", column, value)
					}
				} else if _, ok := value.(string); !ok {
					t.Fatalf("native vector %d: %T", column, value)
				}
			}
			return true
		}
		// Metadata is checked before iteration for populated direct/prepared rows too.
		pptBoth(run, s)
	})
}
