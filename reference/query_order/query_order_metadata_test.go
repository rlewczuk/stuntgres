package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderMetadataAndPreparedFreshness(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
	p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
	p.Step(t, fixture.Exec("CREATE TABLE d(n BIGINT)", 0))
	for _, backend := range p.Backends() {
		func() {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			rows, err := conn.QueryContext(p.Ctx, `SELECT id AS "Mixed",a+b AS k FROM t ORDER BY k,"Mixed"`)
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 2 {
				rows.Close()
				t.Fatal(meta, err)
			}
			if meta[0].Name() != "Mixed" || meta[1].Name() != "k" || meta[0].DatabaseTypeName() != "INT4" || meta[1].DatabaseTypeName() != "INT4" {
				rows.Close()
				t.Fatal(meta)
			}
			nullable, known := meta[0].Nullable()
			if backend.PG {
				if known {
					rows.Close()
					t.Fatal("pq nullable unknown")
				}
			} else if nullable || !known {
				rows.Close()
				t.Fatal("alias lost physical NOT NULL")
			}
			if _, known := meta[1].Nullable(); known {
				rows.Close()
				t.Fatal("computed output invented nullable")
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			const q = "SELECT nextval('s') AS n FROM t WHERE id<4 ORDER BY n DESC"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{conn, stmt, q}
			for _, want := range [][][]any{{{int64(3)}, {int64(2)}, {int64(1)}}, {{int64(6)}, {int64(5)}, {int64(4)}}} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"n"}, []string{"INT8"}, want))
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(7)))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO d SELECT nextval('s') AS n FROM t WHERE id<4 ORDER BY n DESC", 3))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT n FROM d ORDER BY n DESC", []string{"n"}, []string{"INT8"}, referenceLimitIDs(10, 9, 8)))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(11)))
		}()
	}
}
