package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterNotNullMetadata(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "domain_move")
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer CONSTRAINT d_not_null CHECK(VALUE>0)", "CREATE DOMAIN domain_s.child AS domain_s.d", "ALTER DOMAIN domain_s.d SET NOT NULL"} {
			run(fixture.Exec(q, 0))
		}
		id := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND contype='n'")
		check := func(domain, name, schema string) {
			run(fixture.Select("SELECT c.conname,c.contype::text AS kind,c.contypid='"+domain+"'::regtype::oid AS owner,c.conrelid=0::oid AS no_relation,c.conenforced,c.convalidated,c.conislocal,c.condeferrable,c.condeferred,c.coninhcount,c.connoinherit,c.conbin IS NULL AS no_expression,pg_get_constraintdef(c.oid) AS definition,n.nspname FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE c.contypid='"+domain+"'::regtype::oid AND c.contype='n'", []string{"conname", "kind", "owner", "no_relation", "conenforced", "convalidated", "conislocal", "condeferrable", "condeferred", "coninhcount", "connoinherit", "no_expression", "definition", "nspname"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT2", "BOOL", "BOOL", "TEXT", "NAME"}, [][]any{{name, "n", true, true, true, true, true, false, false, int64(0), false, true, "NOT NULL", schema}}))
			run(fixture.Select("SELECT d.deptype::text AS kind FROM pg_depend d JOIN pg_constraint c ON c.oid=d.objid WHERE d.classid='pg_constraint'::regclass::oid AND d.refclassid='pg_type'::regclass::oid AND d.refobjid='"+domain+"'::regtype::oid AND c.contype='n'", []string{"kind"}, []string{"TEXT"}, [][]any{{"a"}}))
			run(fixture.Select("SELECT (pg_identify_object('pg_constraint'::regclass,c.oid,0)).type AS kind,(pg_identify_object('pg_constraint'::regclass,c.oid,0)).schema AS schema,(pg_identify_object('pg_constraint'::regclass,c.oid,0)).name AS name,(pg_identify_object('pg_constraint'::regclass,c.oid,0)).identity AS identity,pg_describe_object('pg_constraint'::regclass,c.oid,0) AS description,(pg_get_object_address('domain constraint',ARRAY['"+domain+"'],ARRAY['"+name+"'])).objid=c.oid AS inverse FROM pg_constraint c WHERE c.contypid='"+domain+"'::regtype::oid AND c.contype='n'", []string{"kind", "schema", "name", "identity", "description", "inverse"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"domain constraint", schema, nil, name + " on " + domain, "constraint " + name, true}}))
		}
		check("domain_s.d", "d_not_null1", "domain_s")
		for _, s := range domainAlterOwnNull("child", false, nil) {
			run(s)
		}
		run(fixture.Prepared(fixture.Exec("ALTER DOMAIN domain_s.d SET NOT NULL", 0)))
		if domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND contype='n'") != id {
			t.Fatal("repeat SET reminted identity")
		}
		for _, q := range []string{"CREATE SCHEMA domain_move", "ALTER DOMAIN domain_s.d RENAME TO renamed", "ALTER DOMAIN domain_s.renamed SET SCHEMA domain_move"} {
			run(fixture.Exec(q, 0))
		}
		check("domain_move.renamed", "d_not_null1", "domain_move")
		if domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_move.renamed'::regtype::oid AND contype='n'") != id {
			t.Fatal("identity edit reminted constraint")
		}
		d := domainNotNullDiagnostic("domain_move", "renamed")
		run(domainDefaultFailure("SELECT NULL::domain_move.renamed AS v FROM public.one", d, false, true))
		run(fixture.Exec("ALTER DOMAIN domain_move.renamed DROP NOT NULL", 0))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE contypid='domain_move.renamed'::regtype::oid ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"d_not_null"}}))
		// CREATE/replay uses the same real definition constructor, including names.
		run(fixture.Exec("CREATE DOMAIN domain_s.created AS integer CONSTRAINT created_nn NOT NULL CONSTRAINT created_check CHECK(VALUE>0)", 0))
		check("domain_s.created", "created_nn", "domain_s")
		long := strings.Repeat("é", 31)
		// PostgreSQL truncates the name component by complete characters, leaving
		// room for '_' and 'not_null' (54 name bytes, 9 suffix bytes).
		want := strings.Repeat("é", 27) + "_not_null"
		run(fixture.Exec("CREATE DOMAIN domain_s.\""+long+"\" AS integer CONSTRAINT \""+want+"\" CHECK(VALUE>0)", 0))
		run(fixture.Exec("ALTER DOMAIN domain_s.\""+long+"\" SET NOT NULL", 0))
		collision := strings.Repeat("é", 26) + "_not_null1"
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE contypid='domain_s.\""+long+"\"'::regtype::oid AND contype='n'", []string{"conname"}, []string{"NAME"}, [][]any{{collision}}))
		if !utf8.ValidString(collision) || len(collision) > 63 {
			t.Fatal("invalid independent generated-name expectation")
		}
		run(fixture.Exec("DROP DOMAIN domain_s.created", 0))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname IN('created_nn','created_check')", []string{"conname"}, []string{"NAME"}, nil))
		run(fixture.Exec("DROP DOMAIN domain_move.renamed CASCADE", 0))
	})
}

func TestReferenceDomainAlterNotNullCreateNamesOrder(t *testing.T) {
	for _, tc := range []struct {
		name, invalid, valid, duplicate string
		validRows                       [][]any
	}{
		{
			name:      "late_nn",
			invalid:   "CHECK(VALUE>0) CONSTRAINT late_nn_check NOT NULL",
			valid:     "CONSTRAINT late_nn_check NOT NULL CHECK(VALUE>0)",
			duplicate: "late_nn_check",
			validRows: [][]any{{"late_nn_check", "n"}, {"late_nn_check1", "c"}},
		},
		{
			name:      "early_nn",
			invalid:   "NOT NULL CONSTRAINT early_nn_not_null CHECK(VALUE>0)",
			valid:     "CONSTRAINT early_nn_not_null CHECK(VALUE>0) NOT NULL",
			duplicate: "early_nn_not_null",
			validRows: [][]any{{"early_nn_not_null", "c"}, {"early_nn_not_null1", "n"}},
		},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
				domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
					q := "CREATE DOMAIN domain_s." + tc.name + " AS integer " + tc.invalid
					want := fixture.ControlsReferenceDiagnostic("42710", "constraint \""+tc.duplicate+"\" for domain \""+tc.name+"\" already exists")
					s := fixture.ErrorDiagnostic(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					// Failure cannot publish the domain, its companion array, or
					// orphaned definitions from the preceding valid declaration.
					run(fixture.Select("SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typname IN('"+tc.name+"','_"+tc.name+"') ORDER BY t.typname", []string{"typname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT c.conname FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='domain_s' ORDER BY c.conname", []string{"conname"}, []string{"NAME"}, nil))
					// Reversing the declarations is legal: an installed explicit
					// name makes only the later generated name acquire a suffix.
					s = fixture.Exec("CREATE DOMAIN domain_s."+tc.name+" AS integer "+tc.valid, 0)
					s.Prepared = prepared
					run(s)
					run(fixture.Select("SELECT conname,contype::text AS kind FROM pg_constraint WHERE contypid='domain_s."+tc.name+"'::regtype::oid ORDER BY conname", []string{"conname", "kind"}, []string{"NAME", "TEXT"}, tc.validRows))
					run(fixture.Select("SELECT typnotnull FROM pg_type WHERE oid='domain_s."+tc.name+"'::regtype::oid", []string{"typnotnull"}, []string{"BOOL"}, [][]any{{true}}))
					domain := domainAlterOID(t, p, c, "SELECT 'domain_s."+tc.name+"'::regtype::oid")
					nn := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s."+tc.name+"'::regtype::oid AND contype='n'")
					check := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s."+tc.name+"'::regtype::oid AND contype='c'")
					if domain == nn || domain == check || nn == check {
						t.Fatal("domain/CHECK/NN identities alias")
					}
				})
			})
		}
	}
}
