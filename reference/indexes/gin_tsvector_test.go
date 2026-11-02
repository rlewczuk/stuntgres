package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINTSVector(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE gin_vectors(id integer,v tsvector)", 0))
		run(fixture.Exec(`INSERT INTO gin_vectors VALUES(1,'cat:1A rat:2'),(2,'cat:1 rat:3A'),(3,'dog:1'),(4,''),(5,NULL),(6,'cat rat'),(7,'catalog:1 rat:2')`, 7))
		const query = "SELECT id FROM gin_vectors WHERE v @@ $1::tsquery ORDER BY id"
		held := classHeld(t, p, conn, query)
		check := func() {
			for _, tc := range []struct {
				query string
				ids   []int64
			}{
				{"cat", []int64{1, 2, 6}},
				{"cat & rat", []int64{1, 2, 6}},
				{"cat <-> rat", []int64{1}},
				{"cat:A", []int64{1, 6}},
				{"cat:*", []int64{1, 2, 6, 7}},
				{"!cat", []int64{3, 4, 7}},
				{"cat | dog", []int64{1, 2, 3, 6}},
			} {
				s := ginIDs(query, tc.ids, tc.query)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, recorder, s)
				run(ginIDs("SELECT id FROM gin_vectors WHERE $1::tsquery @@ v ORDER BY id", tc.ids, tc.query))
			}
			if len(notices) != 0 {
				t.Fatalf("nonempty matching notices: %#v", notices)
			}
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `text-search query doesn't contain lexemes: ""`, Context: "unnamed portal parameter $1 = ''"}}
			s := ginIDs(query, nil, "")
			for _, route := range []string{"direct", "fresh", "held"} {
				switch route {
				case "direct":
					run(s)
				case "fresh":
					run(fixture.Prepared(s))
				case "held":
					fixture.RunStep(t, p.Ctx, held, recorder, s)
				}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("%s empty-query notices=%#v want=%#v", route, notices, want)
				}
				notices = nil
			}
			run(fixture.Select("SELECT v @@ NULL::tsquery AS match,NULL::tsvector @@ 'cat'::tsquery AS absent FROM gin_vectors ORDER BY id", []string{"match", "absent"}, []string{"BOOL", "BOOL"}, [][]any{{nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}))
		}
		check()
		run(fixture.Exec("CREATE INDEX gin_vectors_i ON gin_vectors USING gin(v)", 0))
		check()
		run(fixture.Exec("DROP INDEX gin_vectors_i", 0))
		run(fixture.Prepared(fixture.Exec("CREATE INDEX gin_vectors_i ON gin_vectors USING gin(v pg_catalog.tsvector_ops)", 0)))
		check()
		run(fixture.Prepared(fixture.Exec("INSERT INTO gin_vectors VALUES($1,$2::tsvector)", 1, 8, "cat:1 rat:2")))
		checkPhrase := func(ids []int64) {
			s := ginIDs(query, ids, "cat <-> rat")
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, recorder, s)
		}
		checkPhrase([]int64{1, 8})
		run(fixture.Exec("UPDATE gin_vectors SET v='dog:1' WHERE id=1", 1))
		checkPhrase([]int64{8})
		run(fixture.Exec("DELETE FROM gin_vectors WHERE id=8", 1))
		checkPhrase(nil)
		run(ginIDs(query, []int64{1, 3, 4, 7}, "!cat"))
	})
}
