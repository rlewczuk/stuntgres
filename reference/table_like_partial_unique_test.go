package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceTableLikePartialUnique(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(id INT,k INT,active BOOL,gap INT,n INT)", 0))
		run(referenceExec("ALTER TABLE src DROP COLUMN gap", 0))
		run(referenceExec("CREATE UNIQUE INDEX source_unique_partial ON src(k) WHERE active", 0))
		run(referenceExec("INSERT INTO src VALUES(99,99,TRUE,2)", 1))
		run(referenceExec("CREATE TABLE unique_clone(lead TEXT,LIKE src INCLUDING ALL)", 0))
		run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='unique_clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"unique_clone_k_idx"}}))
		run(referenceSelect("SELECT pg_get_indexdef('unique_clone_k_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX unique_clone_k_idx ON public.unique_clone USING btree (k) WHERE active"}}))
		run(referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='unique_clone'::regclass::oid AND contype='u'", []string{"conname"}, []string{"NAME"}, nil))
		run(referenceSelect("SELECT * FROM unique_clone", []string{"lead", "id", "k", "active", "n"}, []string{"TEXT", "INT4", "INT4", "BOOL", "INT4"}, nil))
		run(referenceExec("INSERT INTO unique_clone VALUES('x',1,1,FALSE,0),('x',2,1,NULL,0),('x',3,1,TRUE,2)", 3))
		duplicate := uniqueReferenceDuplicate("unique_clone", "unique_clone_k_idx", "Key (k)=(1) already exists.")
		for _, q := range []string{
			"INSERT INTO unique_clone VALUES('x',4,1,TRUE,2)",
			"INSERT INTO unique_clone VALUES('x',4,2,TRUE,2),('x',5,1,TRUE,2)",
			"UPDATE unique_clone SET active=TRUE WHERE id=1 OR id=2",
		} {
			run(tableLikeError(q, duplicate))
			run(partialIndexIDs("SELECT id FROM unique_clone ORDER BY id", 1, 2, 3))
			run(partialIndexIDs("SELECT id FROM unique_clone WHERE active ORDER BY id", 3))
		}
		run(referenceExec("UPDATE unique_clone SET active=FALSE WHERE id=3", 1))
		run(referenceExec("UPDATE unique_clone SET active=TRUE WHERE id=1", 1))
		run(tableLikeError("UPDATE unique_clone SET active=TRUE WHERE id=2", duplicate))
		run(partialIndexIDs("SELECT id FROM unique_clone WHERE active ORDER BY id", 1))
		run(referenceExec("DELETE FROM unique_clone WHERE id=1", 1))
		run(referenceExec("UPDATE unique_clone SET active=TRUE WHERE id=2", 1))
		run(partialIndexIDs("SELECT id FROM unique_clone WHERE active ORDER BY id", 2))
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
			index := name + "_k_idx"
			run(referenceExec("CREATE TABLE "+name+"(LIKE src "+mask.sql+")", 0))
			var names [][]any
			if mask.indexed {
				names = [][]any{{index}}
			}
			run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename=$1", []string{"indexname"}, []string{"NAME"}, names, name))
			run(referenceExec("INSERT INTO "+name+" VALUES(1,7,FALSE,0),(2,7,NULL,0),(3,7,TRUE,2)", 3))
			if mask.indexed {
				run(tableLikeError("INSERT INTO "+name+" VALUES(4,7,TRUE,2)", uniqueReferenceDuplicate(name, index, "Key (k)=(7) already exists.")))
				run(partialIndexIDs("SELECT id FROM "+name+" WHERE active ORDER BY id", 3))
			} else {
				run(referenceExec("INSERT INTO "+name+" VALUES(4,7,TRUE,2)", 1))
				run(referenceExec("UPDATE "+name+" SET active=TRUE WHERE id=1 OR id=2", 2))
				run(partialIndexIDs("SELECT id FROM "+name+" WHERE active ORDER BY id", 1, 2, 3, 4))
			}
		}
		run(referenceExec("DROP TABLE src RESTRICT", 0))
		run(referenceExec("INSERT INTO unique_clone VALUES('after',4,1,FALSE,0),('after',5,1,NULL,0)", 2))
		run(tableLikeError("INSERT INTO unique_clone VALUES('after',6,1,TRUE,2)", duplicate))
		run(partialIndexIDs("SELECT id FROM unique_clone ORDER BY id", 2, 3, 4, 5))
	})
}

func TestReferenceTableLikePartialUniqueNulls(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(id INT,k INT,active BOOL)", 0))
		run(referenceExec("CREATE UNIQUE INDEX source_unique ON src(k) NULLS NOT DISTINCT WHERE active", 0))
		run(referenceExec("CREATE TABLE clone_k_idx(dummy INT)", 0))
		run(referenceExec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)", 0))
		run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"clone_k_idx1"}}))
		run(referenceExec("INSERT INTO clone VALUES('x',1,NULL,FALSE),('x',2,NULL,NULL),('x',3,NULL,TRUE)", 3))
		run(tableLikeError("INSERT INTO clone VALUES('x',4,NULL,TRUE)", uniqueReferenceDuplicate("clone", "clone_k_idx1", "Key (k)=(null) already exists.")))
		run(referenceExec("UPDATE clone SET active=NULL WHERE id=3", 1))
		run(referenceExec("UPDATE clone SET active=TRUE WHERE id=1", 1))
		run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 1))
	})
}
