package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// REL_18_6 makeConfigurationDependencies rebuilds edges after mapping changes;
// recordMultipleDependencies omits pinned references, not pinned dependents.
func TestReferenceFTSConfigurationSeedDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			var emitted []byte
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				mutate := func(query string) {
					s := fixture.Exec(query, 0)
					s.Prepared = prepared
					run(s)
				}
				var originalOwner string
				if b.PG {
					if err := c.QueryRowContext(p.Ctx, "SELECT cfgowner::regrole::text FROM pg_ts_config WHERE oid='pg_catalog.simple'::regconfig").Scan(&originalOwner); err != nil {
						t.Fatal("bootstrap configuration owner", err)
					}
				}
				// public reset cannot repair a modified bootstrap configuration. In
				// particular, release its custom-dictionary edge before PG teardown.
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple ALTER MAPPING FOR asciiword,numword WITH pg_catalog.simple"); err != nil {
						t.Error("restore bootstrap mappings", err)
					}
					if b.PG {
						query := "ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple OWNER TO \"" + strings.ReplaceAll(originalOwner, "\"", "\"\"") + "\""
						if _, err := c.ExecContext(ctx, query); err != nil {
							t.Error("restore bootstrap owner", err)
						}
					}
				}()
				run(ftsConfigurationMaps("pg_catalog.simple", ftsConfigurationSimpleMaps()))
				run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.config_seed_dict (TEMPLATE=pg_catalog.simple,Accept=false)", 0))
				edges := func(rows [][]any) fixture.Step {
					return fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid='public.config_seed_dict'::regdictionary::oid AS custom,refobjsubid,deptype::text FROM pg_catalog.pg_depend WHERE classid=3602 AND objid='pg_catalog.simple'::regconfig ORDER BY refclassid,refobjid,refobjsubid,deptype",
						[]string{"classid", "objid", "objsubid", "refclassid", "custom", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "BOOL", "INT4", "TEXT"}, rows)
				}
				oneEdge := [][]any{{int64(3602), int64(3748), int64(0), int64(3600), true, int64(0), "n"}}
				checkEdges := func(rows [][]any) {
					run(edges(rows))
					run(fixture.Prepared(edges(rows)))
				}
				checkEdges(nil)
				mutate("ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple ALTER MAPPING FOR asciiword,numword WITH public.config_seed_dict,public.config_seed_dict")
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal("modified seed dump", err)
					}
					emitted = bytes.Clone(dump.Bytes())
				}
				checkEdges(oneEdge)
				maps := ftsConfigurationSimpleMaps()
				// Compare the custom mapping identities directly, without a generated
				// OID golden. All remaining pinned references stay omitted above.
				mapping := fixture.Select("SELECT maptokentype,mapseqno,mapdict='public.config_seed_dict'::regdictionary::oid AS custom FROM pg_ts_config_map WHERE mapcfg='pg_catalog.simple'::regconfig AND maptokentype IN (1,3) ORDER BY maptokentype,mapseqno", []string{"maptokentype", "mapseqno", "custom"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(1), true}, {int64(1), int64(2), true}, {int64(3), int64(1), true}, {int64(3), int64(2), true}})
				vector := ftsConfigurationVector("SELECT to_tsvector('pg_catalog.simple'::regconfig,'Rats abc123') AS normalized", "")
				vector.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
				for _, s := range []fixture.Step{mapping, vector} {
					run(s)
					run(fixture.Prepared(s))
				}
				if !b.PG {
					// The helper's optional PG replay drops public, which cannot run
					// while pinned simple references the custom dictionary. Ordinary
					// SQL expectations are exercised in this scenario's PG callback;
					// reuse just the helper's complete native replacement lifecycle.
					nativeCycle := *p
					nativeCycle.Postgres = nil
					rangeControlCycle(t, &nativeCycle, b, []fixture.Step{vector, mapping, edges(oneEdge)}, [][]bool{nil, nil, nil}, nil)
				}
				if b.PG {
					// Replay the actual native stream, not a hand-written substitute.
					// Remove the existing source only after releasing simple's edge.
					if len(emitted) == 0 {
						t.Fatal("missing modified seed stream")
					}
					run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple ALTER MAPPING FOR asciiword,numword WITH pg_catalog.simple", 0))
					run(fixture.Exec("DROP TEXT SEARCH DICTIONARY public.config_seed_dict", 0))
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("modified seed PostgreSQL replay", err)
					}
					checkEdges(oneEdge)
					for _, s := range []fixture.Step{mapping, vector} {
						run(s)
						run(fixture.Prepared(s))
					}
				}
				mutate("ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple DROP MAPPING FOR asciiword")
				checkEdges(oneEdge)
				drop := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop text search configuration simple because it is required by the database system")
				denied := fixture.ErrorDiagnostic("DROP TEXT SEARCH DICTIONARY public.config_seed_dict RESTRICT", drop)
				run(denied)
				run(fixture.Prepared(denied))
				checkEdges(oneEdge)
				mutate("ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple DROP MAPPING FOR numword")
				checkEdges(nil)
				mutate("DROP TEXT SEARCH DICTIONARY public.config_seed_dict RESTRICT")
				mutate("ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple ADD MAPPING FOR asciiword,numword WITH pg_catalog.simple")
				run(ftsConfigurationMaps("pg_catalog.simple", maps))
				run(ftsConfigurationVector("SELECT to_tsvector('pg_catalog.simple'::regconfig,'The Fat Rats') AS normalized", "'fat':2 'rats':3 'the':1"))
			})
		})
	}
}

// Utility mapping operands use get_ts_dict_oid/LookupExplicitNamespace without
// an expression position, including both operands of REPLACE.
func TestReferenceFTSConfigurationDictionaryNamespaceDenial(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"config_operand_actor"})
	fixture.SchemaReferenceCleanup(t, "config_operand_owned")
	fixture.SchemaReferenceCleanup(t, "config_operand_private")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, query := range []string{
			"CREATE ROLE config_operand_actor",
			"CREATE SCHEMA config_operand_owned AUTHORIZATION config_operand_actor",
			"CREATE SCHEMA config_operand_private",
			"REVOKE ALL ON SCHEMA config_operand_private FROM PUBLIC",
			"CREATE TEXT SEARCH DICTIONARY config_operand_private.config_operand_dict (TEMPLATE=pg_catalog.simple,Accept=false)",
			"CREATE TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target OWNER TO config_operand_actor",
		} {
			run(fixture.Exec(query, 0))
		}
		queries := []string{
			"ALTER TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target ADD MAPPING FOR blank WITH config_operand_private.config_operand_dict",
			"ALTER TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target ALTER MAPPING FOR asciiword WITH config_operand_private.config_operand_dict",
			"ALTER TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target ALTER MAPPING REPLACE pg_catalog.simple WITH config_operand_private.config_operand_dict",
			"ALTER TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target ALTER MAPPING REPLACE config_operand_private.config_operand_dict WITH pg_catalog.simple",
			"ALTER TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target ALTER MAPPING FOR asciiword REPLACE pg_catalog.simple WITH config_operand_private.config_operand_dict",
			"ALTER TEXT SEARCH CONFIGURATION config_operand_owned.config_operand_target ALTER MAPPING FOR asciiword REPLACE config_operand_private.config_operand_dict WITH pg_catalog.simple",
		}
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema config_operand_private")
		check := func() {
			t.Helper()
			for _, s := range []fixture.Step{
				ftsConfigurationMaps("config_operand_owned.config_operand_target", ftsConfigurationSimpleMaps()),
				ftsConfigurationVector("SELECT to_tsvector('config_operand_owned.config_operand_target'::regconfig,'The Fat Rats') AS normalized", "'fat':2 'rats':3 'the':1"),
				fixture.Select("SELECT cfgowner='config_operand_actor'::regrole::oid AS owned,cfgparser::bigint FROM pg_ts_config WHERE oid='config_operand_owned.config_operand_target'::regconfig", []string{"owned", "cfgparser"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(3722)}}),
			} {
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION config_operand_actor", 0))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error("reset mapping actor", err)
			}
		}()
		check()
		for _, query := range queries {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(query, want)
				s.Prepared = prepared
				run(s)
				check()
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		// The destination-only sentinel distinguishes failed replacement from
		// successful replay of the source snapshot.
		run(fixture.Exec("CREATE TABLE public.config_operand_sentinel(v int)", 0))
		run(fixture.Exec("INSERT INTO public.config_operand_sentinel VALUES(42)", 1))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		oldRows, err := c.QueryContext(p.Ctx, "SELECT to_tsvector('config_operand_owned.config_operand_target'::regconfig,'The Fat Rats') AS normalized")
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		configurationVectorMetadata(t, oldRows, false)
		for _, query := range queries {
			script := dump.String() + "\nSET SESSION AUTHORIZATION config_operand_actor;\n" + query + ";"
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(script)), &want)
			check()
			run(fixture.Select("SELECT v FROM public.config_operand_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("denied dictionary Restore changed destination", err, after.String())
			}
		}
		if !oldRows.Next() {
			t.Fatal("denied dictionary Restore lost old Rows", oldRows.Err())
		}
		var value string
		if err := oldRows.Scan(&value); err != nil || value != "'fat':2 'rats':3 'the':1" {
			t.Fatal("denied dictionary Restore changed old Rows", value, err)
		}
		if oldRows.Next() || oldRows.Err() != nil {
			t.Fatal(oldRows.Err())
		}
	})
}

// CREATE resolves source configuration/parser operands with the same utility
// namespace authority as mapping dictionaries. Transaction-local catalog ACL
// changes exercise the genuine builtin parser without persisting bootstrap ACLs.
func TestReferenceFTSConfigurationSourceNamespaceDenial(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"config_source_actor"})
	fixture.SchemaReferenceCleanup(t, "config_source_owned")
	fixture.SchemaReferenceCleanup(t, "config_source_private")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, query := range []string{
			"CREATE ROLE config_source_actor",
			"CREATE SCHEMA config_source_owned AUTHORIZATION config_source_actor",
			"CREATE SCHEMA config_source_private",
			"REVOKE ALL ON SCHEMA config_source_private FROM PUBLIC",
			"CREATE TEXT SEARCH CONFIGURATION config_source_private.config_source_original (COPY=pg_catalog.simple)",
		} {
			run(fixture.Exec(query, 0))
		}
		for _, tc := range []struct {
			query   string
			message string
			state   string
			revoke  bool
		}{
			{"CREATE TEXT SEARCH CONFIGURATION config_source_owned.config_source_new (COPY=config_source_private.config_source_original)", "permission denied for schema config_source_private", "42501", false},
			{"CREATE TEXT SEARCH CONFIGURATION config_source_owned.config_source_new (PARSER=pg_catalog.default)", "permission denied for schema pg_catalog", "42501", true},
			{"CREATE TEXT SEARCH CONFIGURATION config_source_owned.config_source_new (COPY=config_source_absent.config_source_original)", `schema "config_source_absent" does not exist`, "3F000", false},
			{"CREATE TEXT SEARCH CONFIGURATION config_source_owned.config_source_new (PARSER=config_source_absent.default)", `schema "config_source_absent" does not exist`, "3F000", false},
		} {
			for _, prepared := range []bool{false, true} {
				func() {
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := tx.Rollback(); err != nil {
							t.Error("rollback source-operand authority", err)
						}
					}()
					txRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
					if tc.revoke {
						txRun(fixture.Exec("REVOKE USAGE ON SCHEMA pg_catalog FROM PUBLIC", 0))
					}
					txRun(fixture.Exec("SET LOCAL ROLE config_source_actor", 0))
					denied := fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic(tc.state, tc.message))
					denied.Prepared = prepared
					txRun(denied)
				}()
				// Neither failed CREATE publishes the destination; rollback also
				// restores the builtin namespace grant and the caller's role.
				run(fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgnamespace='config_source_owned'::regnamespace AND cfgname='config_source_new'", []string{"cfgname"}, []string{"NAME"}, nil))
				run(ftsConfigurationMaps("config_source_private.config_source_original", ftsConfigurationSimpleMaps()))
				run(ftsConfigurationVector("SELECT to_tsvector('pg_catalog.simple'::regconfig,'The Fat Rats') AS normalized", "'fat':2 'rats':3 'the':1"))
			}
		}
	})
}
