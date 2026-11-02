package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

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
	dictionaryExtensionControls(t, false)
}

func TestReferenceUnaccentDictionaryControls(t *testing.T) {
	dictionaryExtensionControls(t, true)
}

func dictionaryExtensionControls(t *testing.T, binary bool) {
	t.Helper()
	modes := []string{"options_only", "relabeled", "seed_cycle"}
	if binary {
		modes = append(modes, "detached")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			var emitted []byte
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if mode == "seed_cycle" && b.PG {
					// The initialized dictionary is cluster-global, unlike public
					// scenario objects. Restore it even if a proof assertion fails.
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
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
				function := templateSchema + ".unaccent"
				if binary && mode == "relabeled" {
					for _, q := range []string{
						"ALTER TEXT SEARCH TEMPLATE public.unaccent SET SCHEMA dictionary_right",
						"ALTER FUNCTION public.unaccent(regdictionary,text) RENAME TO normalize_selected",
						"ALTER FUNCTION public.normalize_selected(regdictionary,text) SET SCHEMA dictionary_right",
						"ALTER FUNCTION public.unaccent_init(internal) RENAME TO dictionary_init",
						"ALTER FUNCTION public.dictionary_init(internal) SET SCHEMA dictionary_right",
						"ALTER FUNCTION public.unaccent_lexize(internal,internal,internal,internal) RENAME TO dictionary_lexize",
						"ALTER FUNCTION public.dictionary_lexize(internal,internal,internal,internal) SET SCHEMA dictionary_right",
						"CREATE TEXT SEARCH DICTIONARY public.unaccent (TEMPLATE=pg_catalog.simple,accept=false)",
					} {
						regtsExec(t, p, b, c, q, 0)
					}
					templateSchema, function = "dictionary_right", "dictionary_right.normalize_selected"
				}
				if binary && mode == "detached" {
					regtsExec(t, p, b, c, "ALTER EXTENSION unaccent DROP FUNCTION public.unaccent(regdictionary,text)", 0)
				}
				dictionaryRejectMissingRules(t, p, b, c, qualified)
				for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY " + qualified + " (rules=unaccent)", "ALTER TEXT SEARCH DICTIONARY " + qualified + " OWNER TO dictionary_recipient", "CREATE TABLE dictionary_member_rows(id int,d regdictionary DEFAULT '" + qualified + "'::regdictionary)", "CREATE VIEW dictionary_member_view AS SELECT '" + qualified + "'::regdictionary AS d"} {
					regtsExec(t, p, b, c, q, 0)
				}
				regtsExec(t, p, b, c, "INSERT INTO dictionary_member_rows(id) VALUES(1)", 1)
				if binary {
					for _, q := range []string{
						"ALTER TABLE dictionary_member_rows ADD COLUMN v text DEFAULT " + function + "('" + qualified + "'::regdictionary,'Hôtel')",
						"ALTER TABLE dictionary_member_rows ADD CONSTRAINT unaccent_binary_check CHECK (" + function + "(d,'Hôtel')='Hotel' AND v='Hotel')",
						"CREATE VIEW unaccent_binary_view AS SELECT '" + qualified + "'::regdictionary AS d," + function + "('" + qualified + "'::regdictionary,'Hôtel') AS v",
						"CREATE TEXT SEARCH CONFIGURATION unaccent_binary_config (COPY=pg_catalog.simple)",
						"ALTER TEXT SEARCH CONFIGURATION unaccent_binary_config ALTER MAPPING FOR word WITH " + qualified + ",pg_catalog.simple",
					} {
						regtsExec(t, p, b, c, q, 0)
					}
				}
				if binary && mode == "relabeled" {
					// Capture first, then relabel and replace the original name.
					// Stored consumers must keep the original OID and routine.
					for _, q := range []string{
						"ALTER TEXT SEARCH DICTIONARY dictionary_right.dictionary_member RENAME TO dictionary_saved_member",
						"CREATE TEXT SEARCH DICTIONARY dictionary_right.dictionary_member (TEMPLATE=pg_catalog.simple,accept=false)",
						"ALTER FUNCTION dictionary_right.normalize_selected(regdictionary,text) RENAME TO normalize_saved",
					} {
						regtsExec(t, p, b, c, q, 0)
					}
					name, qualified, function = "dictionary_saved_member", "dictionary_right.dictionary_saved_member", "dictionary_right.normalize_saved"
				}
				check := func() {
					dictionaryExtensionState(t, p, b, c, schema, name, templateSchema, "dictionary_recipient", "rules = 'unaccent'")
					regtsSelect(t, p, b, c, "SELECT d=$1::regdictionary AS same FROM dictionary_member_rows", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, qualified)
					regtsSelect(t, p, b, c, "SELECT d=$1::regdictionary AS same FROM dictionary_member_view", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, qualified)
					regtsExec(t, p, b, c, "INSERT INTO dictionary_member_rows(id) VALUES(2)", 1)
					regtsSelect(t, p, b, c, "SELECT d=$1::regdictionary AS same FROM dictionary_member_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, qualified)
					regtsExec(t, p, b, c, "DELETE FROM dictionary_member_rows WHERE id=2", 1)
					if binary {
						regtsSelect(t, p, b, c, "SELECT v,"+function+"(d,'Hôtel') AS selected FROM dictionary_member_rows ORDER BY id", []string{"v", "selected"}, []string{"TEXT", "TEXT"}, [][]any{{"Hotel", "Hotel"}})
						rejected := fixture.CheckViolation("dictionary_member_rows", "unaccent_binary_check", "99, null, bad")
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("INSERT INTO dictionary_member_rows(id,d,v) VALUES(99,NULL,'bad')", rejected))
						regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM dictionary_member_rows", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
						regtsSelect(t, p, b, c, "SELECT d=$1::regdictionary AS same,v FROM unaccent_binary_view", []string{"same", "v"}, []string{"BOOL", "TEXT"}, [][]any{{true, "Hotel"}}, qualified)
						vector := "'hotel':1"
						if mode == "seed_cycle" {
							// The cycle renamed the unaccent filter to pg_catalog.simple,
							// so this chain has no final recognizing dictionary.
							vector = ""
						}
						regtsSelect(t, p, b, c, "SELECT to_tsvector('unaccent_binary_config','Hôtel') AS v", []string{"v"}, []string{"TSVECTOR"}, [][]any{{vector}})
						regtsSelect(t, p, b, c, "SELECT mapdict=$1::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='unaccent_binary_config'::regconfig AND maptokentype=2 AND mapseqno=1", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, qualified)
						members := int64(6)
						if mode == "detached" {
							members = 5
						}
						regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend dep JOIN pg_extension e ON e.oid=dep.refobjid WHERE dep.refclassid=3079 AND dep.deptype='e' AND e.extname='unaccent'", []string{"n"}, []string{"INT8"}, [][]any{{members}})
						if mode == "relabeled" {
							unaccentDictionaryText(t, p, b, c, "SELECT "+function+"('public.unaccent'::regdictionary,'Hôtel') AS v", "Hôtel")
							unaccentDictionaryText(t, p, b, c, "SELECT "+function+"('dictionary_right.dictionary_member'::regdictionary,'Hôtel') AS v", "Hôtel")
							regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend dep JOIN pg_attrdef a ON dep.classid=2604 AND dep.objid=a.oid JOIN pg_attribute att ON att.attrelid=a.adrelid AND att.attnum=a.adnum WHERE a.adrelid='dictionary_member_rows'::regclass AND att.attname='v' AND dep.deptype='n' AND ((dep.refclassid=3600 AND dep.refobjid=$1::regdictionary::oid) OR (dep.refclassid=1255 AND dep.refobjid=$2::regprocedure::oid))", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}, qualified, function+"(regdictionary,text)")
						}
					}
					if mode == "seed_cycle" {
						dictionaryResult(t, p, b, c, "SELECT ts_lexize('pg_catalog.unaccent','The') AS lexemes", "{}")
						regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,dictinitoption FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace='pg_catalog'::regnamespace", []string{"id", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{int64(3765), "stopwords = 'english'"}})
					}
				}
				cleanup := func() {
					if binary {
						regtsExec(t, p, b, c, "DROP VIEW unaccent_binary_view", 0)
						regtsExec(t, p, b, c, "DROP TEXT SEARCH CONFIGURATION unaccent_binary_config", 0)
						if mode == "relabeled" {
							regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY public.unaccent", 0)
							regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_right.dictionary_member", 0)
						}
					}
					regtsExec(t, p, b, c, "DROP VIEW dictionary_member_view", 0)
					regtsExec(t, p, b, c, "DROP TABLE dictionary_member_rows", 0)
					regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
					if binary && mode == "detached" {
						regtsExec(t, p, b, c, "DROP FUNCTION public.unaccent(regdictionary,text)", 0)
					}
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
				if binary {
					regtsExec(t, p, b, c, "BEGIN", 0)
					regtsExec(t, p, b, c, "INSERT INTO dictionary_member_rows(id) VALUES(77)", 1)
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				emitted = bytes.Clone(dump.Bytes())
				if binary {
					regtsExec(t, p, b, c, "ROLLBACK", 0)
					check()
				}
				owner := []byte("ALTER TEXT SEARCH DICTIONARY \"" + schema + "\".\"" + name + "\" OWNER TO \"dictionary_recipient\";")
				installation, ownership, consumer := bytes.Index(emitted, []byte("CREATE EXTENSION \"unaccent\"")), bytes.Index(emitted, owner), bytes.Index(emitted, []byte("CREATE TABLE \"public\".\"dictionary_member_rows\""))
				standalone := bytes.Contains(emitted, []byte("CREATE TEXT SEARCH DICTIONARY"))
				if bytes.Count(emitted, []byte("CREATE EXTENSION \"unaccent\"")) != 1 || standalone != (binary && mode == "relabeled") || installation < 0 || ownership <= installation || consumer <= ownership {
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
				var binaryRows *sql.Rows
				var binaryTypes []*sql.ColumnType
				if binary {
					binaryRows, err = c.QueryContext(p.Ctx, "SELECT "+function+"(d,'Hôtel') AS v FROM dictionary_member_rows ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer binaryRows.Close()
					binaryTypes, err = binaryRows.ColumnTypes()
					if err != nil || len(binaryTypes) != 1 || binaryTypes[0].DatabaseTypeName() != "TEXT" {
						t.Fatal("retained binary metadata", binaryTypes, err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
						t.Fatal("same-engine binary roundtrip", err)
					}
					check()
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
				if binary {
					columns, err := binaryRows.Columns()
					if err != nil || len(columns) != 1 || columns[0] != "v" || binaryTypes[0].DatabaseTypeName() != "TEXT" || !binaryRows.Next() {
						t.Fatal("retained binary columns/rows", columns, err)
					}
					var value string
					if err := binaryRows.Scan(&value); err != nil || value != "Hotel" {
						t.Fatal("retained binary value", value, err)
					}
					if binaryRows.Next() || binaryRows.Err() != nil {
						t.Fatal(binaryRows.Err())
					}
					binaryRows.Close()
				}
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(emitted, redump.Bytes()) {
					t.Fatal("extension dictionary redump changed", err)
				}
				var failureRows *sql.Rows
				if binary {
					regtsExec(t, p, b, c, "CREATE TABLE unaccent_control_sentinel(v int)", 0)
					regtsExec(t, p, b, c, "INSERT INTO unaccent_control_sentinel VALUES(42)", 1)
					failureRows, err = c.QueryContext(p.Ctx, "SELECT "+function+"(d,'Hôtel') AS v FROM dictionary_member_rows ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer failureRows.Close()
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
				if binary {
					preserved := func() {
						check()
						checkCollision()
						regtsSelect(t, p, b, c, "SELECT v FROM unaccent_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
					}
					for _, suffix := range []string{
						"SELECT (",
						"ALTER TEXT SEARCH DICTIONARY " + qualified + " OWNER TO unaccent_missing_owner;",
						"ALTER TEXT SEARCH DICTIONARY " + qualified + " SET SCHEMA unaccent_missing_schema;",
						"ALTER TEXT SEARCH DICTIONARY " + qualified + " (rules=unaccent,bogus=1);",
						"SET ROLE dictionary_recipient; ALTER TEXT SEARCH DICTIONARY " + qualified + " SET SCHEMA pg_catalog;",
						"SET ROLE dictionary_recipient; ALTER TEXT SEARCH DICTIONARY " + qualified + " OWNER TO postgres;",
					} {
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(string(emitted)+suffix)); err == nil {
							t.Fatal("failed binary Restore published", suffix)
						}
						preserved()
					}
					reading, stopReading := context.WithCancel(p.Ctx)
					interrupted := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stopReading}
					err := stuntgres.Restore(reading, p.Native, interrupted)
					stopReading()
					if !errors.Is(err, context.Canceled) {
						t.Fatal("binary Restore reader cancellation", err)
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
					preserved()
					fixture.LexicalRows(t, false, failureRows, fixture.Select("", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}), nil)
				}
				cleanup()
			})
		})
	}
}
