package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func pgDescriptionRetainedText(t *testing.T, rows *sql.Rows, want string) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"description"}) {
		t.Fatal(columns, err)
	}
	if !rows.Next() {
		t.Fatal("missing retained row", rows.Err())
	}
	var text any
	if err := rows.Scan(&text); err != nil || text != want {
		t.Fatal("retained description", text, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cardinality", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferencePGDescriptionSnapshots(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			run("CREATE TABLE pgd_snapshot(a INT)")
			run("COMMENT ON TABLE pgd_snapshot IS 'old'")
			oid := pgDescriptionRelationID(t, p, backend, "pgd_snapshot")
			query := "SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259"
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: query}
			checkHeld := func(text string) {
				t.Helper()
				pgDescriptionExecutorRows(t, p, backend, held, pgDescriptionSelect(query, [][]any{{oid, int64(1259), int64(0), text}}, oid))
			}
			checkHeld("old")
			old, err := backend.db.QueryContext(p.ctx, "SELECT description FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", oid)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal(metadata, err)
			}
			run("COMMENT ON TABLE pgd_snapshot IS 'new'")
			checkHeld("new")
			pgDescriptionRetainedText(t, old, "old")
			if metadata[0].Name() != "description" || metadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal("saved metadata changed", metadata)
			}
			for _, q := range []string{
				"SELECT description,obj_description(objoid,'pg_class') AS inquiry FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259 AND objsubid=0",
			} {
				s := referenceSelect(q, []string{"description", "inquiry"}, []string{"TEXT", "TEXT"}, [][]any{{"new", "new"}}, oid)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
			}
			run("CREATE VIEW pgd_snapshot_view AS SELECT description,obj_description(objoid,'pg_class') AS inquiry FROM pg_catalog.pg_description WHERE objoid='" + pgDescriptionOIDText(oid) + "'::oid AND classoid=1259 AND objsubid=0")
			run("COMMENT ON TABLE pgd_snapshot IS 'view fresh'")
			s := referenceSelect("SELECT * FROM pgd_snapshot_view", []string{"description", "inquiry"}, []string{"TEXT", "TEXT"}, [][]any{{"view fresh", "view fresh"}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
			join := referenceSelect("SELECT d.description,c.relname FROM pg_catalog.pg_description d JOIN pg_catalog.pg_class c ON d.objoid=c.oid WHERE d.objoid=$1 AND d.classoid=1259 AND d.objsubid=0 ORDER BY d.description", []string{"description", "relname"}, []string{"TEXT", "NAME"}, [][]any{{"view fresh", "pgd_snapshot"}}, oid)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, join)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(join))
			group := referenceSelect("SELECT description,count(*) FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259 GROUP BY description ORDER BY description", []string{"description", "count"}, []string{"TEXT", "INT8"}, [][]any{{"view fresh", int64(1)}}, oid)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, group)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(group))
			empty := pgDescriptionSelect(query, [][]any{}, nil)
			empty.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.pg) }
			pgDescriptionRows(t, p, backend, empty)
			pgDescriptionRows(t, p, backend, referencePrepared(empty))
			beforeDrop, err := backend.db.QueryContext(p.ctx, "SELECT description FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", oid)
			if err != nil {
				t.Fatal(err)
			}
			defer beforeDrop.Close()
			run("DROP TABLE pgd_snapshot")
			pgDescriptionRetainedText(t, beforeDrop, "view fresh")
			pgDescriptionExecutorRows(t, p, backend, held, pgDescriptionSelect(query, [][]any{}, oid))
		})
	}
}
