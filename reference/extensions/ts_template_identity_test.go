package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

type templateIDs struct { oid, namespace, init, lexize int64 }
func templateIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) templateIDs {
	t.Helper()
	var id templateIDs
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,tmplnamespace,tmplinit::oid,tmpllexize::oid FROM pg_ts_template WHERE tmplname=$1", name).Scan(&id.oid, &id.namespace, &id.init, &id.lexize); err != nil { t.Fatal(err) }
	if id.oid <= 0 || id.namespace <= 0 || id.lexize <= 0 { t.Fatal("invalid template identity", id) }
	return id
}
func templateLink(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, dictionary string, id int64) {
	t.Helper()
	s := fixture.Select("SELECT dicttemplate::bigint FROM pg_ts_dict WHERE dictname=$1", []string{"dicttemplate"}, []string{"INT8"}, [][]any{{id}}, dictionary)
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
}

func TestReferenceTSTemplatePrivileges(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE ROLE ext_installer", 0))
		run(fixture.Exec("CREATE SCHEMA ext_locked AUTHORIZATION ext_installer", 0))
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA ext_addon TO ext_installer", 0))
		templateDefinition(t, p, c, r, "ext_locked.template_owned_schema")
		templateDefinition(t, p, c, r, "ext_addon.template_foreign_schema")
		run(fixture.Exec("SET ROLE ext_installer", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		templateDiagnostic(t, p, c, r, "CREATE TEXT SEARCH TEMPLATE ext_locked.template_denied ("+templateSimpleMethods+")", "42501", "must be superuser to create text search templates")
		templateDiagnostic(t, p, c, r, "ALTER TEXT SEARCH TEMPLATE ext_locked.template_owned_schema RENAME TO template_denied", "42501", "must be superuser to rename text search template ext_locked.template_owned_schema")
		templateDiagnostic(t, p, c, r, "ALTER TEXT SEARCH TEMPLATE ext_locked.template_owned_schema SET SCHEMA ext_addon", "42501", "must be superuser to set schema of text search template ext_locked.template_owned_schema")
		run(fixture.Exec("ALTER TEXT SEARCH TEMPLATE ext_locked.template_owned_schema SET SCHEMA ext_locked", 0))
		run(fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH TEMPLATE ext_addon.template_foreign_schema SET SCHEMA ext_addon", 0)))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE CREATE ON SCHEMA ext_addon FROM ext_installer", 0))
		run(fixture.Exec("SET ROLE ext_installer", 0))
		templateDiagnostic(t, p, c, r, "ALTER TEXT SEARCH TEMPLATE ext_addon.template_foreign_schema SET SCHEMA ext_addon", "42501", "permission denied for schema ext_addon")
		templateDiagnostic(t, p, c, r, "ALTER TEXT SEARCH TEMPLATE ext_locked.template_owned_schema SET SCHEMA ext_addon", "42501", "permission denied for schema ext_addon")
		templateDiagnostic(t, p, c, r, "DROP TEXT SEARCH TEMPLATE ext_addon.template_foreign_schema", "42501", "must be superuser")
		run(fixture.Prepared(fixture.Exec("DROP TEXT SEARCH TEMPLATE ext_locked.template_owned_schema", 0)))
		run(fixture.Exec("RESET ROLE", 0))
		templateAbsent(t, p, c, r, "template_owned_schema")
		if templateIdentity(t, p, c, "template_foreign_schema").init != 3725 { t.Fatal("denied drop changed definition") }
	})
}

func TestReferenceTSTemplateIdentity(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE SCHEMA ext_locked", 0))
		templateDefinition(t, p, c, r, "public.template_identity")
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_identity_dict (TEMPLATE=public.template_identity,STOPWORDS=english)", 0))
		before := templateIdentity(t, p, c, "template_identity")
		q := "SELECT ts_lexize('public.template_identity_dict',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:c, Statement:stmt, SQL:q}
		templateDiagnostic(t, p, c, r, "ALTER TEXT SEARCH TEMPLATE public.template_identity RENAME TO template_identity", "42710", `text search template "template_identity" already exists in schema "public"`)
		if templateIdentity(t, p, c, "template_identity") != before { t.Fatal("same-name rename changed identity") }
		templateLink(t, p, c, r, "template_identity_dict", before.oid)
		templateLexemes(t, p, held, r, pg, q, "{word}", "WORD")
		run(fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH TEMPLATE public.template_identity RENAME TO template_renamed", 0)))
		run(fixture.Exec("ALTER TEXT SEARCH TEMPLATE public.template_renamed SET SCHEMA ext_locked", 0))
		after := templateIdentity(t, p, c, "template_renamed")
		if after.oid != before.oid || after.init != before.init || after.lexize != before.lexize || after.namespace == before.namespace { t.Fatal("relabel replaced identity", before, after) }
		templateLink(t, p, c, r, "template_identity_dict", before.oid)
		templateLexemes(t, p, held, r, pg, q, "{word}", "WORD")
		templateDefinition(t, p, c, r, "ext_locked.template_collision")
		templateDiagnostic(t, p, c, r, "ALTER TEXT SEARCH TEMPLATE ext_locked.template_renamed RENAME TO template_collision", "42710", `text search template "template_collision" already exists in schema "ext_locked"`)
		if templateIdentity(t, p, c, "template_renamed") != after { t.Fatal("collision changed identity") }
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		member := templateIdentity(t, p, c, "unaccent")
		run(fixture.Exec("ALTER TEXT SEARCH TEMPLATE ext_addon.unaccent RENAME TO template_member", 0))
		run(fixture.Exec("ALTER TEXT SEARCH TEMPLATE ext_addon.template_member SET SCHEMA ext_locked", 0))
		moved := templateIdentity(t, p, c, "template_member")
		if moved.oid != member.oid || moved.init != member.init || moved.lexize != member.lexize { t.Fatal("member relabel changed manifest links", member, moved) }
		templateLink(t, p, c, r, "unaccent", member.oid)
		templateLexemes(t, p, c, r, pg, "SELECT ts_lexize('ext_addon.unaccent','Hôtel') AS lexemes", "{Hotel}")
		run(fixture.Select("SELECT n.nspname,p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1", []string{"nspname","proname"}, []string{"NAME","NAME"}, [][]any{{"ext_addon","unaccent_init"}}, member.init))
		run(fixture.Select("SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='unaccent'", []string{"nspname"}, []string{"NAME"}, [][]any{{"ext_addon"}}))
		for _, mode := range []string{"RESTRICT", "CASCADE"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DROP TEXT SEARCH TEMPLATE ext_locked.template_member "+mode, fixture.Diagnostic{Code:"2BP01", Message:"cannot drop text search template ext_locked.template_member because extension unaccent requires it", Hint:"You can drop extension unaccent instead."})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				if templateIdentity(t, p, c, "template_member") != moved { t.Fatal("denied member drop changed identity") }
				templateLink(t, p, c, r, "unaccent", member.oid)
				templateLexemes(t, p, c, r, pg, "SELECT ts_lexize('ext_addon.unaccent','Hôtel') AS lexemes", "{Hotel}")
			}
		}
		membership := fixture.Select("SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=3764 AND d.objid=$1 AND d.refclassid=3079 AND d.deptype='e'", []string{"extname"}, []string{"NAME"}, [][]any{{"unaccent"}}, member.oid)
		run(membership)
		run(fixture.Prepared(membership))
	})
}
