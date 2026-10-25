package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGShDescriptionRoleLifecycle(t *testing.T) { pgShDescriptionLifecycleScenario(t) }
func TestReferencePGShDescriptionFeatureSmoke(t *testing.T)  { pgShDescriptionLifecycleScenario(t) }

func pgShDescriptionLifecycleScenario(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgsd_lifecycle_role", "pgsd_lifecycle_reader"})
	fixture.SchemaReferenceCleanup(t, "pgsd_lifecycle")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
			}
			run("CREATE SCHEMA pgsd_lifecycle")
			id := fixture.PGShDescriptionCreateRole(t, p, backend, "pgsd_lifecycle_role", "pgsd_lifecycle")
			pgShDescriptionCheck(t, p, backend, backend.DB, id, nil)
			fixture.PgDescriptionRows(t, p, backend, fixture.Select("SELECT oid,relnatts FROM pg_catalog.pg_class WHERE oid=2396", []string{"oid", "relnatts"}, []string{"OID", "INT2"}, [][]any{{int64(2396), int64(3)}}))
			query := "SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260"
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: query}
			for _, tc := range []struct {
				literal string
				text    *string
			}{
				{"E'quote'' slash\\\\\\n雪'", new("quote' slash\\\n雪")},
				{"'replacement'", new("replacement")},
				{"NULL", nil}, {"''", nil}, {"' '", new(" ")},
			} {
				run("COMMENT ON ROLE pgsd_lifecycle_role IS " + tc.literal)
				pgShDescriptionCheck(t, p, backend, backend.DB, id, tc.text)
				rows := [][]any{}
				if tc.text != nil {
					rows = [][]any{{id, int64(1260), *tc.text}}
				}
				fixture.PgDescriptionExecutorRows(t, p, backend, held, pgShDescriptionSelect(query, rows, id))
			}
			inquiry := fixture.Select("SELECT description,shobj_description(objoid,'pg_authid') AS shared,obj_description(objoid,'pg_authid') AS local FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", []string{"description", "shared", "local"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{" ", " ", nil}}, id)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, inquiry)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(inquiry))
			fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, id))
			run("CREATE ROLE pgsd_lifecycle_reader")
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET ROLE")
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET ROLE pgsd_lifecycle_reader", 0))
				pgShDescriptionCheck(t, p, backend, conn, id, new(" "))
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("RESET ROLE", 0))
			}()
			run("DROP ROLE pgsd_lifecycle_role")
			pgShDescriptionCheck(t, p, backend, backend.DB, id, nil)
			newID := fixture.PGShDescriptionCreateRole(t, p, backend, "pgsd_lifecycle_role", "pgsd_lifecycle")
			if newID == id {
				t.Fatal("same-name role reused identity", id)
			}
			pgShDescriptionCheck(t, p, backend, backend.DB, newID, nil)
			run("COMMENT ON ROLE pgsd_lifecycle_role IS 'recreated'")
			pgShDescriptionCheck(t, p, backend, backend.DB, newID, new("recreated"))
			pgShDescriptionCheck(t, p, backend, backend.DB, id, nil)
			run("DROP ROLE pgsd_lifecycle_role")
			run("DROP ROLE pgsd_lifecycle_reader")
			pgShDescriptionCheck(t, p, backend, backend.DB, newID, nil)
		})
	}
}

func TestReferencePGShDescriptionLocalSeparation(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgsd_separation_role"})
	fixture.SchemaReferenceCleanup(t, "pgsd_separation")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
			}
			run("CREATE SCHEMA pgsd_separation")
			id := fixture.PGShDescriptionCreateRole(t, p, backend, "pgsd_separation_role", "pgsd_separation")
			for _, q := range []string{"COMMENT ON ROLE pgsd_separation_role IS 'shared only'", "CREATE TABLE pgsd_separation.pgsd_local_table(a INT)", "CREATE SEQUENCE pgsd_separation.pgsd_local_sequence", "COMMENT ON TABLE pgsd_separation.pgsd_local_table IS 'local table'", "COMMENT ON COLUMN pgsd_separation.pgsd_local_table.a IS 'local column'", "COMMENT ON SEQUENCE pgsd_separation.pgsd_local_sequence IS 'local sequence'", "COMMENT ON SCHEMA pgsd_separation IS 'local schema'"} {
				run(q)
			}
			table := fixture.PGDescriptionRelationID(t, p, backend, "pgsd_local_table")
			sequence := fixture.PGDescriptionRelationID(t, p, backend, "pgsd_local_sequence")
			var namespace int64
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", table).Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{table, int64(1259), int64(0), "local table"}, {table, int64(1259), int64(1), "local column"}}, table))
			fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{sequence, int64(1259), int64(0), "local sequence"}}, sequence))
			fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=2615", [][]any{{namespace, int64(2615), int64(0), "local schema"}}, namespace))
			fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, id))
			pgShDescriptionCheck(t, p, backend, backend.DB, id, new("shared only"))
			for _, address := range [][2]int64{{table, 1259}, {sequence, 1259}, {namespace, 2615}, {114, 1247}, {950, 3456}, {4518, 2607}} {
				for _, prepared := range []bool{false, true} {
					s := pgShDescriptionSelect("SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=$2", [][]any{}, address[0], address[1])
					s.Prepared = prepared
					fixture.PgDescriptionRows(t, p, backend, s)
				}
			}
			s := fixture.Select("SELECT shobj_description($1,'pg_class') AS wrong,obj_description($2,'pg_class') AS local", []string{"wrong", "local"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "local table"}}, id, table)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
			run("DROP ROLE pgsd_separation_role")
		})
	}
}
