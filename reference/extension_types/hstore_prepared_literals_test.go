package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstorePreparedLiterals(t *testing.T) { hstorePreparedLiterals(t) }

// Assignment keeps the SQL independent of the mutable type name. The same
// statements must preserve literal meanings through identity-only changes, but
// must not reuse a retired hstore datum as an unrelated destination's value.
func hstorePreparedLiterals(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hs_literals_namespace")
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA hs_literals_namespace", 0))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_prepared_literals(id integer NOT NULL,v hstore)", 0))
		cases := []struct {
			name, literal   string
			canonical, text any
		}{
			{"nonempty", `'aa=>long,b=>short,a=>NULL'`, `"a"=>NULL, "b"=>"short", "aa"=>"long"`, "aa=>long,b=>short,a=>NULL"},
			{"empty", `''`, "", ""},
			{"sql_null", `NULL`, nil, nil},
			{"present_null", `'a=>NULL'`, `"a"=>NULL`, "a=>NULL"},
			{"quoted_null", `'a=>"NULL"'`, `"a"=>"NULL"`, `a=>"NULL"`},
		}
		queries := make([]string, len(cases))
		statements := make([]*sql.Stmt, len(cases))
		for i, tc := range cases {
			queries[i] = "INSERT INTO hs_prepared_literals VALUES ($1," + tc.literal + ")"
			stmt, err := c.PrepareContext(p.Ctx, queries[i])
			if err != nil {
				t.Fatalf("initial Prepare %s: %v", tc.name, err)
			}
			defer stmt.Close()
			statements[i] = stmt
		}
		var want [][]any
		id := 0
		phase := func(name string, textColumn bool) {
			for i, tc := range cases {
				for _, route := range []string{"direct", "fresh", "held"} {
					t.Run(name+"/"+tc.name+"/"+route, func(t *testing.T) {
						id++
						s := fixture.Exec(queries[i], 1, id)
						s.Prepared = route == "fresh"
						var db fixture.Executor = c
						if route == "held" {
							db = fixture.HeldStatement{Executor: c, Statement: statements[i], SQL: queries[i]}
						}
						hstoreRun(t, p, b, db, s)
						value := tc.canonical
						if textColumn {
							value = tc.text
						}
						want = append(want, []any{int64(id), value})
					})
				}
			}
			q := "SELECT id,v FROM hs_prepared_literals ORDER BY id"
			var check fixture.Step
			if textColumn {
				check = fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TEXT"}, want)
			} else {
				check = hstoreSelect(b, q, []string{"id", "v"}, want, []bool{false, true})
			}
			hstoreRun(t, p, b, c, check)
			hstoreRun(t, p, b, c, fixture.Prepared(check))
		}
		identity := func(name string) int64 {
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT ("+hstoreLiteral(name)+"::regtype)::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid <= 0 {
				t.Fatalf("nonpositive type identity %s: %d", name, oid)
			}
			return oid
		}
		original := identity("public.hstore")
		phase("before", false)
		hstoreRun(t, p, b, c, fixture.Exec("ALTER TYPE hstore RENAME TO hs_literal_renamed", 0))
		if got := identity("public.hs_literal_renamed"); got != original {
			t.Fatalf("rename replaced logical type %d -> %d", original, got)
		}
		phase("renamed", false)
		hstoreRun(t, p, b, c, fixture.Exec("ALTER TYPE public.hs_literal_renamed SET SCHEMA hs_literals_namespace", 0))
		if got := identity("hs_literals_namespace.hs_literal_renamed"); got != original {
			t.Fatalf("schema move replaced logical type %d -> %d", original, got)
		}
		phase("moved", false)

		// Deliberate consumer teardown retires the original installation. TEXT
		// assignment must retain original literal bytes, not old canonical hstore.
		hstoreRun(t, p, b, c, fixture.Exec("DROP TABLE hs_prepared_literals", 0))
		hstoreRun(t, p, b, c, fixture.Exec("DROP EXTENSION hstore", 0))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_prepared_literals(id integer NOT NULL,v text)", 0))
		want, id = nil, 0
		phase("unrelated_text_destination", true)

		// Reusing the SQL against a new installation must perform fresh binding;
		// the retired descriptor is not authorized by the restored public label.
		hstoreRun(t, p, b, c, fixture.Exec("DROP TABLE hs_prepared_literals", 0))
		hstoreInstall(t, p, b, c)
		if current := identity("public.hstore"); current == original {
			t.Fatalf("reinstallation reused retired type identity %d", original)
		}
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_prepared_literals(id integer NOT NULL,v hstore)", 0))
		want, id = nil, 0
		phase("reinstalled", false)
	})
}
