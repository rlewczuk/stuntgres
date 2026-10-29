package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func dictionaryExtensionState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema, name, templateSchema, owner string, options any) {
	t.Helper()
	qualified := schema + "." + name
	dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::regdictionary,'Hôtel') AS lexemes", "{Hotel}", qualified)
	dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::regdictionary,'plain') AS lexemes", nil, qualified)
	regtsSelect(t, p, b, c, "SELECT d.dictname,n.nspname,d.dictinitoption,r.rolname,t.tmplname,tn.nspname AS template_schema FROM pg_ts_dict d JOIN pg_namespace n ON n.oid=d.dictnamespace JOIN pg_roles r ON r.oid=d.dictowner JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_namespace tn ON tn.oid=t.tmplnamespace WHERE d.oid=$1::regdictionary::oid", []string{"dictname", "nspname", "dictinitoption", "rolname", "tmplname", "template_schema"}, []string{"NAME", "NAME", "TEXT", "NAME", "NAME", "NAME"}, [][]any{{name, schema, options, owner, "unaccent", templateSchema}}, qualified)
	regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend dep JOIN pg_extension e ON e.oid=dep.refobjid WHERE dep.classid=3600 AND dep.objid=$1::regdictionary::oid AND dep.refclassid=3079 AND dep.deptype='e' AND e.extname='unaccent'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, qualified)
	regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend dep JOIN pg_ts_dict d ON d.oid=dep.objid WHERE dep.classid=3600 AND d.oid=$1::regdictionary::oid AND dep.refclassid=3764 AND dep.refobjid=d.dicttemplate AND dep.deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, qualified)
}

func TestReferenceDictionaryExtensionIdentity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE EXTENSION unaccent", "CREATE SCHEMA dictionary_right", "CREATE SCHEMA dictionary_owned", "CREATE ROLE dictionary_peer", "GRANT USAGE ON SCHEMA dictionary_right TO dictionary_peer"} {
			regtsExec(t, p, b, c, q, 0)
		}
		defer regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		var dictionaryOID, templateOID, extensionOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT d.oid::bigint,d.dicttemplate::bigint FROM pg_ts_dict d WHERE d.oid='public.unaccent'::regdictionary::oid").Scan(&dictionaryOID, &templateOID); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_extension WHERE extname='unaccent'").Scan(&extensionOID); err != nil {
			t.Fatal(err)
		}
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{Hotel}", "public.unaccent", "Hôtel")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.unaccent RENAME TO dictionary_member", 0)
		dictionaryResult(t, p, b, held, q, "{Hotel}", "public.dictionary_member", "Hôtel")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_member SET SCHEMA dictionary_right", 0)
		dictionaryResult(t, p, b, held, q, "{Hotel}", "dictionary_right.dictionary_member", "Hôtel")
		dictionaryResult(t, p, b, held, q, nil, "dictionary_right.dictionary_member", "plain")
		dictionaryExtensionState(t, p, b, c, "dictionary_right", "dictionary_member", "public", owner, "rules = 'unaccent'")
		checkIdentity := func() {
			regtsSelect(t, p, b, c, "SELECT d.oid::bigint AS dictionary_id,d.dicttemplate::bigint AS template_id,dep.refobjid::bigint AS extension_id FROM pg_ts_dict d JOIN pg_depend dep ON dep.objid=d.oid WHERE d.oid='dictionary_right.dictionary_member'::regdictionary::oid AND dep.classid=3600 AND dep.refclassid=3079 AND dep.deptype='e'", []string{"dictionary_id", "template_id", "extension_id"}, []string{"INT8", "INT8", "INT8"}, [][]any{{dictionaryOID, templateOID, extensionOID}})
		}
		checkIdentity()
		for _, sql := range []string{"CREATE TEXT SEARCH DICTIONARY dictionary_right.dictionary_conflict (TEMPLATE=simple)", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.dictionary_member (TEMPLATE=simple)"} {
			regtsExec(t, p, b, c, sql, 0)
		}
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.dictionary_member RENAME TO dictionary_conflict", "42710", `text search dictionary "dictionary_conflict" already exists in schema "dictionary_right"`)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.dictionary_member SET SCHEMA dictionary_owned", "42710", `text search dictionary "dictionary_member" already exists in schema "dictionary_owned"`)
		regtsExec(t, p, b, c, "SET ROLE dictionary_peer", 0)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.dictionary_member RENAME TO denied", "42501", "must be owner of text search dictionary dictionary_member")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.dictionary_member (rules)", "42501", "must be owner of text search dictionary dictionary_right.dictionary_member")
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		dictionaryExtensionState(t, p, b, c, "dictionary_right", "dictionary_member", "public", owner, "rules = 'unaccent'")
		checkIdentity()
	})
}

func TestReferenceDictionaryExtensionControls(t *testing.T) {
	for _, mode := range []string{"options_only", "relabeled", "seed_cycle"} {
		t.Run(mode, func(t *testing.T) {
			var emitted []byte
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if mode == "seed_cycle" && b.PG {
					// The initialized dictionary is cluster-global, unlike public
					// scenario objects. Restore it even if a proof assertion fails.
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						for _, q := range []string{"SET SESSION AUTHORIZATION DEFAULT", "RESET ROLE", "DROP EXTENSION IF EXISTS unaccent CASCADE"} {
							if _, err := c.ExecContext(ctx, q); err != nil {
								t.Errorf("dictionary cycle cleanup %s: %v", q, err)
							}
						}
						var current string
						if err := c.QueryRowContext(ctx, "SELECT dictname FROM pg_ts_dict WHERE oid=3765").Scan(&current); err != nil {
							t.Error("dictionary cycle seed cleanup", err)
							return
						}
						if current != "simple" {
							if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog."+fixture.RegtypeIdentifier(current)+" RENAME TO simple"); err != nil {
								t.Error("dictionary cycle name cleanup", err)
								return
							}
						}
						if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.simple (stopwords)"); err != nil {
							t.Error("dictionary cycle options cleanup", err)
						}
					}()
				}
				schema, name, templateSchema := "public", "unaccent", "public"
				install := "CREATE EXTENSION unaccent"
				if mode == "seed_cycle" {
					schema, name, templateSchema = "pg_catalog", "simple", "pg_catalog"
					install += " WITH SCHEMA pg_catalog"
				}
				regtsExec(t, p, b, c, "CREATE ROLE dictionary_recipient", 0)
				regtsExec(t, p, b, c, install, 0)
				if mode == "relabeled" {
					schema, name = "dictionary_right", "dictionary_member"
					for _, q := range []string{"CREATE SCHEMA dictionary_right", "ALTER TEXT SEARCH DICTIONARY public.unaccent RENAME TO dictionary_member", "ALTER TEXT SEARCH DICTIONARY public.dictionary_member SET SCHEMA dictionary_right"} {
						regtsExec(t, p, b, c, q, 0)
					}
				}
				if mode == "seed_cycle" {
					for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY pg_catalog.simple RENAME TO dictionary_cycle_temp", "ALTER TEXT SEARCH DICTIONARY pg_catalog.unaccent RENAME TO simple", "ALTER TEXT SEARCH DICTIONARY pg_catalog.dictionary_cycle_temp RENAME TO unaccent", "ALTER TEXT SEARCH DICTIONARY pg_catalog.unaccent (stopwords=english)"} {
						regtsExec(t, p, b, c, q, 0)
					}
				}
				qualified := schema + "." + name
				dictionaryRejectMissingRules(t, p, b, c, qualified)
				for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY " + qualified + " (rules=unaccent)", "ALTER TEXT SEARCH DICTIONARY " + qualified + " OWNER TO dictionary_recipient", "CREATE TABLE dictionary_member_rows(id int,d regdictionary DEFAULT '" + qualified + "'::regdictionary)", "CREATE VIEW dictionary_member_view AS SELECT '" + qualified + "'::regdictionary AS d"} {
					regtsExec(t, p, b, c, q, 0)
				}
				regtsExec(t, p, b, c, "INSERT INTO dictionary_member_rows(id) VALUES(1)", 1)
				check := func() {
					dictionaryExtensionState(t, p, b, c, schema, name, templateSchema, "dictionary_recipient", "rules = 'unaccent'")
					regtsSelect(t, p, b, c, "SELECT d=$1::regdictionary AS same FROM dictionary_member_rows", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, qualified)
					regtsSelect(t, p, b, c, "SELECT d=$1::regdictionary AS same FROM dictionary_member_view", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, qualified)
					regtsExec(t, p, b, c, "INSERT INTO dictionary_member_rows(id) VALUES(2)", 1)
					regtsSelect(t, p, b, c, "SELECT d=$1::regdictionary AS same FROM dictionary_member_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, qualified)
					regtsExec(t, p, b, c, "DELETE FROM dictionary_member_rows WHERE id=2", 1)
					if mode == "seed_cycle" {
						dictionaryResult(t, p, b, c, "SELECT ts_lexize('pg_catalog.unaccent','The') AS lexemes", "{}")
						regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,dictinitoption FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace='pg_catalog'::regnamespace", []string{"id", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{int64(3765), "stopwords = 'english'"}})
					}
				}
				cleanup := func() {
					regtsExec(t, p, b, c, "DROP VIEW dictionary_member_view", 0)
					regtsExec(t, p, b, c, "DROP TABLE dictionary_member_rows", 0)
					regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
					if mode == "seed_cycle" {
						regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.unaccent RENAME TO simple", 0)
						regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.simple (stopwords)", 0)
					}
					if mode == "relabeled" {
						regtsExec(t, p, b, c, "DROP SCHEMA dictionary_right", 0)
					}
				}
				check()
				if b.PG {
					if len(emitted) == 0 {
						t.Fatal("native emitted SQL is required")
					}
					cleanup()
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("replay actual extension dictionary SQL", err)
					}
					check()
					cleanup()
					return
				}
				var sourceOID int64
				if err := c.QueryRowContext(p.Ctx, "SELECT $1::regdictionary::oid::bigint", qualified).Scan(&sourceOID); err != nil {
					t.Fatal(err)
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				emitted = bytes.Clone(dump.Bytes())
				owner := []byte("ALTER TEXT SEARCH DICTIONARY \"" + schema + "\".\"" + name + "\" OWNER TO \"dictionary_recipient\";")
				installation, ownership, consumer := bytes.Index(emitted, []byte("CREATE EXTENSION \"unaccent\"")), bytes.Index(emitted, owner), bytes.Index(emitted, []byte("CREATE TABLE \"public\".\"dictionary_member_rows\""))
				if bytes.Count(emitted, []byte("CREATE EXTENSION \"unaccent\"")) != 1 || bytes.Contains(emitted, []byte("CREATE TEXT SEARCH DICTIONARY")) || installation < 0 || ownership <= installation || consumer <= ownership {
					t.Fatal("member installation/owner/consumer ordering", string(emitted))
				}
				if mode == "seed_cycle" {
					if !bytes.Contains(emitted, []byte(`CREATE EXTENSION "unaccent" WITH SCHEMA "pg_catalog"`)) ||
						bytes.Contains(emitted, []byte(`CREATE SCHEMA "pg_catalog"`)) ||
						bytes.Contains(emitted, []byte(`DROP SCHEMA "pg_catalog"`)) ||
						bytes.Contains(emitted, []byte(`SET SCHEMA "pg_catalog"`)) {
						t.Fatal("seed cycle must use genuine installation in the existing system namespace", string(emitted))
					}
				}
				old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize($1::regdictionary,'Hôtel') AS lexemes", qualified)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				oldTypes, err := old.ColumnTypes()
				if err != nil || len(oldTypes) != 1 || oldTypes[0].DatabaseTypeName() != "_TEXT" {
					t.Fatal("retained member metadata", oldTypes, err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('pg_catalog.simple','The') AS lexemes", "{the}")
				// Reset preserves the source's allocation frontier. Use an
				// independent native target so the imported OID is still
				// reachable by ordinary temporary dictionary creation.
				// The source's old Rows above remain owned by the source.
				target := fixture.NewFixture(t, false, true)
				targetConn, err := target.Memory.Conn(target.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer targetConn.Close()
				targetBackend := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
				regtsExec(t, target, targetBackend, targetConn, "CREATE ROLE dictionary_recipient", 0)
				// The consumer and cleanup closures now operate on the target;
				// emitted SQL and old Rows still come from the original source.
				p, b, c = target, targetBackend, targetConn
				checkCollision := dictionaryTemporaryCollision(t, p, b, c, sourceOID)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				var targetOID int64
				if err := c.QueryRowContext(p.Ctx, "SELECT $1::regdictionary::oid::bigint", qualified).Scan(&targetOID); err != nil || targetOID == sourceOID {
					t.Fatal("extension dictionary member remap", sourceOID, targetOID, err)
				}
				checkCollision()
				check()
				if !old.Next() {
					t.Fatal("retained member lexeme missing", old.Err())
				}
				var oldValue any
				if err := old.Scan(&oldValue); err != nil || oldValue != "{Hotel}" {
					t.Fatal("retained member lexeme", oldValue, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("retained member rows", old.Err())
				}
				old.Close()
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(emitted, redump.Bytes()) {
					t.Fatal("extension dictionary redump changed", err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(string(emitted)+"ALTER TEXT SEARCH DICTIONARY "+qualified+" (rules=missing_dictionary);")); err == nil {
					t.Fatal("invalid extension dictionary restore published")
				}
				check()
				checkCollision()
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
					t.Fatal("cancelled extension dictionary restore", err)
				}
				check()
				checkCollision()
				cleanup()
			})
		})
	}
}
