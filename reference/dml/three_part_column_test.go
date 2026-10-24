package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const threePartDeleteSQL = "delete from PUBLIC.NONCES where PUBLIC.NONCES.ID = cast($1 as uuid)"
const threePartUUID = "00000000-0000-0000-0000-000000000001"

func threePartDML(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "qa")
	fixture.SchemaReferenceCleanup(t, "qb")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

func threePartSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE SCHEMA qa", 0), fixture.Exec("CREATE SCHEMA qb", 0),
		fixture.Exec("CREATE TABLE qa.items(id int PRIMARY KEY,v int,grp int,denom int)", 0),
		fixture.Exec("CREATE TABLE qb.items(id int,v int)", 0),
		fixture.Exec("INSERT INTO qa.items VALUES(1,10,1,2),(2,20,1,0),(3,30,2,5)", 3),
		fixture.Exec("INSERT INTO qb.items VALUES(1,900),(2,800)", 2),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func TestReferenceThreePartDelete(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE public.nonces(id uuid,note text)", 0))
				run(fixture.Exec("INSERT INTO public.nonces VALUES($1,'match'),('00000000-0000-0000-0000-000000000002','other'),(NULL,'null')", 3, threePartUUID))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, s := range []fixture.Step{fixture.Exec(threePartDeleteSQL, 1, threePartUUID), fixture.Exec(threePartDeleteSQL, 0, threePartUUID), fixture.Exec(threePartDeleteSQL, 0, nil)} {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id::text,note FROM public.nonces ORDER BY note", []string{"id", "note"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "null"}, {"00000000-0000-0000-0000-000000000002", "other"}}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT id::text,note FROM public.nonces ORDER BY note", []string{"id", "note"}, []string{"TEXT", "TEXT"}, [][]any{{threePartUUID, "match"}, {nil, "null"}, {"00000000-0000-0000-0000-000000000002", "other"}}))
			})
		})
	}
}

func TestReferenceThreePartUpdate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
				threePartSeed(t, p, b)
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Select("UPDATE qa.items SET v=qa.items.v+$1 WHERE qa.items.id=$2 RETURNING qa.items.id,qa.items.v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(25)}}, 5, 2))
				run(fixture.Exec("UPDATE qa.items SET v=qa.items.v+1 WHERE qa.items.id=$1", 1, 1))
				for _, arg := range []any{99, nil} {
					run(fixture.Exec("UPDATE qa.items SET v=qa.items.v+1 WHERE qa.items.id=$1", 0, arg))
				}
				run(fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(25)}, {int64(3), int64(30)}}))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("UPDATE qa.items SET v=qa.items.id,id=qa.items.v WHERE qa.items.id=2 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(25), int64(2)}}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestReferenceThreePartDMLConsumers(t *testing.T) {
	threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
		threePartSeed(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("DELETE FROM qa.items WHERE EXISTS(SELECT 1 FROM qb.items WHERE qb.items.id=qa.items.id AND qb.items.v<0)", 0)
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("CREATE TABLE qa.dest(id int PRIMARY KEY,v int)", 0))
		s := fixture.Select("INSERT INTO qa.dest SELECT qa.items.id,qa.items.v FROM qa.items RETURNING qa.dest.id,qa.dest.v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}})
		s.Bag = true
		run(s)
		run(fixture.Select("INSERT INTO qa.dest VALUES(1,3) ON CONFLICT(id) DO UPDATE SET v=qa.dest.v+excluded.v WHERE qa.dest.id=1 RETURNING qa.dest.v", []string{"v"}, []string{"INT4"}, [][]any{{int64(13)}}))
		run(fixture.Select("INSERT INTO qa.dest VALUES(4,(SELECT qa.items.v FROM qa.items WHERE qa.items.id=1)) RETURNING qa.dest.id,qa.dest.v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(10)}}))
		s = fixture.Select("UPDATE qa.items SET v=qb.items.v FROM qb.items WHERE qa.items.id=qb.items.id RETURNING qa.items.id,qa.items.v,qb.items.v", []string{"id", "v", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(900), int64(900)}, {int64(2), int64(800), int64(800)}})
		s.Bag = true
		run(s)
		s = fixture.Select("DELETE FROM qa.items USING qb.items WHERE qa.items.id=qb.items.id RETURNING qa.items.id,qb.items.v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(900)}, {int64(2), int64(800)}})
		s.Bag = true
		run(s)
		run(fixture.Select("WITH removed AS (DELETE FROM qa.items WHERE (abs(CAST(qa.items.v AS int)))+1=31 AND qa.items.id=3 RETURNING qa.items.id) SELECT id FROM removed", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		run(fixture.Select("SELECT id FROM qa.items ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
	})
}

func TestReferenceThreePartAtomicity(t *testing.T) {
	for _, q := range []string{
		"UPDATE qa.items SET v=100/qa.items.denom",
		"DELETE FROM qa.items WHERE 100/qa.items.denom>0",
		"UPDATE qa.items SET v=qa.items.v+1 RETURNING 100/qa.items.denom",
		"DELETE FROM qa.items RETURNING 100/qa.items.denom",
	} {
		t.Run(q, func(t *testing.T) {
			threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
				threePartSeed(t, p, b)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
				}
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
			})
		})
	}
}

func TestReferenceThreePartMerge(t *testing.T) {
	threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
		threePartSeed(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("DELETE FROM qa.items WHERE id=2", 1))
		run(fixture.Exec("ALTER TABLE qb.items RENAME TO source", 0))
		s := fixture.Select("MERGE INTO qa.items USING qb.source ON qa.items.id=qb.source.id WHEN MATCHED AND qa.items.id=1 THEN UPDATE SET v=qa.items.v+qb.source.v WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(qb.source.id,qb.source.v) WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=qa.items.v+1 RETURNING qa.items.id,qa.items.v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(910)}, {int64(2), int64(800)}, {int64(3), int64(31)}})
		s.Bag = true
		run(s)
		run(fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(910)}, {int64(2), int64(800)}, {int64(3), int64(31)}}))
	})
}

func TestReferenceThreePartMergeDiagnostics(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
				threePartSeed(t, p, b)
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ALTER TABLE qb.items RENAME TO source", 0))
				var rows [][]any
				if populated {
					rows = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}
				} else {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DELETE FROM qa.items", 3))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DELETE FROM qb.source", 2))
				}
				for i, q := range []string{
					"MERGE INTO qa.items USING qb.source ON qa.items.id=qb.source.id WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(qb.source.id,qa.items.v)",
					"MERGE INTO qa.items USING qb.source ON qa.items.id=qb.source.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=qb.source.v",
				} {
					table := "items"
					position := 130
					if i == 1 {
						table = "source"
						position = 110
					}
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"" + table + "\""}
					want.Detail = "There is an entry for table \"" + table + "\", but it cannot be referenced from this part of the query."
					want.Position = fixture.Position(position)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, want)
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
					}
				}
			})
		})
	}
}

func TestReferenceThreePartCopy(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection, _ *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE copy_three(id int,keep bool)")
		c.exec(t, "INSERT INTO copy_three VALUES(99,true)")
		copyFilterImport(t, c, "COPY public.copy_three FROM STDIN WHERE public.copy_three.keep", "1\ttrue\n2\tfalse\n3\t\\N\n", 1)
		copyFilterFailure(t, c, "COPY public.copy_three FROM STDIN WHERE 10/public.copy_three.id>0", "4\ttrue\n0\ttrue\n", fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "COPY copy_three, line 2: \"0\ttrue\""})
		c.selectRows(t, "SELECT id,keep FROM copy_three ORDER BY id", []string{"id", "keep"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(99), true}})
		c.output(t, "COPY(SELECT public.copy_three.id,public.copy_three.keep FROM public.copy_three ORDER BY public.copy_three.id) TO STDOUT", "1\tt\n99\tt\n", 2)
	})
}
