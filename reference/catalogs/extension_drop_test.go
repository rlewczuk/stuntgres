package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// extensionDropCase uses the existing extension fixture's isolated installed
// catalog and role cleanup. Extra provider cleanup is PostgreSQL fixture setup.
func extensionDropCase(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		if pg {
			clean := func() {
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP EXTENSION IF EXISTS intarray CASCADE", "DROP EXTENSION IF EXISTS intagg CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(q, err)
					}
				}
			}
			clean()
			defer clean()
		}
		body(t, p, c, r, pg)
	})
}

func extensionDropNotices(t *testing.T, got, want []fixture.Diagnostic) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DROP EXTENSION notices got=%#v want=%#v", got, want)
	}
}

func extensionDropRestrict(detail string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "2BP01", Message: "cannot drop extension unaccent because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func TestReferenceExtensionDropBasic(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "Ext Drop")
	extensionDropCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, pg, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		run(fixture.Exec(`CREATE SCHEMA "Ext Drop"`, 0))
		run(fixture.Exec(`CREATE TABLE "Ext Drop".ext_drop_keep(v int)`, 0))
		run(fixture.Exec(`INSERT INTO "Ext Drop".ext_drop_keep VALUES(77)`, 1))
		var retired extensionTempIdentity
		for i, mode := range []string{"", " RESTRICT"} {
			run(fixture.Exec(`CREATE EXTENSION unaccent SCHEMA "Ext Drop"`, 0))
			id := extensionTempCapture(t, p, c)
			if i != 0 {
				if id.extension == retired.extension {
					t.Error("reinstall reused extension identity")
				}
				for _, member := range id.members {
					for _, old := range retired.members {
						if member == old {
							t.Error("reinstall reused member identity", member)
						}
					}
				}
			}
			extensionTempCall(t, p, c, r, "Ext Drop")
			query := `DROP EXTENSION "unaccent"` + mode
			if i == 0 {
				query = "DROP EXTENSION UNACCENT" + mode
			}
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			extensionTempPresent(t, p, c, r, id)
			stmt.Close()
			step := fixture.Exec(query, 0)
			step.Prepared = i != 0
			run(step)
			extensionTempGone(t, p, c, r, id)
			run(fixture.Select(`SELECT v FROM "Ext Drop".ext_drop_keep`, []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
			run(fixture.Select("SELECT installed_version FROM pg_available_extensions WHERE name='unaccent'", []string{"installed_version"}, []string{"TEXT"}, [][]any{{nil}}))
			for _, q := range []string{`SELECT "Ext Drop".unaccent('Hôtel'::text)`, `SELECT "Ext Drop".unaccent(NULL::text)`} {
				for _, prepared := range []bool{false, true} {
					s := extensionMissing(q, "Ext Drop.unaccent", "text")
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			retired = id
			extensionDropNotices(t, notices, nil)
		}
	})
}
