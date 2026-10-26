package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringVariadicDeparseRelations(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			`CREATE SCHEMA sv_source`,
			`CREATE TABLE sv_source.sv_target(v text)`,
			`CREATE TABLE public.sv_target(v text)`,
			`CREATE VIEW public.sv_relation_view AS SELECT v FROM sv_source.sv_target`,
			`CREATE VIEW public.sv_relation_only_view AS SELECT v FROM ONLY sv_source.sv_target`,
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		defer fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DROP SCHEMA sv_source CASCADE`, 0))
		var canonical bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &canonical); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ name, path, off, on string }{
			{"absent", "pg_catalog", "sv_source.sv_target", `"sv_source"."sv_target"`},
			{"visible", "sv_source,public", "sv_target", `"sv_target"`},
			{"shadowed", "public,sv_source", "sv_source.sv_target", `"sv_source"."sv_target"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path="+tc.path, 0))
				for i, setting := range []string{"off", "on", "off"} {
					t.Run(fmt.Sprintf("%d_%s", i, setting), func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers="+setting, 0))
						column, source := "v", tc.off
						if setting == "on" {
							column, source = `"v"`, tc.on
						}
						formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT pg_get_viewdef('public.sv_relation_view'::regclass)`, []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{" SELECT " + column + "\n   FROM " + source + ";"}}))
						formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT pg_get_viewdef('public.sv_relation_only_view'::regclass)`, []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{" SELECT " + column + "\n   FROM ONLY " + source + ";"}}))
						if !b.PG {
							var dump bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
								t.Fatal(err)
							}
							if !bytes.Equal(canonical.Bytes(), dump.Bytes()) {
								t.Fatal("search path or quote policy changed canonical relation Dump")
							}
						}
					})
				}
			})
		}
	})
}

func TestReferenceStringVariadicBoundFormat(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE sv_bound_format(v text DEFAULT format('%I','foo'),CONSTRAINT sv_bound_format_check CHECK(format('%s',v)<>'bad'))`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE sv_bound_format_typed(v text DEFAULT format('%I','foo'::text))`, 0))
		var canonical bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &canonical); err != nil {
				t.Fatal(err)
			}
		}
		verify := func(t *testing.T, backend fixture.Backend, conn *sql.Conn) {
			for i, setting := range []string{"off", "on", "off"} {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET quote_all_identifiers="+setting, 0))
				t.Run(fmt.Sprintf("%d_%s", i, setting), func(t *testing.T) {
					definition := `format('%I'::text, 'foo')`
					typed := `format('%I'::text, 'foo'::text)`
					check := `CHECK ((format('%s'::text, v) <> 'bad'::text))`
					if setting == "on" {
						definition = `"format"('%I'::"text", 'foo')`
						typed = `"format"('%I'::"text", 'foo'::"text")`
						check = `CHECK (("format"('%s'::"text", "v") <> 'bad'::"text"))`
					}
					for _, tc := range []struct{ name, query, want string }{
						{"default", `SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_attrdef WHERE adrelid='sv_bound_format'::regclass::oid`, definition},
						{"typed_default", `SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_attrdef WHERE adrelid='sv_bound_format_typed'::regclass::oid`, typed},
						{"check", `SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conname='sv_bound_format_check'`, check},
					} {
						t.Run(tc.name, func(t *testing.T) {
							formatReferenceBoth(t, p, backend, conn, fixture.Select(tc.query, []string{"definition"}, []string{"TEXT"}, [][]any{{tc.want}}))
						})
					}
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(canonical.Bytes(), dump.Bytes()) {
							t.Fatal("quote policy changed canonical bound format Dump")
						}
					}
				})
			}
		}
		t.Run("initial", func(t *testing.T) { verify(t, b, c) })
		if b.PG {
			return // Native replacement APIs do not exist on PostgreSQL.
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(canonical.Bytes())); err != nil {
			t.Fatal(err)
		}
		t.Run("restored", func(t *testing.T) { verify(t, b, c) })
		for _, backend := range p.Backends() {
			if !backend.PG {
				continue
			}
			if _, err := backend.DB.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.DB.ExecContext(p.Ctx, canonical.String()); err != nil {
				t.Fatal("PostgreSQL replay of canonical unknown/typed format SQL", err)
			}
			peer, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("postgres_replayed", func(t *testing.T) { verify(t, backend, peer) })
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.DB.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceStringVariadicViewWrites(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for route, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("route_%d", route), func(t *testing.T) {
				base, inner, outer := fmt.Sprintf("sv_write_base_%d", route), fmt.Sprintf("sv_write_inner_%d", route), fmt.Sprintf("sv_write_outer_%d", route)
				for _, q := range []string{
					"CREATE TABLE " + base + "(id integer,v text)",
					"CREATE VIEW " + inner + " AS SELECT id,v FROM " + base + " WHERE v<>quote_ident('inval') AND concat(quote_nullable(v),'')=quote_nullable(v) WITH LOCAL CHECK OPTION",
					"CREATE VIEW " + outer + " AS SELECT id,v FROM " + inner + " WHERE v<>quote_ident('outvl') WITH LOCAL CHECK OPTION",
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				run := func(t *testing.T, s fixture.Step) {
					t.Helper()
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				run(t, fixture.Exec("INSERT INTO "+outer+" VALUES(1,'seed'),(2,'other')", 2))
				run(t, fixture.Exec("UPDATE "+outer+" SET v=concat(v,'!') WHERE id=1", 1))
				unchanged := fixture.Select("SELECT id,v FROM "+base+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "seed!"}, {int64(2), "other"}})
				run(t, unchanged)
				for _, value := range []string{"inval", "outvl"} {
					for _, tc := range []struct{ name, query string }{
						{"insert", "INSERT INTO " + outer + " VALUES(3,'accepted'),(4,'" + value + "')"},
						{"update", "UPDATE " + outer + " SET v=CASE WHEN id=1 THEN 'accepted' ELSE '" + value + "' END"},
					} {
						t.Run(tc.name+"_"+value, func(t *testing.T) {
							layer, id := inner, 2
							if value == "outvl" {
								layer = outer
							}
							if tc.name == "insert" {
								id = 4
							}
							d := fixture.Diagnostic{
								Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "44000",
								Message: "new row violates check option for view \"" + layer + "\"",
								Detail:  fmt.Sprintf("Failing row contains (%d, %s).", id, value),
							}
							s := fixture.ErrorDiagnostic(tc.query, d)
							s.ErrorStage = "execute"
							run(t, s)
						})
						// Every rejected multirow write must leave the base unchanged.
						run(t, unchanged)
					}
				}
			})
		}
	})
}

// Parameterized custom plans re-evaluate immutable quotes when planned again.
// The stored rows also witness omitted defaults and DISCARD refresh.
func TestReferenceStringVariadicMergeDefaults(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE sv_merge_defaults(id integer,a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'),r text)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET quote_all_identifiers=off`, 0))
		// Keep both row identity uses explicitly parameter-dependent.
		q := `MERGE INTO sv_merge_defaults t USING (VALUES($1::integer)) s(id) ON t.id=$1::integer WHEN NOT MATCHED THEN INSERT(id,r) VALUES($1::integer,quote_ident('root'))`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		// Pinned lib/pq does not expose MERGE's command count; stored rows below
		// independently prove every insert, as in the existing MERGE witnesses.
		affected := int64(1)
		if b.PG {
			affected = 0
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET quote_all_identifiers=on`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, affected, int64(1)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET quote_all_identifiers=off`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, affected, int64(2)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, affected, int64(3)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, affected, int64(5))))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DISCARD PLANS`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, affected, int64(4)))
		// Full independently declared rows include both held custom executions.
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT id FROM sv_merge_defaults ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT id,a,b,r FROM sv_merge_defaults WHERE id<=2 ORDER BY id`, []string{"id", "a", "b", "r"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{
			{int64(1), `"a"`, `"b"`, `"root"`},
			{int64(2), "a", "b", "root"},
		}))
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT id,a,b,r FROM sv_merge_defaults WHERE id>=3 ORDER BY id`, []string{"id", "a", "b", "r"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{
			{int64(3), "a", "b", "root"},
			{int64(4), "a", "b", "root"},
			{int64(5), "a", "b", "root"},
		}))
	})
}

// PostgreSQL's observed custom/generic choice and native's approved fresh
// parameterized policy are asserted independently, never treated as parity.
func TestReferenceStringVariadicParameterized(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		set := func(setting, bytea string) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers="+setting, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET bytea_output="+bytea, 0))
		}
		command := func(q string) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		prepare := func(q string) fixture.HeldStatement {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			})
			return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		}
		counters := func(label, q string, custom, generic int64) {
			if !b.PG {
				return // Native intentionally has no planner counters/catalog.
			}
			t.Run(label+"_plans", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
					`SELECT custom_plans,generic_plans FROM pg_catalog.pg_prepared_statements WHERE statement=$1`,
					[]string{"custom_plans", "generic_plans"}, []string{"INT8", "INT8"},
					[][]any{{custom, generic}}, q))
			})
		}
		const hexQuote = `E'\\x00015c'`
		const escapeQuote = `E'\\000\\001\\\\'`
		assertSelect := func(label string, held fixture.HeldStatement, id int64, literal, parameter, bytea string, custom, generic int64) {
			t.Run(label+"_values", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL,
					[]string{"literal", "parameter", "bytea", "id"}, []string{"TEXT", "TEXT", "TEXT", "INT4"},
					[][]any{{literal, parameter, bytea, id}}, "arg", id))
			})
			counters(label, held.SQL, custom, generic)
		}
		set("off", "hex")
		selectSQL := `SELECT quote_ident('lit') AS literal,quote_ident($1::text) AS parameter,quote_literal('\x00015c'::bytea) AS bytea,$2::integer AS id`
		peerSQL := `SELECT quote_ident('peer') AS literal,quote_ident($1::text) AS parameter,quote_literal('\x00015c'::bytea) AS bytea,$2::integer AS id`
		literal, peer := prepare(selectSQL), prepare(peerSQL)
		command(`CREATE TABLE sv_parameter_merge(id integer,a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'),r text)`)
		mergeSQL := `MERGE INTO sv_parameter_merge t USING (VALUES($1::integer)) s(id) ON t.id=$1::integer WHEN NOT MATCHED THEN INSERT(id,r) VALUES($1::integer,quote_ident('root'))`
		merge := prepare(mergeSQL)
		counters("select_prepared", selectSQL, 0, 0)
		counters("merge_prepared", mergeSQL, 0, 0)
		for id := int64(1); id <= 8; id++ {
			assertSelect(fmt.Sprintf("peer_warm_%d", id), peer, id, "peer", "arg", hexQuote, min(id, 5), max(id-5, 0))
		}
		affected := int64(1)
		if b.PG {
			affected = 0 // pq command-count boundary; actual rows remain mandatory.
		}
		var stored [][]any
		for id := int64(1); id <= 10; id++ {
			setting, bytea := "off", "hex"
			on := id <= 8 && id%2 == 1 || id == 10
			if on {
				setting, bytea = "on", "escape"
			}
			set(setting, bytea)
			if id == 9 {
				command(`DISCARD PLANS`)
			}
			lit, param, output, a, defaultB, root := "lit", "arg", hexQuote, "a", "b", "root"
			if on {
				param = `"arg"`
				if !b.PG || id <= 5 {
					lit, output, a, defaultB, root = `"lit"`, escapeQuote, `"a"`, `"b"`, `"root"`
				}
			}
			label := fmt.Sprintf("execute_%d_%s", id, setting)
			assertSelect("select_"+label, literal, id, lit, param, output, min(id, 5), max(id-5, 0))
			fixture.RunStep(t, p.Ctx, merge, b.Recorder, fixture.Exec(mergeSQL, affected, id))
			stored = append(stored, []any{id, a, defaultB, root})
			t.Run("merge_"+label+"_stored", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT id,a,b,r FROM sv_parameter_merge ORDER BY id`,
					[]string{"id", "a", "b", "r"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, stored))
			})
			counters("merge_"+label, mergeSQL, min(id, 5), max(id-5, 0))
		}
		// Same physical connection, independent held owner: DISCARD invalidates
		// lazily. PostgreSQL history survives transaction/savepoint rollback.
		assertSelect("peer_before_transaction", peer, 11, `"peer"`, `"arg"`, escapeQuote, 5, 4)
		command(`BEGIN`)
		command(`SET LOCAL quote_all_identifiers=off`)
		command(`SET LOCAL bytea_output=hex`)
		command(`DISCARD PLANS`)
		assertSelect("select_transaction_discard_off", literal, 12, "lit", "arg", hexQuote, 5, 6)
		command(`ROLLBACK`)
		lit, output := `"lit"`, escapeQuote
		if b.PG {
			lit, output = "lit", hexQuote
		}
		assertSelect("select_after_rollback_on", literal, 13, lit, `"arg"`, output, 5, 7)
		assertSelect("peer_after_rollback_on", peer, 14, `"peer"`, `"arg"`, escapeQuote, 5, 5)
		command(`BEGIN`)
		command(`SAVEPOINT sv_oracle`)
		command(`SET LOCAL quote_all_identifiers=off`)
		command(`SET LOCAL bytea_output=hex`)
		command(`DISCARD PLANS`)
		assertSelect("select_savepoint_discard_off", literal, 15, "lit", "arg", hexQuote, 5, 8)
		command(`ROLLBACK TO SAVEPOINT sv_oracle`)
		assertSelect("select_after_savepoint_rollback_on", literal, 16, lit, `"arg"`, output, 5, 9)
		assertSelect("peer_after_savepoint_rollback_on", peer, 17, `"peer"`, `"arg"`, escapeQuote, 5, 6)
		command(`ROLLBACK`)
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT id,a,b,r FROM sv_parameter_merge ORDER BY id`,
			[]string{"id", "a", "b", "r"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, stored))
	})
}

// This shared scenario preserves the complete owner-operation matrix of the
// internal retained-generation witness without asserting private planner state.
func TestReferenceStringVariadicOperationLocal(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		command := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		command("CREATE TABLE sv_operation_merge(id integer,a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'),r text)")
		query := "MERGE INTO sv_operation_merge t USING (VALUES($1::integer)) s(id) ON t.id=$1::integer WHEN NOT MATCHED THEN INSERT(id,r) VALUES($1::integer,quote_ident('root'))"
		prepare := func(q string) fixture.HeldStatement {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			t.Cleanup(func() { if err := stmt.Close(); err != nil { t.Error(err) } })
			return fixture.HeldStatement{Executor:c, Statement:stmt, SQL:q}
		}
		owner, peer := prepare(query), prepare(query)
		affected := int64(1)
		if b.PG { affected = 0 } // Existing pq MERGE command-count boundary.
		for id := int64(1); id <= 12; id++ {
			on, setting := id%2 == 1, "off"
			if on { setting = "on" }
			if id == 9 || id == 11 {
				command("BEGIN")
				if id == 11 { command("SAVEPOINT sv_operation") }
				command("SET LOCAL quote_all_identifiers="+setting)
				command("DISCARD PLANS")
			} else {
				if id == 10 { command("ROLLBACK") }
				if id == 12 { command("ROLLBACK TO SAVEPOINT sv_operation") }
				command("SET quote_all_identifiers="+setting)
			}
			if id == 7 { command("DISCARD PLANS") }
			held := owner
			if id%2 == 0 { held = peer }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, affected, id))
			a, valueB, root := "a", "b", "root"
			if on { a, valueB, root = `"a"`, `"b"`, `"root"` }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a,b,r FROM sv_operation_merge WHERE id=$1", []string{"a","b","r"}, []string{"TEXT","TEXT","TEXT"}, [][]any{{a,valueB,root}}, id))
		}
		command("ROLLBACK")
		command("CREATE TABLE sv_operation_base(id integer,r text)")
		command("CREATE VIEW sv_operation_view AS SELECT id,r FROM sv_operation_base WHERE r<>quote_ident('blocked') WITH LOCAL CHECK OPTION")
		viewSQL := "INSERT INTO sv_operation_view VALUES($1::integer,quote_ident('root'))"
		view := prepare(viewSQL)
		for id := int64(1); id <= 2; id++ {
			setting, root := "off", "root"
			if id == 1 { setting, root = "on", `"root"` }
			command("SET quote_all_identifiers="+setting)
			fixture.RunStep(t, p.Ctx, view, b.Recorder, fixture.Exec(viewSQL, 1, id))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT r FROM sv_operation_base WHERE id=$1", []string{"r"}, []string{"TEXT"}, [][]any{{root}}, id))
		}
	})
}
