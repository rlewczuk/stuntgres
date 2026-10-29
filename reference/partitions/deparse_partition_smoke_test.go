package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionFeatureSmoke(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE dp_smoke(k INT) PARTITION BY RANGE(k)",
			"CREATE TABLE dp_smoke_mid PARTITION OF dp_smoke FOR VALUES FROM(0) TO(10) PARTITION BY LIST(k)",
			"CREATE TABLE dp_smoke_nested PARTITION OF dp_smoke_mid DEFAULT",
			"CREATE TABLE dp_smoke_plain(k INT)",
			"CREATE TABLE dp_smoke_list(k INT) PARTITION BY LIST(k)",
			"CREATE TABLE dp_smoke_values PARTITION OF dp_smoke_list FOR VALUES IN(3,1,NULL)",
			"CREATE TABLE dp_smoke_only(k INT) PARTITION BY RANGE(k)",
			"CREATE TABLE dp_smoke_only_default PARTITION OF dp_smoke_only DEFAULT",
			"CREATE TABLE dp_smoke_hash(k INT) PARTITION BY HASH(k)",
			"CREATE TABLE dp_smoke_hash_leaf PARTITION OF dp_smoke_hash FOR VALUES WITH(MODULUS 4,REMAINDER 1)",
		} {
			run(fixture.Exec(q, 0))
		}
		keyQuery := "SELECT pg_get_partkeydef($1) AS definition"
		boundQuery := "SELECT pg_get_partition_constraintdef($1) AS definition"
		keyStmt, err := c.PrepareContext(p.Ctx, keyQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer keyStmt.Close()
		boundStmt, err := c.PrepareContext(p.Ctx, boundQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer boundStmt.Close()
		parent := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_smoke'::regclass::oid")
		child := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_smoke_mid'::regclass::oid")
		checkHeld := func(key, bound string) {
			for _, tc := range []struct {
				query string
				stmt  *sql.Stmt
				id    int64
				want  string
			}{{keyQuery, keyStmt, parent, key}, {boundQuery, boundStmt, child, bound}} {
				s := fixture.DeparseRelationsTextStep(t, b, tc.query, tc.want, tc.id)
				run(s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: tc.stmt, SQL: tc.query}, b.Recorder, s)
			}
		}
		checkHeld("RANGE (k)", "((k IS NOT NULL) AND (k >= 0) AND (k < 10))")
		for _, f := range []string{"pg_get_partkeydef", "pg_get_partition_constraintdef"} {
			for _, arg := range []string{"NULL::oid", "'dp_smoke_plain'::regclass"} {
				run(fixture.DeparseRelationsTextStep(t, b, "SELECT "+f+"("+arg+") AS definition", nil))
			}
		}
		run(fixture.Exec(`ALTER TABLE dp_smoke RENAME k TO "K renamed"`, 0))
		key := `RANGE ("K renamed")`
		bound := `(("K renamed" IS NOT NULL) AND ("K renamed" >= 0) AND ("K renamed" < 10))`
		checkHeld(key, bound)
		check := func() {
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_smoke'::regclass) AS definition", key))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_smoke_nested'::regclass) AS definition", bound))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_smoke_only_default'::regclass) AS definition", nil))
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_smoke_values'::regclass) AS definition", "((k IS NULL) OR (k = ANY (ARRAY[3, 1])))"))
			hashID := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_smoke_hash'::regclass::oid")
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_smoke_hash_leaf'::regclass) AS definition", fmt.Sprintf("satisfies_hash_partition('%d'::oid, 4, 1, k)", hashID)))
		}
		check()
		w := fixture.ControlsReferenceDiagnostic("42883", "function pg_get_partkeydef(boolean) does not exist")
		w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		w.Position = "8"
		run(fixture.ErrorDiagnostic("SELECT pg_get_partkeydef(true)", w))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}
