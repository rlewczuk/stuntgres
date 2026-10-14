package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegCollationPrepared(t *testing.T) {
	regObjectScenario(t, []string{"rc_held", "rc_held_other", "rn_held", "rn_renamed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA rc_held", "CREATE SCHEMA rc_held_other", "CREATE SCHEMA rn_held", `CREATE COLLATION rc_held.target FROM pg_catalog."C"`, `CREATE COLLATION rc_held_other.target FROM pg_catalog."C"`, "SET search_path TO rc_held,public")
		var original, namespace int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'target'::regcollation::bigint,'rn_held'::regnamespace::bigint").Scan(&original, &namespace); err != nil {
			t.Fatal(err)
		}
		const literalQuery = "SELECT 'target'::regcollation::bigint AS c,'rn_held'::regnamespace::bigint AS n"
		literal, err := c.PrepareContext(p.Ctx, literalQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer literal.Close()
		held := fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalQuery}
		want := fixture.Select(literalQuery, []string{"c", "n"}, []string{"INT8", "INT8"}, [][]any{{original, namespace}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		retained, err := c.QueryContext(p.Ctx, "SELECT 'target'::regcollation AS c,'rn_held'::regnamespace AS n")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		mutator, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer mutator.Close()
		regObjectExec(t, p, b, mutator, "ALTER COLLATION rc_held.target RENAME TO renamed", "ALTER SCHEMA rn_held RENAME TO rn_renamed")
		retainedTypes := []string{"REGCOLLATION", "REGNAMESPACE"}
		if b.PG {
			retainedTypes[0] = ""
		}
		fixture.ArrayReferenceConsumeHeld(t, retained, [][]any{{"target", "rn_held"}}, retainedTypes)
		// Catalog rename invalidates analysis: the held query resolves its source again.
		failed := regObjectError(literalQuery, "42704", `collation "target" for encoding "UTF8" does not exist`, "8", "")
		failed.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failed)
		regObjectBoth(t, p, b, c, regObjectSelect("SELECT $1::regcollation AS c,$2::regnamespace AS n", []string{"c", "n"}, []string{"REGCOLLATION", "REGNAMESPACE"}, [][]any{{"renamed", "rn_renamed"}}, strconv.FormatInt(original, 10), strconv.FormatInt(namespace, 10)))
		regObjectBoth(t, p, b, c, regObjectError("SELECT $1::text::regcollation AS c", "42704", `collation "target" for encoding "UTF8" does not exist`, "", "", "target"))
		regObjectExec(t, p, b, c, `CREATE COLLATION rc_held.target FROM pg_catalog."C"`)
		// Resolving the recreated collation exposes the second, renamed source name.
		failed = regObjectError(literalQuery, "3F000", `schema "rn_held" does not exist`, "44", "")
		failed.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failed)
		regObjectExec(t, p, b, c, "CREATE SCHEMA rn_held")
		var replacementCollation, replacementNamespace int64
		if err := c.QueryRowContext(p.Ctx, literalQuery).Scan(&replacementCollation, &replacementNamespace); err != nil {
			t.Fatal(err)
		}
		if replacementCollation == original || replacementNamespace == namespace {
			t.Fatal("same-name replacements must have new identities")
		}
		want.Rows = [][]any{{replacementCollation, replacementNamespace}}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		regObjectBoth(t, p, b, c, fixture.Select("SELECT ('target'::regcollation::bigint<>$1) AS replacement", []string{"replacement"}, []string{"BOOL"}, [][]any{{true}}, original))
		// A path change reanalyzes collation lookup without qualifying namespace input.
		regObjectExec(t, p, b, c, "SET search_path TO rc_held_other,public")
		var otherCollation, sameNamespace int64
		if err := c.QueryRowContext(p.Ctx, literalQuery).Scan(&otherCollation, &sameNamespace); err != nil {
			t.Fatal(err)
		}
		if otherCollation == replacementCollation || sameNamespace != replacementNamespace {
			t.Fatal("path change must select the other collation but the same namespace")
		}
		want.Rows = [][]any{{otherCollation, replacementNamespace}}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
	})
	t.Run("drop-recreate", func(t *testing.T) {
		regObjectScenario(t, []string{"rc_recreate", "rn_recreate"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regObjectExec(t, p, b, c, "CREATE SCHEMA rc_recreate", "CREATE SCHEMA rn_recreate", `CREATE COLLATION rc_recreate.target FROM pg_catalog."C"`)
			const query = "SELECT 'rc_recreate.target'::regcollation::bigint AS c,'rn_recreate'::regnamespace::bigint AS n"
			var collation, namespace int64
			if err := c.QueryRowContext(p.Ctx, query).Scan(&collation, &namespace); err != nil {
				t.Fatal(err)
			}
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			executor := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			want := fixture.Select(query, []string{"c", "n"}, []string{"INT8", "INT8"}, [][]any{{collation, namespace}})
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, want)
			regObjectExec(t, p, b, c, "DROP COLLATION rc_recreate.target", "DROP SCHEMA rn_recreate", `CREATE COLLATION rc_recreate.target FROM pg_catalog."C"`, "CREATE SCHEMA rn_recreate")
			// Pinned PostgreSQL invalidates both literal identities on DROP.
			// Read each backend's replacement referents rather than allocator goldens.
			var replacementCollation, replacementNamespace int64
			if err := c.QueryRowContext(p.Ctx, query).Scan(&replacementCollation, &replacementNamespace); err != nil {
				t.Fatal(err)
			}
			if replacementCollation == collation || replacementNamespace == namespace {
				t.Fatal("DROP/recreate must mint new object identities")
			}
			want.Rows = [][]any{{replacementCollation, replacementNamespace}}
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, want)
			regObjectBoth(t, p, b, c, fixture.Select("SELECT ('rc_recreate.target'::regcollation::bigint<>$1) AS c,('rn_recreate'::regnamespace::bigint<>$2) AS n", []string{"c", "n"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, collation, namespace))
		})
	})
}
