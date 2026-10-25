package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func metaphoneInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, namespace, owner string, trusted bool) {
	t.Helper()
	var ext, ns int64
	var version, schema, extensionOwner string
	var reloc bool
	err := c.QueryRowContext(p.Ctx, "SELECT e.oid,extnamespace,extversion,extrelocatable,nspname,rolname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE extname='fuzzystrmatch'").Scan(&ext, &ns, &version, &reloc, &schema, &extensionOwner)
	if err != nil || ext <= 0 || ns <= 0 || version != "1.2" || !reloc || schema != namespace || extensionOwner != owner {
		t.Fatal("extension identity", ext, ns, version, reloc, schema, extensionOwner, err)
	}
	want := map[string]int64{namespace + ".metaphone(text,integer)": 2, namespace + ".dmetaphone(text)": 1, namespace + ".dmetaphone_alt(text)": 1}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,pronargs,prorettype,provolatile,proisstrict,proparallel,p.proowner,rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE pronamespace=$1 AND (proname='metaphone' OR proname='dmetaphone' OR proname='dmetaphone_alt')", ns)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[int64]bool{}
	for rows.Next() {
		var id, nargs, result, ownerID int64
		var signature, volatility, parallel, memberOwner string
		var strict bool
		if err := rows.Scan(&id, &signature, &nargs, &result, &volatility, &strict, &parallel, &ownerID, &memberOwner); err != nil {
			t.Fatal(err)
		}
		arity, ok := want[signature]
		if !ok || arity != nargs || id <= 0 || ids[id] || result != 25 || volatility != "i" || !strict || parallel != "s" {
			t.Fatal("selected procedure identity", id, signature, nargs, result, volatility, strict, parallel)
		}
		if trusted {
			if ownerID != 10 {
				t.Fatal("trusted C member must have bootstrap owner", ownerID)
			}
		} else if memberOwner != owner {
			t.Fatal("member owner", memberOwner, owner)
		}
		delete(want, signature)
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(want) != 0 {
		t.Fatal("missing selected signatures", want)
	}
	for id := range ids {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND refclassid=3079 AND refobjid=$2 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, id, ext))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=3079 AND objid=$1 AND refclassid=2615 AND refobjid=$2 AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, ext, ns))
	if !b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}, ext))
	}
}

func TestReferenceMetaphoneInstallation(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		name := "Superuser"
		if trusted {
			name = "Trusted"
		}
		t.Run(name, func(t *testing.T) {
			metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE SCHEMA phon_addon", 0))
				role := "SUPERUSER"
				if trusted {
					role = "NOSUPERUSER"
				}
				run(fixture.Exec("CREATE USER phon_owner "+role, 0))
				var database string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
					t.Fatal(err)
				}
				quotedDatabase := "\"" + strings.ReplaceAll(database, "\"", "\"\"") + "\""
				run(fixture.Exec("GRANT CREATE ON DATABASE "+quotedDatabase+" TO phon_owner", 0))
				// Database ACLs survive the fixture's public-schema reset. Revoke
				// before its later DROP ROLE, including assertion-failure exits.
				defer func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "REVOKE CREATE ON DATABASE " + quotedDatabase + " FROM phon_owner"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}()
				run(fixture.Exec("SET SESSION AUTHORIZATION phon_owner", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA phon_addon VERSION '1.2'", 0))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA phon_addon VERSION '1.2'", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				metaphoneInventory(t, p, b, c, "phon_addon", "phon_owner", trusted)
				run(fixture.Select("SELECT phon_addon.metaphone('GUMBO',4),phon_addon.dmetaphone('gumbo'),phon_addon.dmetaphone_alt('gumbo'),phon_addon.levenshtein('kitten','sitting')", []string{"metaphone", "dmetaphone", "dmetaphone_alt", "levenshtein"}, []string{"TEXT", "TEXT", "TEXT", "INT4"}, [][]any{{"KM", "KMP", "KMP", int64(3)}}))
				run(fixture.Select("SELECT phon_addon.daitch_mokotoff('John')", []string{"daitch_mokotoff"}, []string{"_TEXT"}, [][]any{{"{160000,460000}"}}))
			})
		})
	}
}
