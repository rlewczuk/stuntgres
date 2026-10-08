package reference_test

import "testing"

func TestReferenceQueryOrderMetadataAndPreparedFreshness(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
	p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
	p.step(t, referenceExec("CREATE TABLE d(n BIGINT)", 0))
	for _, backend := range p.backends() {
		func() {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			rows, err := conn.QueryContext(p.ctx, `SELECT id AS "Mixed",a+b AS k FROM t ORDER BY k,"Mixed"`)
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
			if backend.pg {
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
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{conn, stmt, q}
			for _, want := range [][][]any{{{int64(3)}, {int64(2)}, {int64(1)}}, {{int64(6)}, {int64(5)}, {int64(4)}}} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(q, []string{"n"}, []string{"INT8"}, want))
			}
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(7)))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("INSERT INTO d SELECT nextval('s') AS n FROM t WHERE id<4 ORDER BY n DESC", 3))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT n FROM d ORDER BY n DESC", []string{"n"}, []string{"INT8"}, referenceLimitIDs(10, 9, 8)))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(11)))
		}()
	}
}
