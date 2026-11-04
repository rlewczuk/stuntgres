package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func publicationTablesTemporaryDiagnostic() fixture.Step {
	return publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_temp", "22023", `cannot add relation "publication_rt_temp" to publication`, "This operation is not supported for temporary tables.", "")
}

func TestReferencePGPublicationTablesVisibilityDiagnostics(t *testing.T) {
	t.Run("represented-table-and-own-view-acls", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"publication_rt_owner", "publication_rt_reader"})
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			admin := publicationConn(t, p, b)
			reader := publicationConn(t, p, b)
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := reader.ExecContext(ctx, "RESET ROLE"); err != nil {
					t.Error("reader role cleanup", err)
				}
				if _, err := admin.ExecContext(ctx, "GRANT SELECT ON pg_catalog.pg_publication_tables TO PUBLIC"); err != nil {
					t.Error("publication view ACL restoration", err)
				}
			}()
			run := func(steps ...fixture.Step) { publicationRun(t, p, b, admin, steps...) }
			run(fixture.Exec("CREATE ROLE publication_rt_owner", 0), fixture.Exec("CREATE ROLE publication_rt_reader", 0),
				fixture.Exec("CREATE SCHEMA publication_rt_other AUTHORIZATION publication_rt_owner", 0),
				fixture.Exec("CREATE TABLE publication_rt_other.t(id integer,v integer)", 0),
				fixture.Exec("ALTER TABLE publication_rt_other.t OWNER TO publication_rt_owner", 0),
				fixture.Exec("REVOKE ALL ON TABLE publication_rt_other.t FROM PUBLIC,publication_rt_reader", 0),
				fixture.Exec("REVOKE ALL ON SCHEMA publication_rt_other FROM PUBLIC,publication_rt_reader", 0),
				fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_other.t", 0),
				fixture.Exec("CREATE PUBLICATION publication_rt_schema FOR TABLES IN SCHEMA publication_rt_other", 0),
				fixture.Exec("CREATE PUBLICATION publication_rt_all FOR ALL TABLES", 0))
			publicationRun(t, p, b, reader, fixture.Exec("SET ROLE publication_rt_reader", 0), fixture.Exec("SET search_path TO public", 0))
			// Released PG18 has no represented-table SELECT, schema-USAGE,
			// ownership or search-path predicate on this public metadata view.
			want := [][]any{{"publication_rt_all", "publication_rt_other", "t", "{id,v}", nil}, {"publication_rt_p", "publication_rt_other", "t", "{id,v}", nil}, {"publication_rt_schema", "publication_rt_other", "t", "{id,v}", nil}}
			for _, prepared := range []bool{false, true} {
				publicationTablesRead(t, p, b, reader, publicationTablesSQL, prepared, want...)
			}
			deniedTable := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema publication_rt_other")
			deniedTable.Position = "15"
			s := fixture.ErrorDiagnostic("SELECT * FROM publication_rt_other.t", deniedTable)
			s.SelectRows = true
			publicationRun(t, p, b, reader, s, fixture.Prepared(s))
			stmt, err := reader.PrepareContext(p.Ctx, publicationTablesSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: publicationTablesSQL}
			publicationRun(t, p, b, held, publicationTablesState(want...))
			run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_publication_tables FROM PUBLIC", 0))
			for _, query := range []string{publicationTablesSQL, "SELECT pubname FROM pg_catalog.pg_publication_tables WHERE false"} {
				denied := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for view pg_publication_tables"))
				denied.SelectRows, denied.ErrorStage = true, "execute"
				publicationRun(t, p, b, reader, denied, fixture.Prepared(denied))
				if query == publicationTablesSQL {
					publicationRun(t, p, b, held, denied)
				}
			}
			run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_publication_tables TO PUBLIC", 0))
			publicationRun(t, p, b, held, publicationTablesState(want...))
			publicationTablesRead(t, p, b, reader, publicationTablesSQL, true, want...)
		})
	})
	t.Run("eligible-relations-and-atomic-diagnostic", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := publicationConn(t, p, b)
			run := func(steps ...fixture.Step) { publicationRun(t, p, b, c, steps...) }
			defer func() {
				for _, query := range []string{"DROP SERVER IF EXISTS publication_rt_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS publication_rt_fdw CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, query); err != nil {
						t.Error("foreign relation cleanup", err)
					}
				}
			}()
			run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0),
				fixture.Exec("CREATE TABLE publication_rt_s.t(id integer)", 0),
				fixture.Exec("CREATE UNLOGGED TABLE publication_rt_s.unlogged(id integer)", 0),
				fixture.Exec("CREATE TEMP TABLE publication_rt_temp(id integer)", 0),
				fixture.Exec("CREATE VIEW publication_rt_s.v AS SELECT 1 AS id", 0),
				fixture.Exec("CREATE MATERIALIZED VIEW publication_rt_s.mv AS SELECT 1 AS id", 1),
				fixture.Exec("CREATE SEQUENCE publication_rt_s.seq", 0),
				fixture.Exec("CREATE FOREIGN DATA WRAPPER publication_rt_fdw", 0),
				fixture.Exec("CREATE SERVER publication_rt_server FOREIGN DATA WRAPPER publication_rt_fdw", 0),
				fixture.Exec("CREATE FOREIGN TABLE publication_rt_s.ft(id integer) SERVER publication_rt_server", 0),
				fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLES IN SCHEMA publication_rt_s", 0),
				fixture.Exec("CREATE PUBLICATION publication_rt_all FOR ALL TABLES", 0))
			// Include temp and system candidates explicitly, rather than hiding
			// excluded kinds behind a WHERE clause that cannot select them.
			query := "SELECT pubname,schemaname,tablename FROM pg_catalog.pg_publication_tables WHERE pubname IN ('publication_rt_all','publication_rt_p') AND (schemaname='publication_rt_s' OR tablename='publication_rt_temp' OR (schemaname='pg_catalog' AND tablename='pg_class')) ORDER BY pubname,schemaname,tablename"
			state := func(rows ...[]any) {
				s := fixture.Select(query, []string{"pubname", "schemaname", "tablename"}, []string{"NAME", "NAME", "NAME"}, rows)
				run(s, fixture.Prepared(s))
			}
			want := [][]any{{"publication_rt_all", "publication_rt_s", "t"}, {"publication_rt_p", "publication_rt_s", "t"}}
			state(want...)
			for _, prepared := range []bool{false, true} {
				s := publicationTablesTemporaryDiagnostic()
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				state(want...)
			}
			run(fixture.Exec("DROP TABLE publication_rt_s.t", 0))
			state()
		})
	})
}
