package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINTextFeatureSmoke(t *testing.T) {
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		seed := func() {
			run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
			run(fixture.Exec(`CREATE TABLE text_smoke(id integer NOT NULL,t text,v varchar,b bpchar,c "char",n name,x bytea)`, 0))
			run(fixture.Exec("CREATE INDEX text_smoke_i ON text_smoke USING gin(t public.text_ops,v public.varchar_ops,b public.bpchar_ops,c public.char_ops,n public.name_ops,x public.bytea_ops)", 0))
			run(fixture.Exec(`INSERT INTO text_smoke VALUES(1,'ab','ab','ab','b','ab','\x80'),(2,'ab ','ab ','ab ','\200','ab ','\xff'),(3,'','','','','','\x'),(4,NULL,NULL,NULL,NULL,NULL,NULL)`, 4))
		}
		seed()
		queries := []string{"SELECT id FROM text_smoke WHERE t=$1::text ORDER BY id", "SELECT id FROM text_smoke WHERE v=$1::varchar ORDER BY id", "SELECT id FROM text_smoke WHERE b=$1::bpchar ORDER BY id"}
		held := make([]fixture.HeldStatement, 3)
		for i, q := range queries {
			held[i] = classHeld(t, p, conn, q)
		}
		check := func() {
			for i, q := range queries {
				ids := []int64{1}
				if i == 2 {
					ids = []int64{1, 2}
				}
				s := ginIDs(q, ids, "ab")
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
				s = ginIDs(q, nil, nil)
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
			for _, probe := range []struct {
				where string
				ids   []int64
			}{{`c>'b'::"char"`, []int64{2}}, {"n>'ab'::name", []int64{2}}, {`x>='\x80'::bytea`, []int64{1, 2}}, {"t<>'ab'::text", []int64{2, 3}}, {"t IS NULL", []int64{4}}} {
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_smoke WHERE "+probe.where+" ORDER BY id", probe.ids))
			}
			for i, tc := range textCases {
				values := [][]any{{int64(1), "ab"}, {int64(2), "ab "}, {int64(3), ""}, {int64(4), nil}}
				if i == 3 {
					values = [][]any{{int64(1), "b"}, {int64(2), `\200`}, {int64(3), ""}, {int64(4), nil}}
				}
				if i == 5 {
					values = [][]any{{int64(1), []byte{128}}, {int64(2), []byte{255}}, {int64(3), []byte{}}, {int64(4), nil}}
				}
				column := []string{"t", "v", "b", "c", "n", "x"}[i]
				for _, empty := range []bool{false, true} {
					q := "SELECT id," + column + " AS v FROM text_smoke"
					rows := values
					if empty {
						q += " WHERE false"
						rows = nil
					}
					q += " ORDER BY id"
					s := textValueStep(b, q, tc, rows)
					classRoutes(t, p, b, conn, s)
					h := classHeld(t, p, conn, q)
					fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				}
			}
			textGraph(t, p, b, conn, "public")
		}
		check()
		ginUtilityError(t, p, b, conn, "CREATE INDEX text_smoke_bad ON text_smoke USING gin(t text_ops,x text_ops)", fixture.ControlsReferenceDiagnostic("42804", `operator class "text_ops" does not accept data type bytea`))
		compoundIndexAbsent(run, "text_smoke", "text_smoke_bad", 1)
		d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: text <-> text")
		d.Hint = compoundOperatorHint
		d.Position = fixture.Position(18)
		s := fixture.ErrorDiagnostic("SELECT 'a'::text <-> 'b'::text", d)
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		s.Prepared = true
		s.ErrorStage = "prepare"
		run(s)
		if b.PG {
			if emitted != "" {
				run(fixture.Exec("DROP TABLE text_smoke", 0))
				run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
				if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
					t.Fatalf("PG rejected actual native smoke Dump: %v", err)
				}
			}
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		check()
		insert := `INSERT INTO text_smoke VALUES(5,'ab','ab','ab','b','ab','\x80')`
		insertHeld := classHeld(t, p, conn, insert)
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insert, 1))
		fixture.RunStep(t, p.Ctx, held[0], b.Recorder, ginIDs(queries[0], []int64{1, 5}, "ab"))
		fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], []int64{1, 2, 5}, "ab"))
		run(fixture.Prepared(fixture.Exec("UPDATE text_smoke SET t='z' WHERE id=5", 1)))
		fixture.RunStep(t, p.Ctx, held[0], b.Recorder, ginIDs(queries[0], []int64{1}, "ab"))
		run(fixture.Prepared(fixture.Exec("DELETE FROM text_smoke WHERE id=5", 1)))
		check()
	})
}
