package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionVersionSchemaAndPrivilege(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES(77)", 1))
		for _, tc := range []struct{ version, message, detail string }{{"missing", `extension "unaccent" has no installation script nor update path for version "missing"`, ""}, {"--", `invalid extension version name: "--"`, `Version names must not contain "--".`}, {"", `invalid extension version name: ""`, `Version names must not be empty.`}} {
			run(fixture.ImplicitError("CREATE EXTENSION unaccent SCHEMA missing_ext_schema VERSION '"+tc.version+"'", fixture.Diagnostic{Code: "22023", Message: tc.message, Detail: tc.detail}))
			extensionCount(t, p, c, r, 0)
			extensionAbsentMembers(t, p, c, r)
		}
		run(fixture.ImplicitError("CREATE EXTENSION unaccent VERSION '1.1' VERSION '1.1'", fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options", Position: "41"}))
		extensionCount(t, p, c, r, 0)
		run(extensionError("CREATE EXTENSION unaccent SCHEMA missing_ext_schema", "3F000", `schema "missing_ext_schema" does not exist`))
		extensionCount(t, p, c, r, 0)
		run(fixture.Exec("SET search_path TO missing_ext_schema", 0))
		run(extensionError("CREATE EXTENSION unaccent", "3F000", "no schema has been selected to create in"))
		run(fixture.Exec("RESET search_path", 0))
		extensionAbsentMembers(t, p, c, r)
		run(fixture.Exec("CREATE SCHEMA ext_locked", 0))
		run(fixture.Exec("CREATE USER ext_installer CREATEDB", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		target := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
		run(fixture.Exec("REVOKE CREATE ON DATABASE "+target+" FROM PUBLIC", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		denied := fixture.ImplicitError("CREATE EXTENSION unaccent SCHEMA ext_locked", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "unaccent"`, Hint: "Must have CREATE privilege on current database to create this extension."})
		run(denied)
		run(extensionError("CREATE EXTENSION unaccent SCHEMA missing_ext_schema VERSION 'missing'", "22023", `extension "unaccent" has no installation script nor update path for version "missing"`))
		run(extensionError("CREATE EXTENSION unaccent SCHEMA missing_ext_schema", "3F000", `schema "missing_ext_schema" does not exist`))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		extensionCount(t, p, c, r, 0)
		extensionAbsentMembers(t, p, c, r)
		run(fixture.Exec("GRANT CREATE ON DATABASE "+target+" TO ext_installer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_locked", 0))
		extensionInventory(t, p, c)
		var owner string
		if err := c.QueryRowContext(p.Ctx, "SELECT rolname FROM pg_roles WHERE oid=(SELECT extowner FROM pg_extension WHERE extname='unaccent')").Scan(&owner); err != nil || owner != "ext_installer" {
			t.Fatalf("extension owner %q: %v", owner, err)
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT proowner FROM pg_proc WHERE proname='unaccent'")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var oid int64
			if err := rows.Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid != 10 {
				t.Fatal("trusted member owner", oid)
			}
			n++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if n != 2 {
			t.Fatal("trusted overload count", n)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE CREATE ON DATABASE "+target+" FROM ext_installer", 0))
		run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
		if pg {
			run(fixture.Exec("DROP EXTENSION unaccent", 0))
		} else {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		run(fixture.ImplicitError("CREATE EXTENSION unaccent SCHEMA public", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "unaccent"`, Hint: "Must have CREATE privilege on current database to create this extension."}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		extensionCount(t, p, c, r, 0)
	})
}

func TestReferenceExtensionTransactions(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var db *sql.DB
		for _, b := range p.Backends() {
			if b.PG == pg {
				db = b.DB
			}
		}
		peer, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
		for _, commit := range []bool{false, true} {
			run(c, fixture.Exec("BEGIN", 0))
			run(c, fixture.Exec("SAVEPOINT before_install", 0))
			run(c, fixture.Exec("CREATE EXTENSION unaccent", 0))
			extensionCount(t, p, c, r, 1)
			extensionCount(t, p, peer, r, 0)
			run(c, fixture.Select("SELECT unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			run(peer, extensionMissing("SELECT unaccent('Hôtel')", "unaccent", "unknown"))
			if !commit {
				run(c, fixture.Exec("ROLLBACK TO before_install", 0))
				extensionCount(t, p, c, r, 0)
				run(c, extensionMissing("SELECT unaccent('Hôtel')", "unaccent", "unknown"))
				run(c, fixture.Exec("ROLLBACK", 0))
				extensionCount(t, p, peer, r, 0)
			} else {
				run(c, fixture.Exec("SAVEPOINT after_install", 0))
				run(c, fixture.Exec("ROLLBACK TO after_install", 0))
				run(c, fixture.Exec("COMMIT", 0))
				extensionCount(t, p, peer, r, 1)
				run(peer, fixture.Prepared(fixture.Select("SELECT unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})))
			}
		}
		run(c, fixture.Exec("BEGIN", 0))
		run(c, extensionError("CREATE EXTENSION unaccent", "42710", `extension "unaccent" already exists`))
		run(c, extensionError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		run(c, fixture.Exec("ROLLBACK", 0))
		run(c, fixture.Exec("BEGIN READ ONLY", 0))
		stmt, err := c.PrepareContext(p.Ctx, "CREATE EXTENSION unaccent")
		if err != nil {
			t.Fatalf("read-only Prepare unexpectedly failed: %v", err)
		}
		stmt.Close()
		run(c, extensionError("CREATE EXTENSION unaccent", "25006", "cannot execute CREATE EXTENSION in a read-only transaction"))
		run(c, fixture.Exec("ROLLBACK", 0))
		extensionConcurrentInstall(t, p, c, r, pg, db, peer)
	})
}

func TestReferenceExtensionControls(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE TABLE source(v TEXT CHECK(ext_addon.unaccent(v) <> ''))", 0))
		run(fixture.Exec("INSERT INTO source VALUES('Hôtel')", 1))
		run(fixture.Exec("CREATE VIEW translated AS SELECT ext_addon.unaccent(v) FROM source", 0))
		check := fixture.Select("SELECT unaccent FROM translated", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})
		run(check)
		if !pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			held, err := c.QueryContext(p.Ctx, "SELECT unaccent FROM translated")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			metadata, err := held.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal("retained metadata", metadata, err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			extensionCount(t, p, c, r, 0)
			run(fixture.ImplicitError("SELECT ext_addon.unaccent('Hôtel')", fixture.Diagnostic{Code: "3F000", Message: `schema "ext_addon" does not exist`, Position: "8"}))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			currentMetadata, err := held.ColumnTypes()
			if err != nil || len(currentMetadata) != len(metadata) || currentMetadata[0].DatabaseTypeName() != metadata[0].DatabaseTypeName() {
				t.Fatal("replacement changed retained metadata", err)
			}
			if !held.Next() {
				t.Fatal("held row lost", held.Err())
			}
			var value string
			if err := held.Scan(&value); err != nil || value != "Hotel" {
				t.Fatalf("held row %q %v", value, err)
			}
			held.Close()
			run(check)
			extensionInventory(t, p, c)
			for _, script := range []string{"CREATE EXTENSION unaccent VERSION 'missing';", "CREATE EXTENSION unaccent; CREATE EXTENSION unaccent;"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid restore accepted", script)
				}
				run(check)
			}
			extensionControlsFailures(t, p, c, r, dump.String(), check)
		}
		run(fixture.Prepared(check))
		extensionInventory(t, p, c)
		extensionNamedOwnerControls(t, p, c, r, pg)
	})
}

func TestReferenceExtensionFeatureSmoke(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent VERSION '1.1'", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT unaccent($1)", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}, "Hôtel")
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		extensionInventory(t, p, c)
	})
}

// This is explicitly PostgreSQL-only setup: routine/text-search/schema-GRANT DDL
// remains outside the native extension surface. Native real-descriptor units
// supply the corresponding early/late collision and namespace consumer proof.
func TestReferenceExtensionOracleSetup(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		if !pg {
			return
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		extensionHelperCollisionOracle(t, p, c, r)
		var ns int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&ns); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ setup, cleanup, code, message, detail, table, constraint string }{
			{"CREATE FUNCTION public.unaccent(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'", "DROP FUNCTION public.unaccent(text)", "42723", `function "unaccent" already exists with same argument types`, "", "", ""},
			{"CREATE TEXT SEARCH TEMPLATE public.unaccent (INIT=pg_catalog.dsimple_init, LEXIZE=pg_catalog.dsimple_lexize)", "DROP TEXT SEARCH TEMPLATE public.unaccent", "23505", `duplicate key value violates unique constraint "pg_ts_template_tmplname_index"`, fmt.Sprintf("Key (tmplname, tmplnamespace)=(unaccent, %d) already exists.", ns), "pg_ts_template", "pg_ts_template_tmplname_index"},
			{"CREATE TEXT SEARCH DICTIONARY public.unaccent (TEMPLATE=pg_catalog.simple)", "DROP TEXT SEARCH DICTIONARY public.unaccent", "23505", `duplicate key value violates unique constraint "pg_ts_dict_dictname_index"`, fmt.Sprintf("Key (dictname, dictnamespace)=(unaccent, %d) already exists.", ns), "pg_ts_dict", "pg_ts_dict_dictname_index"},
		} {
			run(fixture.Exec(tc.setup, 0))
			originalQuery := "SELECT oid FROM pg_proc WHERE proname='unaccent' AND pronamespace=$1"
			if tc.table == "pg_ts_template" {
				originalQuery = "SELECT oid FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace=$1"
			}
			if tc.table == "pg_ts_dict" {
				originalQuery = "SELECT oid FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace=$1"
			}
			var original int64
			if err := c.QueryRowContext(p.Ctx, originalQuery, ns).Scan(&original); err != nil {
				t.Fatal(err)
			}
			_, err := c.ExecContext(p.Ctx, "CREATE EXTENSION unaccent")
			if err == nil {
				t.Fatal("collision succeeded")
			}
			contexts := map[string]string{
				"42723":          "SQL statement \"CREATE FUNCTION unaccent(text)\n\tRETURNS text\n\tAS '$libdir/unaccent', 'unaccent_dict'\n\tLANGUAGE C STABLE STRICT PARALLEL SAFE\"\nextension script file \"unaccent--1.1.sql\", near line 11",
				"pg_ts_template": "SQL statement \"CREATE TEXT SEARCH TEMPLATE unaccent (\n\tINIT = unaccent_init,\n\tLEXIZE = unaccent_lexize\n)\"\nextension script file \"unaccent--1.1.sql\", near line 26",
				"pg_ts_dict":     "SQL statement \"CREATE TEXT SEARCH DICTIONARY unaccent (\n\tTEMPLATE = unaccent,\n\tRULES    = 'unaccent'\n)\"\nextension script file \"unaccent--1.1.sql\", near line 31",
			}
			key, schema := tc.table, "pg_catalog"
			if key == "" {
				key, schema = tc.code, ""
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Context: contexts[key], Schema: schema, Table: tc.table, Constraint: tc.constraint}
			fixture.AssertDiagnostic(t, err, &want)
			extensionOracleCollisionInventory(t, p, c, tc.table)
			var retained int64
			if err := c.QueryRowContext(p.Ctx, originalQuery, ns).Scan(&retained); err != nil || retained != original {
				t.Fatal("loose collision identity changed", original, retained, err)
			}
			extensionCount(t, p, c, r, 0)
			run(fixture.Exec(tc.cleanup, 0))
		}
		run(fixture.Exec("CREATE SCHEMA ext_locked", 0))
		run(fixture.Exec("CREATE USER ext_installer", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(`GRANT CREATE ON DATABASE "`+strings.ReplaceAll(database, `"`, `""`)+`" TO ext_installer`, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_locked", 0))
		run(fixture.ImplicitError("SELECT ext_locked.unaccent('Hôtel')", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema ext_locked", Position: "8"}))
		run(fixture.Exec("SET search_path TO ext_locked", 0))
		run(extensionMissing("SELECT unaccent('Hôtel')", "unaccent", "unknown"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA ext_locked TO ext_installer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		run(fixture.Select("SELECT ext_locked.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		extensionInventory(t, p, c)
		var signatures string
		rows, err := c.QueryContext(p.Ctx, "SELECT proname,proargtypes::text FROM pg_proc WHERE pronamespace=(SELECT extnamespace FROM pg_extension WHERE extname='unaccent') ORDER BY proname,pronargs")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, args string
			if err := rows.Scan(&name, &args); err != nil {
				t.Fatal(err)
			}
			signatures += name + "(" + args + ");"
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if signatures != "unaccent(25);unaccent(3769 25);unaccent_init(2281);unaccent_lexize(2281 2281 2281 2281);" {
			t.Fatal("provider signatures", signatures)
		}
		var init, lexize, templateInit, templateLexize int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE proname='unaccent_init' AND pronamespace=(SELECT extnamespace FROM pg_extension WHERE extname='unaccent')").Scan(&init); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE proname='unaccent_lexize' AND pronamespace=(SELECT extnamespace FROM pg_extension WHERE extname='unaccent')").Scan(&lexize); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT tmplinit::oid,tmpllexize::oid FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace=(SELECT extnamespace FROM pg_extension WHERE extname='unaccent')").Scan(&templateInit, &templateLexize); err != nil || init != templateInit || lexize != templateLexize {
			t.Fatal("actual template helper links", init, lexize, templateInit, templateLexize, err)
		}
		var bootstrap string
		if err := c.QueryRowContext(p.Ctx, "SELECT rolname FROM pg_roles WHERE oid=10").Scan(&bootstrap); err != nil {
			t.Fatal(err)
		}
		extensionNamedOwners(t, p.Ctx, c, "ext_installer", bootstrap)
	})
}
