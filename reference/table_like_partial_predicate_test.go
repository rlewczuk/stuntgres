package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTableLikePartialPredicateFirst(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(id INT,n INT,active BOOL,gap INT)", 0))
		run(referenceExec("ALTER TABLE src DROP COLUMN gap", 0))
		run(referenceExec("INSERT INTO src VALUES(99,0,FALSE)", 1))
		run(referenceExec("CREATE INDEX source_expression ON src((10/n)) WHERE active", 0))
		run(referenceExec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)", 0))
		run(referenceSelect("SELECT pg_get_indexdef('clone_expr_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX clone_expr_idx ON public.clone USING btree (((10 / n))) WHERE active"}}))
		run(referenceExec("INSERT INTO clone VALUES('x',1,0,FALSE),('x',2,2,TRUE),('x',3,0,NULL),('x',4,2,FALSE)", 4))
		run(referenceExec("INSERT INTO clone VALUES('x',5,0,FALSE),('x',6,2,TRUE)", 2))
		for _, q := range []string{
			"UPDATE clone SET active=TRUE WHERE id=1 OR id=4",
			"INSERT INTO clone VALUES('x',7,2,TRUE),('x',8,0,TRUE)",
		} {
			run(tableLikeError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			run(partialIndexIDs("SELECT id FROM clone ORDER BY id", 1, 2, 3, 4, 5, 6))
			run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 2, 6))
		}
		run(referenceSelect("SELECT id,10/n AS key FROM clone WHERE active ORDER BY id", []string{"id", "key"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}, {int64(6), int64(5)}}))
		run(referenceExec("UPDATE clone SET active=FALSE WHERE id=2", 1))
		run(referenceExec("UPDATE clone SET n=0 WHERE id=2", 1))
		run(tableLikeError("UPDATE clone SET active=TRUE WHERE id=2", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
		run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 6))
		run(referenceExec("CREATE TABLE excluded(lead TEXT,LIKE src INCLUDING ALL EXCLUDING INDEXES)", 0))
		run(referenceExec("INSERT INTO excluded VALUES('x',1,0,TRUE)", 1))
		run(partialIndexIDs("SELECT id FROM excluded WHERE active ORDER BY id", 1))
		run(referenceExec("DROP TABLE src RESTRICT", 0))
		run(referenceExec("INSERT INTO clone VALUES('after',7,0,NULL),('after',8,2,TRUE)", 2))
		run(partialIndexIDs("SELECT id FROM clone WHERE active ORDER BY id", 6, 8))
	})
}

func TestReferenceTableLikePartialProviders(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE anchor(a INT)", 0))
		run(referenceExec("CREATE DOMAIN predicate_type AS INT", 0))
		run(referenceExec("CREATE TABLE src(id INT,k INT,active BOOL,gap INT,n INT)", 0))
		run(referenceExec("ALTER TABLE src DROP COLUMN gap", 0))
		run(referenceExec("CREATE INDEX source_provider ON src(k) INCLUDE(id) WHERE active AND ('anchor'::regclass IS NOT NULL) AND ('predicate_type'::regtype IS NOT NULL)", 0))
		run(referenceExec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING ALL)", 0))
		// The bounded native pg_depend projection omits predicate-only edges;
		// descriptor proofs and the actual DROP/column lifecycle below do not.
		attributes := [][]any{{int64(2)}, {int64(3)}}
		count := int64(0)
		if b.pg {
			attributes = append(attributes, []any{int64(4)})
			count = 1
		}
		run(referenceSelect("SELECT refobjsubid FROM pg_depend WHERE objid='clone_k_id_idx'::regclass::oid AND refobjid='clone'::regclass::oid AND refobjsubid>0 ORDER BY refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, attributes))
		run(referenceSelect("SELECT count(*) AS dependencies FROM pg_depend WHERE objid='clone_k_id_idx'::regclass::oid AND refobjid='anchor'::regclass::oid AND deptype='n'", []string{"dependencies"}, []string{"INT8"}, [][]any{{count}}))
		run(referenceSelect("SELECT count(*) AS dependencies FROM pg_depend WHERE objid='clone_k_id_idx'::regclass::oid AND refobjid='predicate_type'::regtype::oid AND deptype='n'", []string{"dependencies"}, []string{"INT8"}, [][]any{{count}}))
		run(referenceExec("ALTER TABLE anchor RENAME TO renamed", 0))
		run(referenceExec("DROP TABLE src RESTRICT", 0))
		run(referenceExec("INSERT INTO clone VALUES('x',1,7,TRUE,2),('x',2,7,FALSE,0),('x',3,7,NULL,0)", 3))
		run(partialIndexIDs("SELECT id FROM clone WHERE active AND ('renamed'::regclass IS NOT NULL) ORDER BY id", 1))
		run(tableLikeError("DROP TABLE renamed RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop table renamed because other objects depend on it", Detail: "index clone_k_id_idx depends on table renamed", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(referenceExec("ALTER TABLE clone DROP COLUMN active", 0))
		run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='clone'", []string{"indexname"}, []string{"NAME"}, nil))
		run(referenceExec("DROP TABLE renamed RESTRICT", 0))
		run(referenceExec("DROP DOMAIN predicate_type RESTRICT", 0))
		run(referenceSelect("SELECT id,k,n FROM clone ORDER BY id", []string{"id", "k", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(2)}, {int64(2), int64(7), int64(0)}, {int64(3), int64(7), int64(0)}}))
	})
}

func TestReferenceTableLikePartialSourceConstant(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(id INT,k INT,active BOOL)", 0))
		run(referenceExec("CREATE INDEX source_self ON src(k) WHERE active AND ('src'::regclass IS NOT NULL)", 0))
		run(referenceExec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING INDEXES)", 0))
		// Predicate-only pg_depend projection is PG-only; real DROP blocking stays shared.
		count := int64(0)
		if b.pg {
			count = 1
		}
		run(referenceSelect("SELECT count(*) AS dependencies FROM pg_depend WHERE objid='clone_k_idx'::regclass::oid AND refobjid='src'::regclass::oid AND deptype='n'", []string{"dependencies"}, []string{"INT8"}, [][]any{{count}}))
		run(tableLikeError("DROP TABLE src RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop table src because other objects depend on it", Detail: "index clone_k_idx depends on table src", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(referenceExec("INSERT INTO clone VALUES('x',1,7,TRUE),('x',2,7,NULL)", 2))
		run(partialIndexIDs("SELECT id FROM clone WHERE active AND ('src'::regclass IS NOT NULL) ORDER BY id", 1))
		run(referenceExec("DROP INDEX clone_k_idx", 0))
		run(referenceExec("DROP TABLE src RESTRICT", 0))
		run(partialIndexIDs("SELECT id FROM clone ORDER BY id", 1, 2))
	})
}
