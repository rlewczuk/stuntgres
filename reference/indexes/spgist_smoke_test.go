package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTFeatureSmoke(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE spgist_smoke(id integer,p point,s text)", 0))
		run(fixture.Exec("INSERT INTO spgist_smoke VALUES(1,'(0,0)','a'),(2,'(3,4)','aa'),(3,'(0,5)','b'),(4,NULL,NULL)", 4))
		const q = "SELECT id,p <-> point '(0,0)' AS d FROM spgist_smoke ORDER BY p <-> point '(0,0)' NULLS LAST,id"
		const textq = "SELECT id FROM spgist_smoke WHERE s ^@ $1::text ORDER BY id"
		held, textHeld := classHeld(t, p, conn, q), classHeld(t, p, conn, textq)
		run(fixture.Exec("CREATE INDEX spgist_smoke_i ON spgist_smoke USING spgist(p) INCLUDE(id) WITH(fillfactor=80)", 0))
		run(fixture.Exec("CREATE INDEX spgist_smoke_kd ON spgist_smoke USING spgist(p kd_point_ops)", 0))
		run(fixture.Exec("CREATE INDEX spgist_smoke_text ON spgist_smoke USING spgist(s text_ops)", 0))
		check := func(rows [][]any, ids [][]any) {
			step := fixture.Select(q, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, rows)
			classRoutes(t, p, b, conn, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			step = fixture.Select(textq, []string{"id"}, []string{"INT4"}, ids, "a")
			classRoutes(t, p, b, conn, step)
			fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, step)
		}
		before := [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}, {int64(3), float64(5)}, {int64(4), nil}}
		check(before, [][]any{{int64(1)}, {int64(2)}})
		classRoutes(t, p, b, conn, fixture.Select(q+" LIMIT 3", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, before[:3]))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_smoke WHERE s ~<~ 'b' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT m.amname,c.opcname,pg_index_column_has_property(r.oid,1,'distance_orderable') AS d,pg_index_column_has_property(r.oid,1,'orderable') AS o FROM pg_index i JOIN pg_class r ON r.oid=i.indexrelid JOIN pg_am m ON m.oid=r.relam JOIN pg_opclass c ON c.oid=i.indclass[0] WHERE r.relname IN('spgist_smoke_i','spgist_smoke_kd','spgist_smoke_text') ORDER BY r.relname", []string{"amname", "opcname", "d", "o"}, []string{"NAME", "NAME", "BOOL", "BOOL"}, [][]any{{"spgist", "quad_point_ops", true, false}, {"spgist", "kd_point_ops", true, false}, {"spgist", "text_ops", false, false}}))
		run(fixture.Exec("INSERT INTO spgist_smoke VALUES(5,'(1,0)','ab')", 1))
		run(fixture.Exec("UPDATE spgist_smoke SET p='(6,8)' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM spgist_smoke WHERE id=3", 1))
		after := [][]any{{int64(1), float64(0)}, {int64(5), float64(1)}, {int64(2), float64(10)}, {int64(4), nil}}
		check(after, [][]any{{int64(1)}, {int64(2)}, {int64(5)}})
		run(fixture.Exec("CREATE TABLE spgist_smoke_ex(s text,CONSTRAINT spgist_smoke_exclusion EXCLUDE USING spgist(s WITH =))", 0))
		run(fixture.Exec("INSERT INTO spgist_smoke_ex VALUES('a')", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO spgist_smoke_ex VALUES('a')", fixture.ExclusionReferenceConflict("spgist_smoke_ex", "spgist_smoke_exclusion", "s", "a", "a")))
		d := fixture.ControlsReferenceDiagnostic("42704", `data type integer has no default operator class for access method "spgist"`)
		d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
		run(fixture.ErrorDiagnostic("CREATE INDEX spgist_bad ON spgist_smoke USING spgist(id)", d))
		run(fixture.ErrorDiagnostic("CREATE INDEX spgist_bad ON spgist_smoke USING spgist(p) WITH(buffering=on)", fixture.ControlsReferenceDiagnostic("22023", `unrecognized parameter "buffering"`)))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='spgist_bad'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("REINDEX INDEX spgist_smoke_i", 0))
		if !b.PG {
			dump := fixture.CheckDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(after, [][]any{{int64(1)}, {int64(2)}, {int64(5)}})
		}
		run(fixture.Exec("INSERT INTO spgist_smoke VALUES(6,'(2,0)','ac')", 1))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_smoke WHERE p ~= point '(2,0)'", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))
		run(fixture.Exec("DROP INDEX spgist_smoke_i", 0))
	})
}
