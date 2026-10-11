package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashIndexLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hash_life_a", "hash_life_b")
	hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN hash_key_domain AS BIGINT", 0),
			fixture.Exec("CREATE TABLE t(extra INT,n INT,s TEXT)", 0),
			fixture.Exec("INSERT INTO t VALUES(0,2,'B'),(0,2,'b'),(0,NULL,NULL),(0,1,'a')", 4),
			fixture.Exec("CREATE INDEX hn ON t USING hash(n)", 0),
			fixture.Exec("CREATE INDEX hs ON t USING hash(lower(s))", 0),
			fixture.Exec("CREATE INDEX bn ON t USING btree(n)", 0),
		} {
			run(s)
		}
		hashIndexProperties(run, "hn")
		for _, tc := range []struct{ index, definition string }{
			{"hn", "CREATE INDEX hn ON public.t USING hash (n)"},
			{"hs", "CREATE INDEX hs ON public.t USING hash (lower(s))"},
			{"bn", "CREATE INDEX bn ON public.t USING btree (n)"},
		} {
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('"+tc.index+"'::regclass) AS definition", tc.definition))
		}
		if b.PG {
			// This is the existing PG catalog control surface, not a native SQL
			// catalog expansion. Numeric class identities are separately discovered.
			run(fixture.Select("SELECT c.relname,c.relam,i.indnatts,i.indnkeyatts,i.indisunique,i.indkey::text,i.indoption::text,i.indexprs IS NULL AS no_expr,i.indpred IS NULL AS no_pred FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname IN ('hn','hs') ORDER BY c.relname", []string{"relname", "relam", "indnatts", "indnkeyatts", "indisunique", "indkey", "indoption", "no_expr", "no_pred"}, []string{"NAME", "OID", "INT2", "INT2", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{
				{"hn", "405", int64(1), int64(1), false, "2", "0", true, true},
				{"hs", "405", int64(1), int64(1), false, "0", "0", false, true},
			}))
		}
		run(fixture.Exec("ALTER TABLE t DROP extra", 0))
		run(fixture.Exec("ALTER TABLE t ALTER n TYPE hash_key_domain USING n::bigint", 0))
		run(fixture.Exec("ALTER TABLE t ALTER s TYPE VARCHAR USING s::varchar", 0))
		run(fixture.Select("SELECT n,s FROM t WHERE n=2 ORDER BY s", []string{"n", "s"}, []string{"INT8", "VARCHAR"}, [][]any{{int64(2), "B"}, {int64(2), "b"}}))
		run(fixture.Select("SELECT n FROM t WHERE lower(s)='b'", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}}))
		run(fixture.Exec("ALTER TABLE t ALTER n TYPE TEXT USING n::text", 0))
		run(fixture.Select("SELECT n,s FROM t WHERE n='2' ORDER BY s", []string{"n", "s"}, []string{"TEXT", "VARCHAR"}, [][]any{{"2", "B"}, {"2", "b"}}))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('hn'::regclass) AS definition", "CREATE INDEX hn ON public.t USING hash (n)"))
		// Remove the independently checked B-tree sibling so its JSON boundary
		// cannot mask the selected hash method's target-provider diagnostic.
		run(fixture.Exec("DROP INDEX bn", 0))
		hashIndexSemanticFailure(t, p, b, run, "ALTER TABLE t ALTER n TYPE JSON USING n::json", fixture.Diagnostic{Code: "42704", Message: `data type json has no default operator class for access method "hash"`, Hint: "You must specify an operator class for the index or define a default operator class for the data type."})
		run(fixture.Select("SELECT n FROM t WHERE n='2'", []string{"n"}, []string{"TEXT"}, [][]any{{"2"}, {"2"}}))
		run(fixture.Exec("CREATE INDEX bn ON t USING btree(n)", 0))
		for _, s := range []fixture.Step{
			fixture.Exec("ALTER INDEX hn RENAME TO hash_renamed", 0),
			fixture.Exec("ALTER TABLE t RENAME n TO renamed_n", 0),
			fixture.Exec("ALTER TABLE t RENAME TO renamed", 0),
			fixture.Exec("REINDEX INDEX hash_renamed", 0),
			fixture.Exec("REINDEX TABLE renamed", 0),
			fixture.Exec("CREATE SCHEMA hash_life_a", 0),
			fixture.Exec("ALTER TABLE renamed SET SCHEMA hash_life_a", 0),
			fixture.Exec("ALTER SCHEMA hash_life_a RENAME TO hash_life_b", 0),
		} {
			run(s)
		}
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('hash_life_b.hash_renamed'::regclass) AS definition", "CREATE INDEX hash_renamed ON hash_life_b.renamed USING hash (renamed_n)"))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('hash_life_b.hs'::regclass) AS definition", "CREATE INDEX hs ON hash_life_b.renamed USING hash (lower((s)::text))"))
		run(fixture.Prepared(fixture.Exec("INSERT INTO hash_life_b.renamed VALUES('2','B')", 1)))
		run(fixture.Select("SELECT renamed_n FROM hash_life_b.renamed WHERE renamed_n='2'", []string{"renamed_n"}, []string{"TEXT"}, [][]any{{"2"}, {"2"}, {"2"}}))
		run(fixture.Exec("ALTER TABLE hash_life_b.renamed DROP s", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='hs'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("DROP INDEX hash_life_b.hash_renamed", 0))
		run(fixture.Exec("DROP TABLE hash_life_b.renamed", 0))
		run(fixture.Exec("DROP SCHEMA hash_life_b", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='hash_renamed' OR relname='hs' OR relname='bn'", []string{"relname"}, []string{"NAME"}, nil))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE unique_t(n INT UNIQUE,p TEXT)", 0),
			fixture.Exec("CREATE INDEX unique_hash ON unique_t USING hash(n)", 0),
			fixture.Exec("INSERT INTO unique_t VALUES(1,'one')", 1),
			fixture.ErrorDiagnostic("INSERT INTO unique_t VALUES(1,'two')", fixture.UniqueReferenceDuplicate("unique_t", "unique_t_n_key", "Key (n)=(1) already exists.")),
		} {
			run(s)
		}
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('unique_t_n_key'::regclass) AS definition", "CREATE UNIQUE INDEX unique_t_n_key ON public.unique_t USING btree (n)"))
		run(fixture.Select("SELECT n,p FROM unique_t WHERE n=1", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	})
	t.Run("retained-class-and-conversion-atomicity", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'1'),(2,'bad')", 2))
			run(fixture.Exec("CREATE INDEX hn ON t USING hash(n oid_ops)", 0))
			run(fixture.Exec("CREATE INDEX hs ON t USING hash(s)", 0))
			hashIndexSemanticFailure(t, p, b, run, "ALTER TABLE t ALTER n TYPE TEXT USING n::text", fixture.Diagnostic{Code: "42804", Message: `operator class "oid_ops" does not accept data type text`})
			hashIndexSemanticFailure(t, p, b, run, "ALTER TABLE t ALTER s TYPE INT USING s::int", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
			run(fixture.Select("SELECT n,s FROM t ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1"}, {int64(2), "bad"}}))
			run(fixture.Exec("REINDEX TABLE t", 0))
			run(hashIndexBag("SELECT n,s FROM t WHERE n=2", [][]any{{int64(2), "bad"}}))
			run(fixture.Exec("UPDATE t SET s='2' WHERE n=2", 1))
			run(fixture.Exec("ALTER TABLE t ALTER s TYPE INT USING s::int", 0))
			run(fixture.Select("SELECT n,s FROM t WHERE s=2", []string{"n", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}}))
		})
	})
	t.Run("same-name-method-siblings", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			for _, q := range []string{
				"CREATE SCHEMA hash_life_a", "CREATE SCHEMA hash_life_b",
				"CREATE TABLE hash_life_a.t(n INT)", "CREATE TABLE hash_life_b.t(n INT)",
				"CREATE INDEX same_name ON hash_life_a.t USING hash(n)",
				"CREATE INDEX same_name ON hash_life_b.t USING btree(n)",
				"REINDEX INDEX hash_life_a.same_name", "REINDEX TABLE hash_life_b.t",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO hash_life_a.t VALUES(2),(2),(NULL)", 3))
			run(fixture.Exec("INSERT INTO hash_life_b.t VALUES(1),(3)", 2))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('hash_life_a.same_name'::regclass) AS definition", "CREATE INDEX same_name ON hash_life_a.t USING hash (n)"))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('hash_life_b.same_name'::regclass) AS definition", "CREATE INDEX same_name ON hash_life_b.t USING btree (n)"))
			run(fixture.Select("SELECT n FROM hash_life_a.t WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			run(fixture.Select("SELECT n FROM hash_life_b.t WHERE n>=1 ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
			run(fixture.Exec("DROP SCHEMA hash_life_a CASCADE", 0))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('hash_life_b.same_name'::regclass) AS definition", "CREATE INDEX same_name ON hash_life_b.t USING btree (n)"))
			run(fixture.Exec("DROP SCHEMA hash_life_b CASCADE", 0))
		})
	})
}
