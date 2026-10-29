package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Definitions are independently specified from PostgreSQL 18's released SQL
// contract. Backend subtests also permit oracle-first discovery without a filter.
func publicationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "publication_rt_s")
	fixture.SchemaReferenceCleanup(t, "publication_rt_other")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				rows, err := b.DB.QueryContext(ctx, "SELECT pubname FROM pg_catalog.pg_publication WHERE pubname LIKE 'publication_rt_%'")
				if err != nil {
					t.Error("publication cleanup inventory", err)
					return
				}
				var names []string
				for rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						t.Error(err)
						break
					}
					names = append(names, name)
				}
				if err := rows.Err(); err != nil {
					t.Error(err)
				}
				rows.Close()
				for _, name := range names {
					if _, err := b.DB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
						t.Error("publication cleanup", err)
					}
				}
			}()
			body(t, p, b)
		})
	}
}

func publicationRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, steps ...fixture.Step) {
	t.Helper()
	for _, s := range steps {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func publicationDiagnostic(query, state, message, detail, hint string) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic(state, message)
	d.Detail, d.Hint = detail, hint
	return fixture.ErrorDiagnostic(query, d)
}

func publicationMembers(name string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT schemaname,tablename FROM pg_catalog.pg_publication_tables WHERE pubname=$1 ORDER BY schemaname,tablename", []string{"schemaname", "tablename"}, []string{"NAME", "NAME"}, rows, name)
}

func publicationDefinitions(name string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT tablename,attnames::text,rowfilter FROM pg_catalog.pg_publication_tables WHERE pubname=$1 ORDER BY schemaname,tablename", []string{"tablename", "attnames", "rowfilter"}, []string{"NAME", "TEXT", "TEXT"}, rows, name)
}

func publicationOptions(name string, all, insert, update, delete, truncate, root bool, generated string) fixture.Step {
	return fixture.Select("SELECT pubname,puballtables,pubinsert,pubupdate,pubdelete,pubtruncate,pubviaroot,pubgencols FROM pg_catalog.pg_publication WHERE pubname=$1", []string{"pubname", "puballtables", "pubinsert", "pubupdate", "pubdelete", "pubtruncate", "pubviaroot", "pubgencols"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "CHAR"}, [][]any{{name, all, insert, update, delete, truncate, root, generated}}, name)
}

func publicationNamespace(name string, schemas ...string) fixture.Step {
	var rows [][]any
	for _, schema := range schemas {
		rows = append(rows, []any{schema, true, true})
	}
	return fixture.Select("SELECT n.nspname,pn.oid>0 AS valid_oid,pn.pnpubid=p.oid AS linked FROM pg_catalog.pg_publication_namespace pn JOIN pg_catalog.pg_publication p ON p.oid=pn.pnpubid JOIN pg_catalog.pg_namespace n ON n.oid=pn.pnnspid WHERE p.pubname=$1 ORDER BY n.nspname", []string{"nspname", "valid_oid", "linked"}, []string{"NAME", "BOOL", "BOOL"}, rows, name)
}

func publicationConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
