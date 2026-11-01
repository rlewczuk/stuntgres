package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type dictIntControlNames struct {
	member, template, init, lexize string
	options                        any
	members                        int64
	lexeme                         string
	helperOwner                    string
}

const dictIntOwnerDeniedSQL = "ALTER TEXT SEARCH DICTIONARY public.dict_int_custom (maxlen=2)"
const dictIntSchemaDeniedSQL = "ALTER TEXT SEARCH CONFIGURATION dict_int_addon.dict_int_guard DROP MAPPING FOR int"

func dictIntControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mutated bool) dictIntControlNames {
	t.Helper()
	// A superuser installer owns its script objects; trusted installation by
	// an ordinary role uses bootstrap ownership. Replay later applies the
	// exact native dump's named OWNER directives independently of this role.
	var installer string
	var superuser bool
	if err := c.QueryRowContext(p.Ctx, "SELECT rolname,rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&installer, &superuser); err != nil {
		t.Fatal("installation principal", err)
	}
	helperOwner := "postgres"
	if superuser {
		helperOwner = installer
	}
	n := dictIntControlNames{"public.intdict", "public.intdict_template", "public.dintdict_init", "public.dintdict_lexize", nil, 4, "{-12345}", helperOwner}
	for _, q := range []string{"CREATE ROLE dict_int_owner", "CREATE ROLE dict_int_denied", "GRANT USAGE ON SCHEMA public TO dict_int_denied", "CREATE EXTENSION dict_int", "COMMENT ON EXTENSION dict_int IS 'integer controls'"} {
		dictIntExec(t, p, b, c, q, 0)
	}
	if mutated {
		for _, q := range []string{
			"CREATE SCHEMA dict_int_moved",
			"ALTER FUNCTION public.dintdict_init(internal) RENAME TO dict_int_init_moved",
			"ALTER FUNCTION public.dict_int_init_moved(internal) SET SCHEMA dict_int_moved",
			"ALTER FUNCTION public.dintdict_lexize(internal,internal,internal,internal) RENAME TO dict_int_lexize_moved",
			"ALTER FUNCTION public.dict_int_lexize_moved(internal,internal,internal,internal) SET SCHEMA dict_int_moved",
			"ALTER TEXT SEARCH TEMPLATE public.intdict_template RENAME TO dict_int_template_moved",
			"ALTER TEXT SEARCH TEMPLATE public.dict_int_template_moved SET SCHEMA dict_int_moved",
			"ALTER TEXT SEARCH DICTIONARY public.intdict RENAME TO dict_int_member_moved",
			"ALTER TEXT SEARCH DICTIONARY public.dict_int_member_moved SET SCHEMA dict_int_moved",
			"ALTER TEXT SEARCH DICTIONARY dict_int_moved.dict_int_member_moved (maxlen=3,absval=true)",
			"ALTER TEXT SEARCH DICTIONARY dict_int_moved.dict_int_member_moved OWNER TO dict_int_owner",
			"ALTER FUNCTION dict_int_moved.dict_int_init_moved(internal) OWNER TO dict_int_owner",
			"ALTER FUNCTION dict_int_moved.dict_int_lexize_moved(internal,internal,internal,internal) OWNER TO dict_int_owner",
			"ALTER EXTENSION dict_int DROP TEXT SEARCH DICTIONARY dict_int_moved.dict_int_member_moved",
		} {
			dictIntExec(t, p, b, c, q, 0)
		}
		n = dictIntControlNames{"dict_int_moved.dict_int_member_moved", "dict_int_moved.dict_int_template_moved", "dict_int_moved.dict_int_init_moved", "dict_int_moved.dict_int_lexize_moved", "maxlen = 3, absval = 'true'", 3, "{123}", "dict_int_owner"}
	}
	for _, q := range []string{
		"CREATE TEXT SEARCH DICTIONARY public.dict_int_custom (TEMPLATE=" + n.template + ",maxlen=3,absval=true,rejectlong=false)",
		"ALTER TEXT SEARCH DICTIONARY public.dict_int_custom OWNER TO dict_int_owner",
		"CREATE TEXT SEARCH CONFIGURATION public.dict_int_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION public.dict_int_cfg ALTER MAPPING FOR int,uint WITH public.dict_int_custom",
		"ALTER TEXT SEARCH CONFIGURATION public.dict_int_cfg OWNER TO dict_int_owner",
		"CREATE SCHEMA dict_int_addon AUTHORIZATION dict_int_owner",
		"REVOKE ALL ON SCHEMA dict_int_addon FROM PUBLIC",
		"CREATE TEXT SEARCH CONFIGURATION dict_int_addon.dict_int_guard (COPY=public.dict_int_cfg)",
		"ALTER TEXT SEARCH CONFIGURATION dict_int_addon.dict_int_guard OWNER TO dict_int_owner",
		"CREATE TABLE public.dict_int_vectors(id int,v tsvector,d regdictionary DEFAULT 'public.dict_int_custom'::regdictionary,m regdictionary DEFAULT '" + n.member + "'::regdictionary)",
		"CREATE VIEW public.dict_int_saved AS SELECT 'public.dict_int_custom'::regdictionary AS d,'" + n.member + "'::regdictionary AS m",
		"CREATE INDEX dict_int_vectors_gist ON public.dict_int_vectors USING gist(v)",
	} {
		dictIntExec(t, p, b, c, q, 0)
	}
	dictIntExec(t, p, b, c, "INSERT INTO dict_int_vectors(id,v) VALUES(1,to_tsvector('dict_int_cfg','-123456')),(2,to_tsvector('dict_int_cfg','42')),(3,NULL)", 3)
	return n
}

func dictIntControlState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n dictIntControlNames, mutated bool) {
	t.Helper()
	dictIntLexemes(t, p, b, c, n.lexeme, n.member, "-123456")
	dictIntOptionText(t, p, b, c, n.member, n.options)
	dictIntLexemes(t, p, b, c, "{123}", "public.dict_int_custom", "-123456")
	dictIntOptionText(t, p, b, c, "public.dict_int_custom", "maxlen = 3, absval = 'true', rejectlong = 'false'")
	dictIntControlGraph(t, p, b, c, n)
	dictIntSelect(t, p, b, c, "SELECT d.dicttemplate=t.oid AS linked,t.tmplinit=$2::regprocedure AS init,t.tmpllexize=$3::regprocedure AS lexize FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate WHERE d.oid=$1::regdictionary::oid", []string{"linked", "init", "lexize"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, n.member, n.init+"(internal)", n.lexize+"(internal,internal,internal,internal)")
	dictIntSelect(t, p, b, c, "SELECT d.dicttemplate=m.dicttemplate AS linked,r.rolname FROM pg_ts_dict d JOIN pg_ts_dict m ON m.oid=$1::regdictionary::oid JOIN pg_roles r ON r.oid=d.dictowner WHERE d.oid='public.dict_int_custom'::regdictionary::oid", []string{"linked", "rolname"}, []string{"BOOL", "NAME"}, [][]any{{true, "dict_int_owner"}}, n.member)
	dictIntSelect(t, p, b, c, "SELECT e.extversion,n.nspname,obj_description(e.oid,'pg_extension') AS comment FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='dict_int'", []string{"extversion", "nspname", "comment"}, []string{"TEXT", "NAME", "TEXT"}, [][]any{{"1.0", "public", "integer controls"}})
	dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_extension e ON e.oid=dep.refobjid WHERE dep.refclassid=3079 AND e.extname='dict_int' AND dep.deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{n.members}})
	memberEdges := int64(1)
	if mutated {
		memberEdges = 0
		dictIntSelect(t, p, b, c, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.oid=$1::regdictionary::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{"dict_int_owner"}}, n.member)
	}
	dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE classid=3600 AND objid=$1::regdictionary::oid AND refclassid=3079 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{memberEdges}}, n.member)
	dictIntSelect(t, p, b, c, "SELECT mapcfg='dict_int_cfg'::regconfig::oid AS cfg,maptokentype,mapseqno,mapdict='dict_int_custom'::regdictionary::oid AS dictionary FROM pg_ts_config_map WHERE mapcfg='dict_int_cfg'::regconfig::oid AND (maptokentype=21 OR maptokentype=22) ORDER BY maptokentype,mapseqno", []string{"cfg", "maptokentype", "mapseqno", "dictionary"}, []string{"BOOL", "INT4", "INT4", "BOOL"}, [][]any{{true, int64(21), int64(1), true}, {true, int64(22), int64(1), true}})
	dictIntSelect(t, p, b, c, "SELECT id,v::text,d='public.dict_int_custom'::regdictionary AS dictionary,m=$1::regdictionary AS member FROM dict_int_vectors ORDER BY id", []string{"id", "v", "dictionary", "member"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "'123':1", true, true}, {int64(2), "'42':1", true, true}, {int64(3), nil, true, true}}, n.member)
	dictIntSelect(t, p, b, c, "SELECT d='public.dict_int_custom'::regdictionary AS dictionary,m=$1::regdictionary AS member FROM dict_int_saved", []string{"dictionary", "member"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, n.member)
	dictIntMatches(t, p, b, c, 1)
	dictIntSelect(t, p, b, c, "SELECT to_tsvector('dict_int_addon.dict_int_guard','-123456')::text AS vector", []string{"vector"}, []string{"TEXT"}, [][]any{{"'123':1"}})
	dictIntExec(t, p, b, c, "INSERT INTO dict_int_vectors(id) VALUES(9)", 1)
	dictIntSelect(t, p, b, c, "SELECT d='public.dict_int_custom'::regdictionary AS dictionary,m=$1::regdictionary AS member FROM dict_int_vectors WHERE id=9", []string{"dictionary", "member"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, n.member)
	dictIntExec(t, p, b, c, "DELETE FROM dict_int_vectors WHERE id=9", 1)
}

func dictIntControlCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n dictIntControlNames, mutated bool) {
	t.Helper()
	for _, q := range []string{"DROP VIEW dict_int_saved", "DROP TABLE dict_int_vectors", "DROP TEXT SEARCH CONFIGURATION dict_int_addon.dict_int_guard", "DROP TEXT SEARCH CONFIGURATION dict_int_cfg", "DROP TEXT SEARCH DICTIONARY dict_int_custom"} {
		dictIntExec(t, p, b, c, q, 0)
	}
	if mutated {
		dictIntExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY "+n.member, 0)
	}
	dictIntExec(t, p, b, c, "DROP EXTENSION dict_int CASCADE", 0)
	if mutated {
		dictIntExec(t, p, b, c, "DROP SCHEMA dict_int_moved", 0)
	}
	dictIntExec(t, p, b, c, "DROP SCHEMA dict_int_addon", 0)
}

// The destination session keeps a genuine class-3600 temporary dictionary at
// exactly the imported ordinary dictionary's source OID throughout replacement.
func dictIntCollision(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source int64) func() {
	t.Helper()
	for i := int64(0); i <= source; i++ {
		name := fmt.Sprintf("dict_int_collision_%d", i)
		qualified := "pg_temp." + name
		dictIntExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY "+qualified+" (TEMPLATE=pg_catalog.simple,accept=false)", 0)
		oid := dictIntID(t, p, c, "SELECT $1::regdictionary::oid::bigint", qualified)
		if oid > source {
			t.Fatalf("temporary dictionary overshot exact source collision: got %d want %d", oid, source)
		}
		if oid != source {
			continue
		}
		ns := dictIntID(t, p, c, "SELECT dictnamespace::bigint FROM pg_ts_dict WHERE oid=$1", source)
		var namespace string
		if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", ns).Scan(&namespace); err != nil || !strings.HasPrefix(namespace, "pg_temp_") {
			t.Fatal("actual session temporary namespace", namespace, err)
		}
		check := func() {
			dictIntSelect(t, p, b, c, "SELECT oid::bigint,dictname,dictnamespace::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1", []string{"oid", "dictname", "dictnamespace", "dicttemplate", "dictinitoption"}, []string{"INT8", "NAME", "INT8", "INT8", "TEXT"}, [][]any{{source, name, ns, int64(3727), "accept = 'false'"}}, source)
			dictIntSelect(t, p, b, c, "SELECT $1::regdictionary::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{source}}, qualified)
			dictIntLexemes(t, p, b, c, nil, qualified, "WORD")
		}
		check()
		return check
	}
	t.Fatal("exact temporary dictionary collision not reached")
	return nil
}

func TestReferenceDictIntControls(t *testing.T) {
	for _, mutated := range []bool{false, true} {
		name := "default"
		if mutated {
			name = "mutated"
		}
		t.Run(name, func(t *testing.T) {
			var emitted []byte
			dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Align source namespace allocation with the independent target
				// before assigning imported persistent dictionary identities.
				if !b.PG {
					dictIntExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.dict_int_source (TEMPLATE=pg_catalog.simple)", 0)
				}
				n := dictIntControlSetup(t, p, b, c, mutated)
				check := func() { dictIntControlState(t, p, b, c, n, mutated) }
				check()
				dictIntControlAuthorization(t, p, b, c, check)
				if b.PG {
					if len(emitted) == 0 {
						t.Fatal("exact native-emitted SQL is required for PostgreSQL replay; run after native provider installation is implemented")
					}
					dictIntControlCleanup(t, p, b, c, n, mutated)
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("replay actual native dictionary SQL", err)
					}
					if !mutated {
						// The real native stream restores named bootstrap ownership;
						// the PostgreSQL replay login is not its member owner.
						n.helperOwner = "postgres"
					}
					check()
					dictIntControlAuthorization(t, p, b, c, check)
					return
				}
				emitted = []byte(fixture.ExtensionDump(t, p.Ctx, p.Native))
				if bytes.Count(emitted, []byte(`CREATE EXTENSION "dict_int"`)) != 1 || bytes.Contains(emitted, []byte(`CREATE TEXT SEARCH TEMPLATE`)) {
					t.Fatal("installed provider must be replayed once, not as standalone callbacks/template", string(emitted))
				}
				source := dictIntID(t, p, c, "SELECT 'public.dict_int_custom'::regdictionary::oid::bigint")
				old, err := c.QueryContext(p.Ctx, dictIntLexizeSQL, "public.dict_int_custom", "-123456")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				oldTypes, err := old.ColumnTypes()
				if err != nil || len(oldTypes) != 1 || oldTypes[0].DatabaseTypeName() != "_TEXT" {
					t.Fatal("retained lexeme metadata", oldTypes, err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				check()
				dictIntControlAuthorization(t, p, b, c, check)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				dictIntSelect(t, p, b, c, "SELECT extname FROM pg_extension WHERE extname='dict_int'", []string{"extname"}, []string{"NAME"}, nil)
				dictIntSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictname='dict_int_custom'", []string{"dictname"}, []string{"NAME"}, nil)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				check()
				dictIntControlAuthorization(t, p, b, c, check)
				if got := fixture.ExtensionDump(t, p.Ctx, p.Native); got != string(emitted) {
					t.Fatal("same-target dictionary dump changed after replay")
				}
				dictIntControlFailures(t, p, b, c, emitted, check)
				fresh := fixture.NewFixture(t, false, true)
				fc, err := fresh.Memory.Conn(fresh.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer fc.Close()
				fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
				for _, q := range []string{"CREATE ROLE dict_int_owner", "CREATE ROLE dict_int_denied"} {
					dictIntExec(t, fresh, fb, fc, q, 0)
				}
				collision := dictIntCollision(t, fresh, fb, fc, source)
				if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				target := dictIntID(t, fresh, fc, "SELECT 'public.dict_int_custom'::regdictionary::oid::bigint")
				if target == source {
					t.Fatal("exact class-3600 collision did not remap imported dictionary", source)
				}
				freshCheck := func() {
					collision()
					dictIntControlState(t, fresh, fb, fc, n, mutated)
				}
				freshCheck()
				dictIntControlAuthorization(t, fresh, fb, fc, freshCheck)
				dictIntControlFailures(t, fresh, fb, fc, emitted, freshCheck)
				if !old.Next() {
					t.Fatal("retained source lexeme missing", old.Err())
				}
				var value any
				if err := old.Scan(&value); err != nil || value != "{123}" {
					t.Fatal("retained source lexeme", value, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("retained source row completion", old.Err())
				}
			})
		})
	}
}

func dictIntControlGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n dictIntControlNames) {
	t.Helper()
	templateName := strings.Split(n.template, ".")
	dictIntSelect(t, p, b, c, "SELECT t.tmplname,ns.nspname FROM pg_ts_template t JOIN pg_namespace ns ON ns.oid=t.tmplnamespace JOIN pg_ts_dict d ON d.dicttemplate=t.oid WHERE d.oid=$1::regdictionary::oid", []string{"tmplname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{templateName[1], templateName[0]}}, n.member)
	owner := n.helperOwner
	for _, callback := range []struct{ name, arguments, source string }{{n.init, "2281", "dintdict_init"}, {n.lexize, "2281 2281 2281 2281", "dintdict_lexize"}} {
		names := strings.Split(callback.name, ".")
		signature := callback.name + "(internal)"
		if callback.source == "dintdict_lexize" {
			signature = callback.name + "(internal,internal,internal,internal)"
		}
		dictIntSelect(t, p, b, c, "SELECT p.proname,ns.nspname,r.rolname,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proparallel,p.probin,p.prosrc FROM pg_proc p JOIN pg_namespace ns ON ns.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1::regprocedure::oid", []string{"proname", "nspname", "rolname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "probin", "prosrc"}, []string{"NAME", "NAME", "NAME", "TEXT", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT"}, [][]any{{names[1], names[0], owner, callback.arguments, int64(2281), true, "v", "u", "$libdir/dict_int", callback.source}}, signature)
		dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_extension e ON e.oid=dep.refobjid WHERE dep.classid=1255 AND dep.objid=$1::regprocedure::oid AND dep.refclassid=3079 AND e.extname='dict_int' AND dep.deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, signature)
	}
	dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_ts_dict d ON d.dicttemplate=dep.objid JOIN pg_extension e ON e.oid=dep.refobjid WHERE d.oid=$1::regdictionary::oid AND dep.classid=3764 AND dep.refclassid=3079 AND e.extname='dict_int' AND dep.deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, n.member)
	for _, dictionary := range []string{n.member, "public.dict_int_custom"} {
		dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_ts_dict d ON d.oid=dep.objid WHERE d.oid=$1::regdictionary::oid AND dep.classid=3600 AND dep.refclassid=3764 AND dep.refobjid=d.dicttemplate AND dep.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, dictionary)
	}
	dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_ts_template t ON t.oid=dep.objid JOIN pg_ts_dict d ON d.dicttemplate=t.oid WHERE d.oid=$1::regdictionary::oid AND dep.classid=3764 AND dep.refclassid=1255 AND (dep.refobjid=t.tmplinit::oid OR dep.refobjid=t.tmpllexize::oid) AND dep.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, n.member)
	dictIntSelect(t, p, b, c, "SELECT cfgparser::bigint,r.rolname FROM pg_ts_config cfg JOIN pg_roles r ON r.oid=cfg.cfgowner WHERE cfg.oid='dict_int_cfg'::regconfig::oid", []string{"cfgparser", "rolname"}, []string{"INT8", "NAME"}, [][]any{{int64(3722), "dict_int_owner"}})
	dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE classid=3602 AND objid='dict_int_cfg'::regconfig::oid AND refclassid=3600 AND refobjid='dict_int_custom'::regdictionary::oid AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})
}

// These are ordinary public SQL witnesses on both backends, separate from
// native-only Restore atomicity. Exercise the exact initial and replayed
// feature objects, preserving the caller's session authorization on every exit.
func dictIntControlAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func()) {
	t.Helper()
	const optionsSQL = "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid='public.dict_int_custom'::regdictionary::oid"
	const mappingsSQL = "SELECT mapcfg::bigint,maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='public.dict_int_cfg'::regconfig::oid OR mapcfg='dict_int_addon.dict_int_guard'::regconfig::oid ORDER BY mapcfg,maptokentype,mapseqno"
	options, mappings := dictIntRows(t, p, c, optionsSQL), dictIntRows(t, p, c, mappingsSQL)
	var sessionUser string
	if err := c.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&sessionUser); err != nil {
		t.Fatal("saved control authorization", err)
	}
	for _, tc := range []struct{ sql, message string }{
		{dictIntOwnerDeniedSQL, "must be owner of text search dictionary public.dict_int_custom"},
		{dictIntSchemaDeniedSQL, "permission denied for schema dict_int_addon"},
	} {
		for _, prepared := range []bool{false, true} {
			func() {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					if _, err := c.ExecContext(ctx, "SET SESSION AUTHORIZATION "+fixture.ExtensionTempIdentifier(sessionUser)); err != nil {
						t.Error("restore control authorization", err)
					}
				}()
				dictIntExec(t, p, b, c, "SET SESSION AUTHORIZATION dict_int_denied", 0)
				dictIntError(t, p, b, c, tc.sql, "42501", tc.message, prepared)
			}()
			dictIntSelect(t, p, b, c, "SELECT session_user AS actor", []string{"actor"}, []string{"NAME"}, [][]any{{sessionUser}})
			dictIntUnchanged(t, p, c, options, optionsSQL)
			dictIntUnchanged(t, p, c, mappings, mappingsSQL)
			check()
		}
	}
}

func dictIntControlFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted []byte, check func()) {
	t.Helper()
	dictIntExec(t, p, b, c, "CREATE TABLE dict_int_sentinel(v int)", 0)
	dictIntExec(t, p, b, c, "INSERT INTO dict_int_sentinel VALUES(42)", 1)
	old, err := c.QueryContext(p.Ctx, "SELECT v FROM dict_int_sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	types, err := old.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal("retained destination metadata", types, err)
	}
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	unchanged := func() {
		check()
		dictIntSelect(t, p, b, c, "SELECT v FROM dict_int_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("failed Restore changed destination committed state")
		}
	}
	for _, suffix := range []string{
		"ALTER TEXT SEARCH DICTIONARY public.dict_int_custom (maxlen=2,unknown=1);",
		"ALTER TEXT SEARCH DICTIONARY public.dict_int_custom OWNER TO dict_int_missing_owner;",
	} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+suffix)); err == nil {
			t.Fatal("invalid late replay published")
		}
		unchanged()
	}
	denied := string(emitted) + "SET SESSION AUTHORIZATION dict_int_denied; " + dictIntOwnerDeniedSQL + ";"
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(denied))
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of text search dictionary public.dict_int_custom"})
	unchanged()
	// Destination CREATE is intentionally trusted by Restore. Exercise actual
	// semantic source USAGE instead: the copied configuration consumes this
	// feature's dictionary, exists, and is owned by a different named role.
	deniedSchema := string(emitted) + "SET SESSION AUTHORIZATION dict_int_denied; " + dictIntSchemaDeniedSQL + ";"
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(deniedSchema))
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema dict_int_addon"})
	unchanged()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled Restore", err)
	}
	unchanged()
	reading, stopReading := context.WithCancel(p.Ctx)
	reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stopReading}
	err = stuntgres.Restore(reading, p.Native, reader)
	stopReading()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("reader-canceled Restore", err)
	}
	unchanged()
	if !old.Next() {
		t.Fatal("retained destination row missing", old.Err())
	}
	var value int64
	if err := old.Scan(&value); err != nil || value != 42 {
		t.Fatal("retained destination sentinel", value, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained destination row completion", old.Err())
	}
}
