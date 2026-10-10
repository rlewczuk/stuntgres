package reference_test

import "testing"

func TestReferencePGShDescriptionSnapshotsAndViews(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgsd_snapshot_role"})
	schemaReferenceCleanup(t, "pgsd_snapshot")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			run("CREATE SCHEMA pgsd_snapshot")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_snapshot_role", "pgsd_snapshot")
			run("COMMENT ON ROLE pgsd_snapshot_role IS 'old'")
			query := "SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260"
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: query}
			checkHeld := func(text *string) {
				t.Helper()
				rows := [][]any{}
				if text != nil {
					rows = [][]any{{id, int64(1260), *text}}
				}
				pgDescriptionExecutorRows(t, p, backend, held, pgShDescriptionSelect(query, rows, id))
			}
			checkHeld(new("old"))
			old, err := backend.db.QueryContext(p.ctx, query, id)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata := pgShDescriptionMetadata(t, old, backend.pg)
			run("COMMENT ON ROLE pgsd_snapshot_role IS 'new'")
			checkHeld(new("new"))
			pgShDescriptionCheck(t, p, backend, backend.db, id, new("new"))
			if !old.Next() {
				t.Fatal("missing retained row", old.Err())
			}
			var objoid, classoid int64
			var text string
			if err := old.Scan(&objoid, &classoid, &text); err != nil || objoid != id || classoid != 1260 || text != "old" {
				t.Fatal(objoid, classoid, text, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained cardinality", old.Err())
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			pgShDescriptionSavedMetadata(t, metadata, backend.pg)
			run("CREATE VIEW pgsd_snapshot.shared_view AS SELECT objoid,classoid,description,shobj_description(objoid,'pg_authid') AS inquiry FROM pg_catalog.pg_shdescription WHERE objoid='" + pgDescriptionOIDText(id) + "'::oid AND classoid=1260")
			view := referenceSelect("SELECT * FROM pgsd_snapshot.shared_view", []string{"objoid", "classoid", "description", "inquiry"}, []string{"OID", "OID", "TEXT", "TEXT"}, [][]any{{id, int64(1260), "new", "new"}})
			pgDescriptionRows(t, p, backend, view)
			pgDescriptionRows(t, p, backend, referencePrepared(view))
			for _, q := range []string{
				"SELECT a.description FROM pg_catalog.pg_shdescription a JOIN pg_catalog.pg_shdescription b ON a.objoid=b.objoid AND a.classoid=b.classoid WHERE a.objoid=$1 AND a.classoid=1260",
				"SELECT description FROM (SELECT objoid,classoid,description FROM pg_catalog.pg_shdescription) d WHERE objoid=$1 AND classoid=1260",
			} {
				s := referenceSelect(q, []string{"description"}, []string{"TEXT"}, [][]any{{"new"}}, id)
				s.bag = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
			}
			run("CREATE TABLE pgsd_snapshot.copied(objoid OID,classoid OID,description TEXT)")
			beforeRemoval, err := backend.db.QueryContext(p.ctx, "SELECT description FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", id)
			if err != nil {
				t.Fatal(err)
			}
			defer beforeRemoval.Close()
			removalMetadata, err := beforeRemoval.ColumnTypes()
			if err != nil || len(removalMetadata) != 1 || removalMetadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal(removalMetadata, err)
			}
			for _, present := range []bool{true, false} {
				count := int64(1)
				if !present {
					run("COMMENT ON ROLE pgsd_snapshot_role IS NULL")
					count = 0
				}
				s := referenceSelect("SELECT count(*) FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", []string{"count"}, []string{"INT8"}, [][]any{{count}}, id)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO pgsd_snapshot.copied SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", count, id))
			}
			pgDescriptionRetainedText(t, beforeRemoval, "new")
			if removalMetadata[0].Name() != "description" || removalMetadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal("removal changed retained metadata", removalMetadata)
			}
			pgDescriptionRows(t, p, backend, pgShDescriptionSelect("SELECT * FROM pgsd_snapshot.copied", [][]any{{id, int64(1260), "new"}}))
			checkHeld(nil)
			pgDescriptionRows(t, p, backend, referenceSelect("SELECT * FROM pgsd_snapshot.shared_view", []string{"objoid", "classoid", "description", "inquiry"}, []string{"OID", "OID", "TEXT", "TEXT"}, [][]any{}))
			run("COMMENT ON ROLE pgsd_snapshot_role IS 'before drop'")
			beforeDrop, err := backend.db.QueryContext(p.ctx, "SELECT description FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", id)
			if err != nil {
				t.Fatal(err)
			}
			defer beforeDrop.Close()
			run("DROP ROLE pgsd_snapshot_role")
			pgDescriptionRetainedText(t, beforeDrop, "before drop")
			checkHeld(nil)
			newID := pgShDescriptionCreateRole(t, p, backend, "pgsd_snapshot_role", "pgsd_snapshot")
			if newID == id {
				t.Fatal("reused dropped identity")
			}
			checkHeld(nil)
			pgShDescriptionCheck(t, p, backend, backend.db, newID, nil)
			run("DROP ROLE pgsd_snapshot_role")
		})
	}
}
