package reference_test

import (
	"context"
	"testing"
)

func TestReferencePGShDescriptionRoleLifecycle(t *testing.T) { pgShDescriptionLifecycleScenario(t) }
func TestReferencePGShDescriptionFeatureSmoke(t *testing.T)  { pgShDescriptionLifecycleScenario(t) }

func pgShDescriptionLifecycleScenario(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgsd_lifecycle_role", "pgsd_lifecycle_reader"})
	schemaReferenceCleanup(t, "pgsd_lifecycle")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			run("CREATE SCHEMA pgsd_lifecycle")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_lifecycle_role", "pgsd_lifecycle")
			pgShDescriptionCheck(t, p, backend, backend.db, id, nil)
			pgDescriptionRows(t, p, backend, referenceSelect("SELECT oid,relnatts FROM pg_catalog.pg_class WHERE oid=2396", []string{"oid", "relnatts"}, []string{"OID", "INT2"}, [][]any{{int64(2396), int64(3)}}))
			query := "SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260"
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: query}
			for _, tc := range []struct {
				literal string
				text    *string
			}{
				{"E'quote'' slash\\\\\\n雪'", new("quote' slash\\\n雪")},
				{"'replacement'", new("replacement")},
				{"NULL", nil}, {"''", nil}, {"' '", new(" ")},
			} {
				run("COMMENT ON ROLE pgsd_lifecycle_role IS " + tc.literal)
				pgShDescriptionCheck(t, p, backend, backend.db, id, tc.text)
				rows := [][]any{}
				if tc.text != nil {
					rows = [][]any{{id, int64(1260), *tc.text}}
				}
				pgDescriptionExecutorRows(t, p, backend, held, pgShDescriptionSelect(query, rows, id))
			}
			inquiry := referenceSelect("SELECT description,shobj_description(objoid,'pg_authid') AS shared,obj_description(objoid,'pg_authid') AS local FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", []string{"description", "shared", "local"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{" ", " ", nil}}, id)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, inquiry)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(inquiry))
			pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, id))
			run("CREATE ROLE pgsd_lifecycle_reader")
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET ROLE")
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET ROLE pgsd_lifecycle_reader", 0))
				pgShDescriptionCheck(t, p, backend, conn, id, new(" "))
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("RESET ROLE", 0))
			}()
			run("DROP ROLE pgsd_lifecycle_role")
			pgShDescriptionCheck(t, p, backend, backend.db, id, nil)
			newID := pgShDescriptionCreateRole(t, p, backend, "pgsd_lifecycle_role", "pgsd_lifecycle")
			if newID == id {
				t.Fatal("same-name role reused identity", id)
			}
			pgShDescriptionCheck(t, p, backend, backend.db, newID, nil)
			run("COMMENT ON ROLE pgsd_lifecycle_role IS 'recreated'")
			pgShDescriptionCheck(t, p, backend, backend.db, newID, new("recreated"))
			pgShDescriptionCheck(t, p, backend, backend.db, id, nil)
			run("DROP ROLE pgsd_lifecycle_role")
			run("DROP ROLE pgsd_lifecycle_reader")
			pgShDescriptionCheck(t, p, backend, backend.db, newID, nil)
		})
	}
}

func TestReferencePGShDescriptionLocalSeparation(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgsd_separation_role"})
	schemaReferenceCleanup(t, "pgsd_separation")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			run("CREATE SCHEMA pgsd_separation")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_separation_role", "pgsd_separation")
			for _, q := range []string{"COMMENT ON ROLE pgsd_separation_role IS 'shared only'", "CREATE TABLE pgsd_separation.pgsd_local_table(a INT)", "CREATE SEQUENCE pgsd_separation.pgsd_local_sequence", "COMMENT ON TABLE pgsd_separation.pgsd_local_table IS 'local table'", "COMMENT ON COLUMN pgsd_separation.pgsd_local_table.a IS 'local column'", "COMMENT ON SEQUENCE pgsd_separation.pgsd_local_sequence IS 'local sequence'", "COMMENT ON SCHEMA pgsd_separation IS 'local schema'"} {
				run(q)
			}
			table := pgDescriptionRelationID(t, p, backend, "pgsd_local_table")
			sequence := pgDescriptionRelationID(t, p, backend, "pgsd_local_sequence")
			var namespace int64
			if err := backend.db.QueryRowContext(p.ctx, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", table).Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{table, int64(1259), int64(0), "local table"}, {table, int64(1259), int64(1), "local column"}}, table))
			pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{sequence, int64(1259), int64(0), "local sequence"}}, sequence))
			pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=2615", [][]any{{namespace, int64(2615), int64(0), "local schema"}}, namespace))
			pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, id))
			pgShDescriptionCheck(t, p, backend, backend.db, id, new("shared only"))
			for _, address := range [][2]int64{{table, 1259}, {sequence, 1259}, {namespace, 2615}, {114, 1247}, {950, 3456}, {4518, 2607}} {
				for _, prepared := range []bool{false, true} {
					s := pgShDescriptionSelect("SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=$2", [][]any{}, address[0], address[1])
					s.prepared = prepared
					pgDescriptionRows(t, p, backend, s)
				}
			}
			s := referenceSelect("SELECT shobj_description($1,'pg_class') AS wrong,obj_description($2,'pg_class') AS local", []string{"wrong", "local"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "local table"}}, id, table)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
			run("DROP ROLE pgsd_separation_role")
		})
	}
}
