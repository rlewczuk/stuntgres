package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSearchVisibilitySmoke(t *testing.T) {
	searchVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchVisibilitySetup(t, p, b, c)
		searchVisibilityStored(t, p, b, c)
		for _, k := range searchVisibilityClasses {
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			z := searchVisibilityDiscover(t, p, c, k, "sv_b", "v")
			stmt, err := c.PrepareContext(p.Ctx, searchVisibilityStep(k, nil, nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, reverse := range []bool{false, true} {
				path := "sv_a,sv_b"
				if reverse {
					path = "sv_b,sv_a"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path="+path, 0))
				searchVisibilityRoutes(t, p, b, c, k, stmt, a, !reverse)
				searchVisibilityRoutes(t, p, b, c, k, stmt, z, reverse)
				for _, arg := range []any{nil, int64(0)} {
					searchVisibilityRoutes(t, p, b, c, k, stmt, arg, nil)
				}
			}
			s := fixture.Select("SELECT pg_catalog."+k.fn+"(NULL) AS visible WHERE FALSE", []string{"visible"}, []string{"BOOL"}, [][]any{})
			s.CheckMetadata = fixture.VisibilityBoolMetadata
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		searchVisibilityMatrix(t, p, b, c, true)
	})
}

func TestReferenceSearchVisibilityPaths(t *testing.T) {
	searchVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchVisibilitySetup(t, p, b, c)
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		for _, k := range searchVisibilityClasses {
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			z := searchVisibilityDiscover(t, p, c, k, "sv_b", "v")
			stmt, err := c.PrepareContext(p.Ctx, searchVisibilityStep(k, nil, nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				path string
				a, z bool
			}{
				{"sv_a,sv_b", true, false},
				{"sv_b,sv_a", false, true},
				{"sv_off", false, false},
				{"missing_schema,sv_a,sv_a,sv_b", true, false},
				{"pg_temp,sv_a,sv_b", true, false},
			} {
				run("SET search_path=" + tc.path)
				searchVisibilityRoutes(t, p, b, c, k, stmt, a, tc.a)
				searchVisibilityRoutes(t, p, b, c, k, stmt, z, tc.z)
			}
			run("SET search_path=sv_a,sv_b")
			unqualified := fixture.Select("SELECT "+k.fn+"($1)", []string{k.fn}, []string{"BOOL"}, [][]any{{true}}, a)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, unqualified)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(unqualified))

			// Keep a separate borrower so an unread PostgreSQL cursor never
			// shares its physical protocol stream with the mutation commands.
			reader, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if _, err := reader.ExecContext(p.Ctx, "SET search_path=sv_a,sv_b"); err != nil {
				t.Fatal(err)
			}
			old, err := reader.QueryContext(p.Ctx, searchVisibilityStep(k, nil, nil).Query, z)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			run("BEGIN")
			run("DROP " + k.ddl + " sv_a.v CASCADE")
			searchVisibilityRoutes(t, p, b, c, k, stmt, a, nil)
			searchVisibilityRoutes(t, p, b, c, k, stmt, z, true)
			run("SET LOCAL search_path=sv_off")
			searchVisibilityRoutes(t, p, b, c, k, stmt, z, false)
			run("ROLLBACK")
			if restored := searchVisibilityDiscover(t, p, c, k, "sv_a", "v"); restored != a {
				t.Fatal("rollback replaced descriptor identity", a, restored)
			}
			searchVisibilityRoutes(t, p, b, c, k, stmt, a, true)
			searchVisibilityRoutes(t, p, b, c, k, stmt, z, false)
			visibilityInquiryRows(t, old, nil, k.fn, []any{false})
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if k.ddl == "STATISTICS" {
				continue
			}

			// Text-search identity mutations reuse their existing public DDL;
			// statistics needs no new ALTER capability to prove visibility.
			run("BEGIN")
			run("ALTER " + k.ddl + " sv_a.v SET SCHEMA sv_off")
			run("ALTER " + k.ddl + " sv_off.v RENAME TO renamed")
			searchVisibilityRoutes(t, p, b, c, k, stmt, a, false)
			searchVisibilityRoutes(t, p, b, c, k, stmt, z, true)
			run("SET LOCAL search_path=sv_off,sv_a,sv_b")
			searchVisibilityRoutes(t, p, b, c, k, stmt, a, true)
			run("ROLLBACK")
			searchVisibilityRoutes(t, p, b, c, k, stmt, a, true)
			searchVisibilityRoutes(t, p, b, c, k, stmt, z, false)
			run("BEGIN")
			run("ALTER " + k.ddl + " sv_a.v RENAME TO renamed")
			searchVisibilityRoutes(t, p, b, c, k, stmt, a, true)
			searchVisibilityRoutes(t, p, b, c, k, stmt, z, true)
			run("ROLLBACK")
		}
		// Dependency order: configuration, dictionary, parser, then template.
		for _, k := range searchVisibilityClasses {
			z := searchVisibilityDiscover(t, p, c, k, "sv_b", "v")
			stmt, err := c.PrepareContext(p.Ctx, searchVisibilityStep(k, nil, nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run("DROP " + k.ddl + " sv_b.v")
			searchVisibilityRoutes(t, p, b, c, k, stmt, z, nil)
		}
	})
}
