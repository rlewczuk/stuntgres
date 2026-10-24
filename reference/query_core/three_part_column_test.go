package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func threePartQuery(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	for _, schema := range []string{"qa", "qb", "qc", "MixSchema"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SCHEMA qa", 0), fixture.Exec("CREATE SCHEMA qb", 0),
			fixture.Exec("CREATE TABLE qa.items(id int,v int,grp int,denom int)", 0),
			fixture.Exec("CREATE TABLE qb.items(id int,v int)", 0),
			fixture.Exec("INSERT INTO qa.items VALUES(1,10,1,2),(2,20,1,0),(3,30,2,5)", 3),
			fixture.Exec("INSERT INTO qb.items VALUES(1,900),(2,800)", 2),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		body(p, b)
	}
}

func TestReferenceThreePartSelect(t *testing.T) {
	threePartQuery(t, func(p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, path := range []string{"qb,qa", "qa,qb"} {
			run(fixture.Exec("SET search_path="+path, 0))
			for _, prepared := range []bool{false, true} {
				for _, arg := range []any{2, 99, nil} {
					var rows [][]any
					if arg == 2 {
						rows = [][]any{{int64(3), int64(30)}, {int64(2), int64(20)}}
					}
					s := fixture.Select("SELECT qa.items.id,qa.items.v FROM qa.items WHERE qa.items.id >= $1 ORDER BY qa.items.id DESC", []string{"id", "v"}, []string{"INT4", "INT4"}, rows, arg)
					s.Prepared = prepared
					run(s)
				}
			}
		}
		run(fixture.Exec(`CREATE SCHEMA "MixSchema"`, 0))
		run(fixture.Exec(`CREATE TABLE "MixSchema"."MixTable"("MixID" int)`, 0))
		run(fixture.Exec(`INSERT INTO "MixSchema"."MixTable" VALUES(7)`, 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(`SELECT "MixSchema"."MixTable"."MixID" FROM "MixSchema"."MixTable" WHERE "MixSchema"."MixTable"."MixID"=$1 ORDER BY "MixSchema"."MixTable"."MixID"`, []string{"MixID"}, []string{"INT4"}, [][]any{{int64(7)}}, 7)
			s.Prepared = prepared
			run(s)
		}
	})
}

func TestReferenceThreePartQueryConsumers(t *testing.T) {
	threePartQuery(t, func(p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		steps := []fixture.Step{
			fixture.Select("SELECT CAST(qa.items.v AS bigint) AS big,abs(qa.items.v) AS absolute,qa.items.v+1 AS plus,qa.items.id>1 AND (qa.items.v=20 OR qa.items.grp=2) AS yes FROM qa.items ORDER BY qa.items.id", []string{"big", "absolute", "plus", "yes"}, []string{"INT8", "INT4", "INT4", "BOOL"}, [][]any{{int64(10), int64(10), int64(11), false}, {int64(20), int64(20), int64(21), true}, {int64(30), int64(30), int64(31), true}}),
			fixture.Select("SELECT qa.items.id,qa.items.v,qb.items.v FROM qa.items JOIN qb.items ON qa.items.id=qb.items.id ORDER BY qa.items.id", []string{"id", "v", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(900)}, {int64(2), int64(20), int64(800)}}),
			fixture.Select("SELECT qa.items.grp,sum(qa.items.v) FROM qa.items GROUP BY qa.items.grp HAVING sum(qa.items.v)>25 ORDER BY qa.items.grp", []string{"grp", "sum"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(30)}, {int64(2), int64(30)}}),
			fixture.Select("SELECT DISTINCT ON(qa.items.grp) qa.items.grp,qa.items.id FROM qa.items ORDER BY qa.items.grp,qa.items.id", []string{"grp", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(3)}}),
			fixture.Select("SELECT qa.items.id,sum(qa.items.v) OVER(PARTITION BY qa.items.grp ORDER BY qa.items.id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM qa.items ORDER BY qa.items.id", []string{"id", "sum"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(30)}, {int64(3), int64(30)}}),
			fixture.Select("SELECT qa.items.id FROM qa.items WHERE EXISTS(SELECT 1 FROM qb.items WHERE qb.items.id=qa.items.id) ORDER BY qa.items.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT qa.items.id FROM qa.items WHERE qa.items.id IN(SELECT qb.items.id FROM qb.items WHERE qb.items.id=qa.items.id) ORDER BY qa.items.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT qa.items.id,(SELECT (SELECT qa.items.v)) AS nested FROM qa.items ORDER BY qa.items.id", []string{"id", "nested"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}),
			fixture.Select("SELECT qa.items.id,(SELECT qa.items.denom FROM qb.items AS items WHERE items.id=1) AS outer_value FROM qa.items ORDER BY qa.items.id", []string{"id", "outer_value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(0)}, {int64(3), int64(5)}}),
			fixture.Select("SELECT qa.items.id,x.v FROM qa.items CROSS JOIN LATERAL unnest(ARRAY[qa.items.v]) AS x(v) ORDER BY qa.items.id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}),
			fixture.Select("WITH x AS(SELECT qa.items.id FROM qa.items) SELECT id FROM x UNION ALL SELECT qb.items.id FROM qb.items ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}}),
		}
		for _, s := range steps {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SEQUENCE qa.seq", 0),
			fixture.Select("SELECT setval('qa.seq',7)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(7)}}),
			fixture.Exec("CREATE TABLE qa.seqnames(seqname regclass)", 0), fixture.Exec("INSERT INTO qa.seqnames VALUES('qa.seq')", 1),
		} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		for _, q := range []string{
			"SELECT currval(qa.seqnames.seqname) FROM qa.seqnames",
			"SELECT currval(CAST(qa.seqnames.seqname AS regclass)) FROM qa.seqnames",
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"currval"}, []string{"INT8"}, [][]any{{int64(7)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
		}
	})
}

func TestReferenceThreePartDiagnostics(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			threePartQuery(t, func(p *fixture.Pair, b fixture.Backend) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				for _, q := range []string{"CREATE SCHEMA qc", "CREATE TABLE qc.items(id int)", `CREATE SCHEMA "MixSchema"`, `CREATE TABLE "MixSchema"."MixTable"("MixID" int)`} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				defer conn.ExecContext(p.Ctx, "DROP SCHEMA qc CASCADE")
				if !populated {
					for _, q := range []string{"DELETE FROM qa.items", "DELETE FROM qb.items"} {
						if _, err := conn.ExecContext(p.Ctx, q); err != nil {
							t.Fatal(err)
						}
					}
				}
				for _, tc := range []struct {
					query      string
					diagnostic fixture.Diagnostic
				}{
					{"SELECT qa.items.id FROM qa.items AS x", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Hint: "Perhaps you meant to reference the table alias \"x\".", Position: "8"}},
					{"SELECT qa.items.id FROM qa.items AS items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "8"}},
					{"SELECT absent.items.id FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "8"}},
					{"SELECT qb.items.id FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "8"}},
					{"SELECT qc.items.id FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "8"}},
					{"SELECT public.items.id FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "8"}},
					{"UPDATE qa.items SET v=1 WHERE public.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "31"}},
					{"DELETE FROM qa.items WHERE public.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "28"}},
					{"SELECT qa.unknown.id FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"unknown\"", Position: "8"}},
					{"SELECT qa.items.zzzzzz FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column items.zzzzzz does not exist", Position: "8"}},
					{"SELECT id FROM qa.items CROSS JOIN qb.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: "column reference \"id\" is ambiguous", Position: "8"}},
					{"SELECT items.id FROM qa.items CROSS JOIN qb.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P09", Message: "table reference \"items\" is ambiguous", Position: "8"}},
					{"SELECT x.id FROM qa.items CROSS JOIN(SELECT qa.items.id) AS x", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Hint: "To reference that table, you must mark this subquery with LATERAL.", Position: "45"}},
					{"SELECT 1 FROM qa.items, qb.items JOIN qc.items ON qa.items.id=qc.items.id", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "51"}},
					{"SELECT (SELECT items.denom FROM qb.items AS items) FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column items.denom does not exist", Detail: "There is a column named \"denom\" in table \"items\", but it cannot be referenced from this part of the query.", Position: "16"}},
					{"SELECT \"MixSchema\".\"MixTable\".\"mixid\" FROM \"MixSchema\".\"MixTable\"", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column MixTable.mixid does not exist", Position: "8"}},
					{"UPDATE qa.items SET items.v=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"items\" of relation \"items\" does not exist", Hint: "SET target columns cannot be qualified with the relation name.", Position: "21"}},
					{"UPDATE qa.items SET qa.items.v=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"qa\" of relation \"items\" does not exist", Position: "21"}},
					{"UPDATE qa.items AS x SET v=1 WHERE qa.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Hint: "Perhaps you meant to reference the table alias \"x\".", Position: "36"}},
					{"DELETE FROM qa.items AS x WHERE qa.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Hint: "Perhaps you meant to reference the table alias \"x\".", Position: "33"}},
					{"UPDATE qa.items AS items SET v=1 WHERE qa.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "40"}},
					{"DELETE FROM qa.items AS items WHERE qa.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "37"}},
					{"UPDATE qa.items SET v=1 WHERE absent.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "31"}},
					{"DELETE FROM qa.items WHERE absent.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "28"}},
					{"UPDATE qa.items SET v=1 WHERE qb.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "31"}},
					{"DELETE FROM qa.items WHERE qb.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "28"}},
					{"UPDATE qa.items SET v=1 WHERE qc.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "31"}},
					{"DELETE FROM qa.items WHERE qc.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "28"}},
					{"UPDATE qa.items SET v=1 WHERE qa.unknown.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"unknown\"", Position: "31"}},
					{"DELETE FROM qa.items WHERE qa.unknown.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"unknown\"", Position: "28"}},
					{"UPDATE qa.items SET v=1 WHERE qa.items.zzzzzz=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column items.zzzzzz does not exist", Position: "31"}},
					{"DELETE FROM qa.items WHERE qa.items.zzzzzz=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column items.zzzzzz does not exist", Position: "28"}},
					{"UPDATE qa.items SET v=1 FROM qb.items WHERE id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: "column reference \"id\" is ambiguous", Position: "45"}},
					{"DELETE FROM qa.items USING qb.items WHERE id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: "column reference \"id\" is ambiguous", Position: "43"}},
					{"SELECT qa.items.id FROM (qa.items JOIN qb.items ON qa.items.id=qb.items.id) AS hidden", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "8"}},
					{"/*é*/ DELETE FROM qa.items WHERE qb.items.id=1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "34"}},
					{"SELECT qa.items.id FROM qa.items LIMIT qa.items.id", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P10", Message: "argument of LIMIT must not contain variables", Position: "40"}},
					{"SELECT qa.items.id FROM qa.items OFFSET qa.items.id", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P10", Message: "argument of OFFSET must not contain variables", Position: "41"}},
					{"SELECT sum(v) OVER(ROWS qa.items.id PRECEDING) FROM qa.items", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P10", Message: "argument of ROWS must not contain variables", Position: "25"}},
				} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(tc.query, tc.diagnostic)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						var rows [][]any
						if populated {
							rows = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
					}
				}
			})
		})
	}
}
