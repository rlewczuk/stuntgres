package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExprCollationKeys(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		fixture.CollationExec(t, p, b, `CREATE TABLE expr_coll_keys(v TEXT COLLATE expr_coll_copy)`, `CREATE INDEX expr_coll_hash ON expr_coll_keys USING hash(v)`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO expr_coll_keys VALUES('A'),('a'),('A'),('é'),(NULL)`, 5))
		exprCollationCases(t, p, b, []fixture.Step{
			fixture.Select(`SELECT v FROM expr_coll_keys ORDER BY v COLLATE "C"`, []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"A"}, {"a"}, {"é"}, {nil}}),
			fixture.Select(`SELECT v FROM expr_coll_keys ORDER BY v COLLATE "POSIX" DESC`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {"é"}, {"a"}, {"A"}, {"A"}}),
			fixture.Select(`SELECT DISTINCT v FROM expr_coll_keys ORDER BY v`, []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"a"}, {"é"}, {nil}}),
			fixture.Select(`SELECT v,count(*) AS n FROM expr_coll_keys GROUP BY v ORDER BY v`, []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"A", int64(2)}, {"a", int64(1)}, {"é", int64(1)}, {nil, int64(1)}}),
			fixture.Select(`SELECT v FROM expr_coll_keys WHERE v=$1 ORDER BY v`, []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"A"}}, "A"),
			fixture.Select(`SELECT v FROM expr_coll_keys WHERE v=$1 ORDER BY v`, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}, "a"),
			fixture.Select(`SELECT 'A'::text COLLATE expr_coll_copy='a' AS folded,'A'::text COLLATE "POSIX"='A' AS same,'é'::text COLLATE "C"='é' AS normalized`, []string{"folded", "same", "normalized"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false}}),
		})
		fixture.CollationExec(t, p, b, `CREATE TABLE expr_coll_unique(v TEXT COLLATE expr_coll_copy CONSTRAINT expr_coll_unique_key UNIQUE)`, `CREATE TABLE expr_coll_bp(v CHAR(3) COLLATE expr_coll_copy)`, `CREATE INDEX expr_coll_bp_hash ON expr_coll_bp USING hash(v)`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO expr_coll_unique VALUES('A'),('a')`, 2))
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO expr_coll_bp VALUES('A'),('A '),('a')`, 3))
		exprCollationCases(t, p, b, []fixture.Step{
			fixture.Select(`SELECT count(*) AS n FROM expr_coll_bp WHERE v=$1::char(3)`, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}, "A "),
			fixture.Select(`SELECT v,count(*) AS n FROM expr_coll_bp GROUP BY v ORDER BY v`, []string{"v", "n"}, []string{"BPCHAR", "INT8"}, [][]any{{"A  ", int64(2)}, {"a  ", int64(1)}}),
		})
		// Failed statement must not retain the preceding 'z' row; use one
		// mutation route to avoid successful routes changing each other's state.
		fixture.CollationStep(t, p, b, fixture.ImplicitError(`INSERT INTO expr_coll_unique VALUES('z'),('A')`, fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "expr_coll_unique_key"`, Detail: "Key (v)=(A) already exists.", Schema: "public", Table: "expr_coll_unique", Constraint: "expr_coll_unique_key"}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT v FROM expr_coll_unique ORDER BY v`, []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"a"}}))
		// Key conflicts are analysis errors even on empty input, unlike reached
		// scalar comparison demand.
		for _, suffix := range []string{" WHERE FALSE", " WHERE id<=2"} {
			for _, q := range []string{`SELECT a||b AS v FROM expr_coll_t` + suffix + ` ORDER BY v`, `SELECT a||b AS v,count(*) AS n FROM expr_coll_t` + suffix + ` GROUP BY a||b`, `SELECT DISTINCT a||b AS v FROM expr_coll_t` + suffix} {
				d := fixture.Diagnostic{Code: "42P21", Message: `collation mismatch between implicit collations "C" and "POSIX"`, Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: strconv.Itoa(strings.Index(q, "b AS") + 1)}
				t.Run(q, func(t *testing.T) { fixture.CollationError(t, p, b, q, d) })
			}
		}
	})
}

func TestReferenceExprCollationFeatureSmoke(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		fixture.CollationExec(t, p, b, `CREATE VIEW expr_coll_stored AS SELECT id,a COLLATE expr_coll_copy=b AS same FROM expr_coll_t`)
		stored := fixture.Select(`SELECT id,same FROM expr_coll_stored ORDER BY id`, []string{"id", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(4), nil}, {int64(5), nil}})
		stored.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 2 {
				t.Fatal(meta, err)
			}
			if meta[0].DatabaseTypeName() != "INT4" || meta[1].DatabaseTypeName() != "BOOL" {
				t.Fatal(meta)
			}
		}
		exprCollationCases(t, p, b, []fixture.Step{
			stored,
			fixture.Select(`SELECT a FROM expr_coll_t WHERE id<=2 ORDER BY a COLLATE "C"`, []string{"a"}, []string{"TEXT"}, [][]any{{"A"}, {"a"}}),
			exprCollationDemand(`SELECT a=b AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT a COLLATE "C"=b AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			exprCollationExplicit(`SELECT a COLLATE "C"=b COLLATE "POSIX" FROM expr_coll_t`, "C", "POSIX"),
			fixture.Select(`SELECT count(*) AS n FROM pg_catalog.pg_collation WHERE collname='expr_coll_copy' AND collprovider='c' AND collisdeterministic`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
		})
		fixture.CollationError(t, p, b, `SELECT 1 COLLATE "C"`, fixture.Diagnostic{Code: "42804", Message: "collations are not supported by type integer", Position: "10"})
		// pg_rewrite owns view dependencies on PostgreSQL; both engines must
		// expose the actual copy's dependency, not its equivalent builtin OID.
		oid := fixture.CollationOID(t, b.DB, "expr_coll_copy")
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT count(*) AS n FROM pg_catalog.pg_depend WHERE refclassid=3456 AND refobjid=$1`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, oid))
	})
}
