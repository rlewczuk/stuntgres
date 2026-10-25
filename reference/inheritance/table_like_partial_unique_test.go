package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikePartialUnique(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(id INT,k INT,active BOOL,gap INT,n INT)", 0))
		run(fixture.Exec("ALTER TABLE src DROP COLUMN gap", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX source_unique_partial ON src(k) WHERE active", 0))
		run(fixture.Exec("INSERT INTO src VALUES(99,99,TRUE,2)", 1))
		run(fixture.Exec("CREATE TABLE unique_clone(lead TEXT,LIKE src INCLUDING ALL)", 0))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='unique_clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"unique_clone_k_idx"}}))
		run(fixture.Select("SELECT pg_get_indexdef('unique_clone_k_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX unique_clone_k_idx ON public.unique_clone USING btree (k) WHERE active"}}))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='unique_clone'::regclass::oid AND contype='u'", []string{"conname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT * FROM unique_clone", []string{"lead", "id", "k", "active", "n"}, []string{"TEXT", "INT4", "INT4", "BOOL", "INT4"}, nil))
		run(fixture.Exec("INSERT INTO unique_clone VALUES('x',1,1,FALSE,0),('x',2,1,NULL,0),('x',3,1,TRUE,2)", 3))
		duplicate := fixture.UniqueReferenceDuplicate("unique_clone", "unique_clone_k_idx", "Key (k)=(1) already exists.")
		for _, q := range []string{
			"INSERT INTO unique_clone VALUES('x',4,1,TRUE,2)",
			"INSERT INTO unique_clone VALUES('x',4,2,TRUE,2),('x',5,1,TRUE,2)",
			"UPDATE unique_clone SET active=TRUE WHERE id=1 OR id=2",
		} {
			run(fixture.TableLikeError(q, duplicate))
			run(fixture.PartialIndexIDs("SELECT id FROM unique_clone ORDER BY id", 1, 2, 3))
			run(fixture.PartialIndexIDs("SELECT id FROM unique_clone WHERE active ORDER BY id", 3))
		}
		run(fixture.Exec("UPDATE unique_clone SET active=FALSE WHERE id=3", 1))
		run(fixture.Exec("UPDATE unique_clone SET active=TRUE WHERE id=1", 1))
		run(fixture.TableLikeError("UPDATE unique_clone SET active=TRUE WHERE id=2", duplicate))
		run(fixture.PartialIndexIDs("SELECT id FROM unique_clone WHERE active ORDER BY id", 1))
		run(fixture.Exec("DELETE FROM unique_clone WHERE id=1", 1))
		run(fixture.Exec("UPDATE unique_clone SET active=TRUE WHERE id=2", 1))
		run(fixture.PartialIndexIDs("SELECT id FROM unique_clone WHERE active ORDER BY id", 2))
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
			run(fixture.Exec("CREATE TABLE "+name+"(LIKE src "+mask.sql+")", 0))
			var names [][]any
			if mask.indexed {
				names = [][]any{{index}}
			}
			run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename=$1", []string{"indexname"}, []string{"NAME"}, names, name))
			run(fixture.Exec("INSERT INTO "+name+" VALUES(1,7,FALSE,0),(2,7,NULL,0),(3,7,TRUE,2)", 3))
			if mask.indexed {
				run(fixture.TableLikeError("INSERT INTO "+name+" VALUES(4,7,TRUE,2)", fixture.UniqueReferenceDuplicate(name, index, "Key (k)=(7) already exists.")))
				run(fixture.PartialIndexIDs("SELECT id FROM "+name+" WHERE active ORDER BY id", 3))
			} else {
				run(fixture.Exec("INSERT INTO "+name+" VALUES(4,7,TRUE,2)", 1))
				run(fixture.Exec("UPDATE "+name+" SET active=TRUE WHERE id=1 OR id=2", 2))
				run(fixture.PartialIndexIDs("SELECT id FROM "+name+" WHERE active ORDER BY id", 1, 2, 3, 4))
			}
		}
		run(fixture.Exec("DROP TABLE src RESTRICT", 0))
		run(fixture.Exec("INSERT INTO unique_clone VALUES('after',4,1,FALSE,0),('after',5,1,NULL,0)", 2))
		run(fixture.TableLikeError("INSERT INTO unique_clone VALUES('after',6,1,TRUE,2)", duplicate))
		run(fixture.PartialIndexIDs("SELECT id FROM unique_clone ORDER BY id", 2, 3, 4, 5))
	})
}

func TestReferenceTableLikePartialUniqueNulls(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(id INT,k INT,active BOOL)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX source_unique ON src(k) NULLS NOT DISTINCT WHERE active", 0))
		run(fixture.Exec("CREATE TABLE clone_k_idx(dummy INT)", 0))
		run(fixture.Exec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)", 0))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"clone_k_idx1"}}))
		run(fixture.Exec("INSERT INTO clone VALUES('x',1,NULL,FALSE),('x',2,NULL,NULL),('x',3,NULL,TRUE)", 3))
		run(fixture.TableLikeError("INSERT INTO clone VALUES('x',4,NULL,TRUE)", fixture.UniqueReferenceDuplicate("clone", "clone_k_idx1", "Key (k)=(null) already exists.")))
		run(fixture.Exec("UPDATE clone SET active=NULL WHERE id=3", 1))
		run(fixture.Exec("UPDATE clone SET active=TRUE WHERE id=1", 1))
		run(fixture.PartialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 1))
	})
}
