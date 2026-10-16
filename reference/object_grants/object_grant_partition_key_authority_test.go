package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Pinned PostgreSQL 18.0 independently establishes these key initialization
// frontiers. Native implements HASH/RANGE; LIST retains its explicit existing
// syntax boundary rather than gaining an adjacent partition implementation.
// The existing immutable providers both return INT4; HASH's modulus one avoids
// making its internal support function or hash algorithm a SQL EXECUTE target.
func TestReferenceObjectGrantPartitionKeyAuthority(t *testing.T) {
	for _, key := range []struct {
		name, typ, routine, target, list string
		zero, one, two                   string
	}{
		{"json", "JSON", "json_array_length", "pg_catalog.json_array_length(json)", "0,1,2,NULL", "[]", "[1]", "[1,2]"},
		{"text", "TEXT", "bit_length", "pg_catalog.bit_length(text)", "0,8,16,NULL", "", "a", "ab"},
	} {
		for _, strategy := range []struct {
			name, bounds string
		}{
			{"HASH", "FOR VALUES WITH(MODULUS 1,REMAINDER 0)"},
			{"RANGE", "FOR VALUES FROM(MINVALUE) TO(MAXVALUE)"},
			{"LIST", "FOR VALUES IN(" + key.list + ")"},
		} {
			t.Run(key.name+"/"+strings.ToLower(strategy.name), func(t *testing.T) {
				objectGrantScenario(t, []string{"og_partition_key"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
					targets := []string{key.target}
					if key.name == "text" {
						targets = append(targets, "pg_catalog.octet_length(text)")
					}
					defer objectGrantRoutineCleanup(t, p, c, targets)
					run(fixture.Exec("CREATE SCHEMA og_partition_key AUTHORIZATION og_object_other", 0))
					run(fixture.Exec("GRANT USAGE ON SCHEMA og_partition_key TO og_object_reader", 0))
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+key.target+" FROM PUBLIC", 0))
					create := "CREATE TABLE og_partition_key.root(id INT,v " + key.typ + ") PARTITION BY " + strategy.name + "((pg_catalog." + key.routine + "(v)))"
					if !b.PG && strategy.name == "LIST" {
						fixture.RoleGrantActor(run, "og_object_other", func() {
							s := fixture.ImplicitError(create, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "LIST"`, Position: fixture.Position(strings.Index(create, "LIST") + 1)})
							s.ErrorStage = "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							run(s)
						})
						return
					}

					// The genuine non-superuser creator has schema CREATE but no X.
					// Empty declaration is deliberately tested before owner X is granted.
					fixture.RoleGrantActor(run, "og_object_other", func() {
						run(fixture.Select("SELECT current_user AS actor", []string{"actor"}, []string{"NAME"}, [][]any{{"og_object_other"}}))
						run(fixture.Exec(create, 0))
						run(fixture.Exec("CREATE TABLE og_partition_key.leaf PARTITION OF og_partition_key.root "+strategy.bounds, 0))
					})
					run(fixture.Select("SELECT c.relname,r.rolname AS owner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='og_partition_key' ORDER BY c.relname", []string{"relname", "owner"}, []string{"NAME", "NAME"}, [][]any{{"leaf", "og_object_other"}, {"root", "og_object_other"}}))
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+key.target+" TO og_object_other", 0))
					run(fixture.Exec("GRANT INSERT,SELECT ON og_partition_key.root,og_partition_key.leaf TO og_object_reader", 0))

					const root = "og_partition_key.root"
					const leaf = "og_partition_key.leaf"
					query := "INSERT INTO " + root + " VALUES($1,$2::" + key.typ + ") RETURNING id,v::TEXT AS value"
					var held fixture.HeldStatement
					// Preparing as the denied writer must not execute a key recipe,
					// publish a row, or borrow the separately authorized owner's X.
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						run(fixture.Select("SELECT current_user AS actor", []string{"actor"}, []string{"NAME"}, [][]any{{"og_object_reader"}}))
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						held = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					})
					defer held.Statement.Close()
					read := func(table string, rows [][]any) {
						t.Helper()
						run(fixture.Select("SELECT id,v::TEXT AS value FROM "+table+" ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, rows))
					}
					denial := func(q, name string, args ...any) fixture.Step {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name}, args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						return s
					}
					insert := func(table string, id int64, value any) fixture.Step {
						return fixture.Exec("INSERT INTO "+table+" VALUES($1,$2::"+key.typ+")", 1, id, value)
					}
					deniedWrites := func(id int64) {
						t.Helper()
						fixture.RoleGrantActor(run, "og_object_reader", func() {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, denial(query, key.routine, id, key.two))
							for _, table := range []string{root, leaf} {
								s := insert(table, id, key.two)
								run(denial(s.Query, key.routine, s.Args...))
								// Strict argument NULL does not erase a retained Var recipe.
								s = insert(table, id+1, nil)
								run(denial(s.Query, key.routine, s.Args...))
								// Both an empty producer and a real NULL tuple are covered,
								// rather than calling the key function with a NULL literal.
								run(fixture.Exec("INSERT INTO "+table+" SELECT 90,NULL::"+key.typ+" WHERE FALSE", 0))
								q := "INSERT INTO " + table + " VALUES(91,NULL),(92,$1::" + key.typ + ") RETURNING id"
								run(denial(q, key.routine, key.one))
							}
						})
					}
					deniedWrites(10)
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						read(root, nil)
						read(leaf, nil)
					})

					// Binding invalid JSON is an input-import error, not execution of
					// json_array_length. A valid scalar reaches its actual body only
					// after the current writer acquires X.
					if key.name == "json" {
						fixture.RoleGrantActor(run, "og_object_reader", func() {
							s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type json", Detail: `Token "no" is invalid.`, Context: "JSON data, line 1: no\nunnamed portal parameter $2 = '...'"}, int64(80), "no")
							s.SelectRows, s.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, denial(query, key.routine, int64(81), "1"))
						})
					}
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+key.target+" TO og_object_reader", 0))
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(10), key.two}}, int64(10), key.two))
						run(insert(leaf, 20, key.one))
						// LIST bounds are actual 0/1/2 or 0/8/16, not guessed
						// residues. All strategies also exercise the zero result.
						run(insert(root, 25, key.zero))
						if key.name == "json" {
							s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: "cannot get array length of a scalar"}, int64(81), "1")
							s.SelectRows, s.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
					})
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+key.target+" FROM og_object_reader", 0))
					deniedWrites(50)
					fixture.RoleGrantActor(run, "og_object_other", func() {
						// Owner remains executable after the writer's independent revoke.
						run(insert(root, 30, key.two))
						run(insert(leaf, 31, key.one))
					})
					stored := [][]any{{int64(10), key.two}, {int64(20), key.one}, {int64(25), key.zero}, {int64(30), key.two}, {int64(31), key.one}}
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						// Reading stored values requires SELECT, not key replay. Exact
						// rows exclude every failed-write ID and any successful prefix.
						read(root, stored)
						read(leaf, stored)
					})
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+key.target+" TO og_object_reader", 0))
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(40), key.one}}, int64(40), key.one))
						if strategy.name != "RANGE" {
							// HASH skips NULL internally; LIST explicitly includes NULL.
							// Neither fact removes admission of the retained key program.
							run(insert(root, 41, nil))
							run(insert(leaf, 42, nil))
						}
					})
					stored = append(stored, []any{int64(40), key.one})
					if strategy.name != "RANGE" {
						stored = append(stored, []any{int64(41), nil}, []any{int64(42), nil})
					}

					if key.name == "text" {
						// Released bit_length(TEXT)'s genuine SQL body invokes
						// octet_length(TEXT). Outer X and owner child X cannot license
						// this writer; revoke the child independently, not int4 support.
						const child = "pg_catalog.octet_length(text)"
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+child+" FROM PUBLIC", 0))
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+child+" TO og_object_other", 0))
						childDenied := func(id int64) {
							fixture.RoleGrantActor(run, "og_object_reader", func() {
								fixture.RunStep(t, p.Ctx, held, b.Recorder, denial(query, "octet_length", id, key.two))
								s := insert(leaf, id, key.one)
								run(denial(s.Query, "octet_length", s.Args...))
							})
						}
						childDenied(60)
						fixture.RoleGrantActor(run, "og_object_other", func() { run(insert(root, 61, key.two)) })
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+child+" TO og_object_reader", 0))
						fixture.RoleGrantActor(run, "og_object_reader", func() {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(62), key.two}}, int64(62), key.two))
							run(insert(leaf, 63, key.one))
						})
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+child+" FROM og_object_reader", 0))
						childDenied(64)
						stored = append(stored, []any{int64(61), key.two}, []any{int64(62), key.two}, []any{int64(63), key.one})
					}
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						read(root, stored)
						read(leaf, stored)
					})
					// Statements are closed by defer before ACL/connection/fixture
					// cleanup, including an early oracle assertion failure.
				})
			})
		}
	}
}
