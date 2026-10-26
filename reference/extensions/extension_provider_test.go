package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL's public-schema reset does not remove database-wide extensions.
// Clean the oracle's extension and non-public schemas before and after each case.
func extensionCase(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS unaccent CASCADE", "DROP SCHEMA IF EXISTS ext_addon CASCADE", "DROP SCHEMA IF EXISTS ext_locked CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
					var n int
					if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='ext_installer'").Scan(&n); err != nil {
						t.Error(err)
					} else if n != 0 {
						for _, q := range []string{"DROP OWNED BY ext_installer", "DROP ROLE ext_installer"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, c, b.Recorder, b.PG)
		})
	}
}

func extensionError(query, code, message string) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
}
func extensionMissing(query, name, args string) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", name, args), Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
}
func extensionCount(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, n int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='unaccent'", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
}

func TestReferenceExtensionInstallAndProvider(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(extensionMissing("SELECT unaccent('Hôtel')", "unaccent", "unknown"))
		absent := fixture.Prepared(extensionMissing("SELECT unaccent('Hôtel')", "unaccent", "unknown"))
		absent.ErrorStage = "prepare"
		run(absent)
		run(extensionMissing("SELECT unaccent(NULL)", "unaccent", "unknown"))
		run(fixture.ImplicitError("CREATE EXTENSION unaccent junk", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "junk"`, Position: "27"}))
		extensionCount(t, p, c, r, 0)
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		cases := []struct{ input, output string }{{"Hôtel", "Hotel"}, {"", ""}, {"Æsir Straße Œuf ﬃ", "AEsir Strasse OEuf ffi"}, {"À", "A"}, {"1½", "1 1/2"}, {"東京🙂", "東京🙂"}, {"ёлка", "елка"}, {"ЁЖИК", "ЕЖИК"}, {"℃℉", "°C°F"}, {"℗", "(P)"}, {"ℌ", "H"}, {"foobar", "foobar"}}
		stmt, err := c.PrepareContext(p.Ctx, "SELECT unaccent($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range cases {
			s := fixture.Select("SELECT unaccent($1)", []string{"unaccent"}, []string{"TEXT"}, [][]any{{tc.output}}, tc.input)
			run(s)
			run(fixture.Prepared(s))
			var got string
			if err := stmt.QueryRowContext(p.Ctx, tc.input).Scan(&got); err != nil || got != tc.output {
				t.Fatalf("held parameter %q: %q %v", tc.input, got, err)
			}
		}
		for _, q := range []string{"SELECT unaccent(NULL)", "SELECT unaccent(NULL::text)"} {
			s := fixture.Select(q, []string{"unaccent"}, []string{"TEXT"}, [][]any{{nil}})
			run(s)
			run(fixture.Prepared(s))
		}
		var null any
		if err := stmt.QueryRowContext(p.Ctx, nil).Scan(&null); err != nil || null != nil {
			t.Fatalf("held NULL: %#v %v", null, err)
		}
		wrong := extensionMissing("SELECT unaccent(1)", "unaccent", "integer")
		run(wrong)
		wrong = fixture.Prepared(wrong)
		wrong.ErrorStage = "prepare"
		run(wrong)
		run(extensionMissing(`SELECT "Unaccent"('Hôtel')`, "Unaccent", "unknown"))
		run(fixture.Select("SELECT UNACCENT('Hôtel'), unaccent(''), unaccent(NULL), unaccent('Æsir Straße Œuf ﬃ'), unaccent('À'), unaccent('1½'), unaccent('東京🙂')", []string{"unaccent", "unaccent", "unaccent", "unaccent", "unaccent", "unaccent", "unaccent"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"Hotel", "", nil, "AEsir Strasse OEuf ffi", "A", "1 1/2", "東京🙂"}}))
		run(fixture.Exec("CREATE TABLE ext_checked(v TEXT CHECK(unaccent(v) <> ''))", 0))
		run(fixture.Exec("INSERT INTO ext_checked VALUES('Hôtel'),(NULL)", 2))
		run(fixture.Select("SELECT unaccent(min(v)) FROM ext_checked", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		run(fixture.Exec("SET search_path TO pg_catalog", 0))
		run(extensionMissing("SELECT unaccent('Hôtel')", "unaccent", "unknown"))
		run(fixture.Prepared(fixture.Select("SELECT public.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})))
		run(fixture.Exec("RESET search_path", 0))
	})
}

func TestReferenceExtensionIdentityAndRepeat(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent WITH SCHEMA ext_addon VERSION '1.1'", 0))
		extensionInventory(t, p, c)
		run(fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		run(fixture.Exec("SET search_path TO ext_addon", 0))
		run(fixture.Prepared(fixture.Select("SELECT unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})))
		run(extensionError("CREATE EXTENSION unaccent SCHEMA missing_ext_schema VERSION 'missing'", "42710", `extension "unaccent" already exists`))
		run(extensionError("CREATE EXTENSION unaccent SCHEMA public", "42710", `extension "unaccent" already exists`))
		var before int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='unaccent'").Scan(&before); err != nil {
			t.Fatal(err)
		}
		memberIDs := extensionMemberIDs(t, p, c, before)
		var notices []fixture.Diagnostic
		_, noticeConn, nr := fixture.NoticeReferencePool(t, p, pg, &notices)
		fixture.RunStep(t, p.Ctx, noticeConn, nr, fixture.Exec("CREATE EXTENSION IF NOT EXISTS unaccent SCHEMA missing_ext_schema VERSION 'missing'", 0))
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "unaccent" already exists, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices %#v want %#v", notices, want)
		}
		run(fixture.Exec("CREATE USER ext_installer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		run(extensionError("CREATE EXTENSION unaccent VERSION 'missing'", "42710", `extension "unaccent" already exists`))
		fixture.RunStep(t, p.Ctx, noticeConn, nr, fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		fixture.RunStep(t, p.Ctx, noticeConn, nr, fixture.Exec("CREATE EXTENSION IF NOT EXISTS unaccent SCHEMA missing_ext_schema VERSION 'missing'", 0))
		fixture.RunStep(t, p.Ctx, noticeConn, nr, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if !reflect.DeepEqual(notices, append(want, want...)) {
			t.Fatalf("unprivileged skipping notices %#v", notices)
		}
		var after int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='unaccent'").Scan(&after); err != nil || before != after {
			t.Fatalf("skip identity %d -> %d: %v", before, after, err)
		}
		if !reflect.DeepEqual(memberIDs, extensionMemberIDs(t, p, c, after)) {
			t.Fatal("skipping changed installed member identities")
		}
		extensionInventory(t, p, c)
	})
}

func extensionInventory(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	var ext, ns, owner int64
	var name, version string
	var reloc bool
	var config, condition any
	err := c.QueryRowContext(p.Ctx, "SELECT oid,extname,extowner,extnamespace,extrelocatable,extversion,extconfig,extcondition FROM pg_extension WHERE extname='unaccent'").Scan(&ext, &name, &owner, &ns, &reloc, &version, &config, &condition)
	if err != nil || ext <= 0 || ns <= 0 || owner <= 0 || name != "unaccent" || version != "1.1" || !reloc || config != nil || condition != nil {
		t.Fatalf("extension identity: %d %q %d %d %v %q %#v %#v: %v", ext, name, owner, ns, reloc, version, config, condition, err)
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", ext)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	members := map[int64]int64{}
	counts := map[int64]int{}
	for rows.Next() {
		var class, id, sub, refclass, refid, refsub int64
		var typ string
		if err := rows.Scan(&class, &id, &sub, &refclass, &refid, &refsub, &typ); err != nil {
			t.Fatal(err)
		}
		if id <= 0 || sub != 0 || refclass != 3079 || refid != ext || refsub != 0 || typ != "e" {
			t.Fatal("invalid member edge")
		}
		if _, ok := members[id]; ok {
			t.Fatal("duplicate member")
		}
		members[id] = class
		counts[class]++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !reflect.DeepEqual(counts, map[int64]int{1255: 4, 3764: 1, 3600: 1}) {
		t.Fatalf("membership %#v", counts)
	}
	rows, err = c.QueryContext(p.Ctx, "SELECT oid,proname,pronamespace,proowner,pronargs,prorettype,provolatile,proisstrict,proparallel FROM pg_proc WHERE pronamespace=$1", ns)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	procs := 0
	for rows.Next() {
		var id, pns, powner, nargs, ret int64
		var pname, vol, parallel string
		var strict bool
		if err := rows.Scan(&id, &pname, &pns, &powner, &nargs, &ret, &vol, &strict, &parallel); err != nil {
			t.Fatal(err)
		}
		if members[id] != 1255 {
			continue
		}
		procs++
		if pns != ns || powner <= 0 || parallel != "s" {
			t.Fatal("proc identity")
		}
		switch pname {
		case "unaccent":
			if (nargs != 1 && nargs != 2) || ret != 25 || vol != "s" || !strict {
				t.Fatal("wrapper signature/properties")
			}
		case "unaccent_init":
			if nargs != 1 || ret != 2281 || vol != "v" || strict {
				t.Fatal("init signature/properties")
			}
		case "unaccent_lexize":
			if nargs != 4 || ret != 2281 || vol != "v" || strict {
				t.Fatal("lexize signature/properties")
			}
		default:
			t.Fatal("unexpected proc", pname)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if procs != 4 {
		t.Fatal("proc count", procs)
	}
	var tmpl, tns int64
	var tn string
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,tmplname,tmplnamespace FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace=$1", ns).Scan(&tmpl, &tn, &tns); err != nil || members[tmpl] != 3764 || tns != ns || tn != "unaccent" {
		t.Fatalf("template: %v", err)
	}
	var dict, dns, downer, dtmpl int64
	var dn, opts string
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,dictname,dictnamespace,dictowner,dicttemplate,dictinitoption FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace=$1", ns).Scan(&dict, &dn, &dns, &downer, &dtmpl, &opts); err != nil || members[dict] != 3600 || dns != ns || downer <= 0 || dtmpl != tmpl || dn != "unaccent" || opts != "rules = 'unaccent'" {
		t.Fatalf("dictionary identity/options %q: %v", opts, err)
	}
}

func extensionMemberIDs(t *testing.T, p *fixture.Pair, c *sql.Conn, ext int64) []int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT objid FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e' ORDER BY objid", ext)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func extensionAbsentMembers(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	for _, query := range []string{"SELECT count(*) FROM pg_proc WHERE proname='unaccent' OR proname='unaccent_init' OR proname='unaccent_lexize'", "SELECT count(*) FROM pg_ts_template WHERE tmplname='unaccent'", "SELECT count(*) FROM pg_ts_dict WHERE dictname='unaccent'"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}
