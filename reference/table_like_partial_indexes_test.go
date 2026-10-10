package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceTableLikePartialIndexes(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(id INT,k INT,active BOOL,gap INT,n INT)", 0))
		run(referenceExec("ALTER TABLE src DROP COLUMN gap", 0))
		run(referenceExec("INSERT INTO src VALUES(99,99,TRUE,2)", 1))
		run(referenceExec("CREATE INDEX source_partial ON src(k) INCLUDE(id) WHERE active", 0))
		run(referenceExec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)", 0))
		run(referenceSelect("SELECT * FROM clone", []string{"lead", "id", "k", "active", "n"}, []string{"TEXT", "INT4", "INT4", "BOOL", "INT4"}, nil))
		referenceTableDDLMetadata(t, p.ctx, c, b.pg, prepared, "SELECT * FROM clone", []string{"lead", "id", "k", "active", "n"}, []string{"TEXT", "INT4", "INT4", "BOOL", "INT4"}, []bool{true, true, true, true, true}, 0)
		run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"clone_k_id_idx"}}))
		run(referenceSelect("SELECT pg_get_indexdef('clone_k_id_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX clone_k_id_idx ON public.clone USING btree (k) INCLUDE (id) WHERE active"}}))
		// Native pg_depend projects key/INCLUDE edges, not predicate edges.
		// Exact private predicate manifests and shared lifecycle behavior cover those.
		attributes := [][]any{{int64(2)}, {int64(3)}}
		if b.pg {
			attributes = append(attributes, []any{int64(4)})
		}
		run(referenceSelect("SELECT refobjsubid FROM pg_depend WHERE objid='clone_k_id_idx'::regclass::oid AND refobjid='clone'::regclass::oid AND refobjsubid>0 ORDER BY refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, attributes))
		run(referenceExec("INSERT INTO clone VALUES('x',1,10,TRUE,2),('x',2,20,FALSE,0),('x',3,30,NULL,0),('x',4,NULL,TRUE,2),('x',5,10,TRUE,2)", 5))
		run(partialIndexIDs("SELECT id FROM clone ORDER BY id", 1, 2, 3, 4, 5))
		run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 1, 4, 5))
		run(partialIndexIDs("SELECT id FROM clone WHERE active IS NULL ORDER BY id", 3))
		run(partialIndexIDs("SELECT id FROM clone WHERE active IS NOT TRUE ORDER BY id", 2, 3))
		run(referenceExec("UPDATE clone SET active=FALSE WHERE id=1", 1))
		run(referenceExec("UPDATE clone SET active=NULL WHERE id=4", 1))
		run(referenceExec("UPDATE clone SET active=TRUE WHERE id=2 OR id=3", 2))
		run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 2, 3, 5))
		run(referenceExec("UPDATE clone SET id=12,k=22 WHERE id=2", 1))
		run(referenceSelect("SELECT id,k FROM clone WHERE active ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}, {int64(5), int64(10)}, {int64(12), int64(22)}}))
		run(referenceExec("DELETE FROM clone WHERE id=1 OR id=3", 2))
		run(partialIndexIDs("SELECT id FROM clone ORDER BY id", 4, 5, 12))
		run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 5, 12))
		masks := []struct {
			sql     string
			indexed bool
		}{
			{"", false}, {"EXCLUDING INDEXES", false}, {"INCLUDING INDEXES EXCLUDING INDEXES", false},
			{"EXCLUDING INDEXES INCLUDING INDEXES", true}, {"INCLUDING ALL", true}, {"INCLUDING ALL EXCLUDING INDEXES", false},
			{"INCLUDING ALL EXCLUDING ALL INCLUDING INDEXES", true}, {"INCLUDING INDEXES EXCLUDING ALL", false},
		}
		for i, mask := range masks {
			name := fmt.Sprintf("mask%d", i)
			run(referenceExec("CREATE TABLE "+name+"(LIKE src "+mask.sql+")", 0))
			var names [][]any
			if mask.indexed {
				names = [][]any{{name + "_k_id_idx"}}
			}
			run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename=$1", []string{"indexname"}, []string{"NAME"}, names, name))
			run(referenceExec("INSERT INTO "+name+" VALUES(1,7,TRUE,2),(2,7,FALSE,0),(3,7,NULL,0)", 3))
			run(partialIndexIDs("SELECT id FROM "+name+" WHERE active ORDER BY id", 1))
		}
		run(referenceExec("CREATE TABLE other(other_id INT,enabled BOOL)", 0))
		run(referenceExec("CREATE INDEX other_partial ON other(other_id) WHERE enabled", 0))
		run(referenceExec("CREATE TABLE mixed(LIKE src INCLUDING ALL EXCLUDING INDEXES,LIKE other EXCLUDING INDEXES INCLUDING INDEXES)", 0))
		run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='mixed'", []string{"indexname"}, []string{"NAME"}, [][]any{{"mixed_other_id_idx"}}))
		run(referenceExec("INSERT INTO mixed VALUES(1,7,TRUE,2,8,TRUE),(2,7,NULL,0,8,FALSE)", 2))
		run(referenceSelect("SELECT id,other_id FROM mixed WHERE enabled", []string{"id", "other_id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}}))
		run(referenceExec("DROP TABLE src,other RESTRICT", 0))
		run(referenceExec("INSERT INTO clone VALUES('after',6,10,TRUE,2)", 1))
		run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 5, 6, 12))
	})
}

func TestReferenceTableLikePartialConstants(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(id INT,n INT)", 0))
		for _, truth := range []string{"FALSE", "NULL"} {
			run(referenceExec("CREATE INDEX source_constant ON src((10/n)) WHERE "+truth, 0))
			run(referenceExec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)", 0))
			run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"clone_expr_idx"}}))
			if b.pg {
				run(referenceSelect("SELECT indpred IS NOT NULL AS partial FROM pg_index WHERE indexrelid='clone_expr_idx'::regclass", []string{"partial"}, []string{"BOOL"}, [][]any{{true}}))
			}
			run(referenceExec("INSERT INTO clone VALUES('x',1,0),('x',2,0)", 2))
			run(referenceExec("UPDATE clone SET n=0", 2))
			run(partialIndexIDs("SELECT id FROM clone ORDER BY id", 1, 2))
			run(referenceExec("DROP TABLE clone", 0))
			run(referenceExec("DROP INDEX source_constant", 0))
		}
		run(referenceExec("CREATE INDEX source_true ON src(id) WHERE TRUE", 0))
		run(referenceExec("CREATE TABLE all_rows(lead TEXT,LIKE src INCLUDING ALL)", 0))
		if b.pg {
			run(referenceSelect("SELECT indpred IS NULL AS ordinary FROM pg_index WHERE indexrelid='all_rows_id_idx'::regclass", []string{"ordinary"}, []string{"BOOL"}, [][]any{{true}}))
		}
		run(referenceExec("INSERT INTO all_rows VALUES('x',1,0),('x',2,0)", 2))
		run(partialIndexIDs("SELECT id FROM all_rows WHERE TRUE ORDER BY id", 1, 2))
	})
}
