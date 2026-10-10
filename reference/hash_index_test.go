package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// hashIndexScenario keeps Context/plain routes in one shared contract body.
func hashIndexScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, func(referenceStep))) {
	t.Helper()
	for _, plain := range []bool{false, true} {
		t.Run(fmt.Sprintf("plain=%v", plain), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) {
						s.nonContext = plain
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					}
					body(t, p, b, run)
				})
			}
		})
	}
}

func hashIndexSeed(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE t(n INT,s TEXT)", 0))
	run(referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5))
}

func hashIndexBag(query string, rows [][]any, args ...any) referenceStep {
	s := referenceSelect(query, []string{"n", "s"}, []string{"INT4", "TEXT"}, rows, args...)
	s.bag = true
	return s
}

func hashIndexLookupContract(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
	t.Helper()
	hashIndexSeed(run)
	run(referenceExec("CREATE INDEX hn ON t USING hash(n)", 0))
	create := "CREATE INDEX hs ON t USING hash(s)"
	for _, plain := range []bool{false, true} {
		var stmt *sql.Stmt
		var err error
		if plain {
			stmt, err = b.db.Prepare(create)
		} else {
			stmt, err = b.db.PrepareContext(p.ctx, create)
		}
		if err != nil {
			t.Fatal("hash utility Prepare", err)
		}
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='hs'", []string{"relname"}, []string{"NAME"}, nil))
		s := referenceExec(create, 0)
		s.nonContext = plain
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: create}, b.recorder, s)
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if !plain {
			run(referenceExec("DROP INDEX hs", 0))
		}
	}
	for _, s := range []referenceStep{
		hashIndexBag("SELECT n,s FROM t WHERE n=2", [][]any{{int64(2), "b"}, {int64(2), "b"}}),
		hashIndexBag("SELECT n,s FROM t WHERE 2=n", [][]any{{int64(2), "b"}, {int64(2), "b"}}),
		hashIndexBag("SELECT n,s FROM t WHERE s='b'", [][]any{{int64(2), "b"}, {int64(2), "b"}}),
		hashIndexBag("SELECT n,s FROM t WHERE n=NULL", nil),
		hashIndexBag("SELECT n,s FROM t WHERE n=99", nil),
		hashIndexBag("SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}}),
		hashIndexBag("SELECT n,s FROM t WHERE n IS NOT NULL", [][]any{{int64(2), "b"}, {int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}}),
		hashIndexBag("SELECT n,s FROM t WHERE n>=2", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}),
		referenceSelect("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}),
	} {
		run(s)
		run(referencePrepared(s))
	}
	q := "SELECT n,s FROM t WHERE n=$1"
	probes := [2]*sql.Stmt{}
	for route, plain := range []bool{false, true} {
		var err error
		if plain {
			probes[route], err = b.db.Prepare(q)
		} else {
			probes[route], err = b.db.PrepareContext(p.ctx, q)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer probes[route].Close()
		held := referenceHeldStatement{referenceExecutor: b.db, statement: probes[route], query: q}
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{
			{int64(2), [][]any{{int64(2), "b"}, {int64(2), "b"}}},
			{int64(1), [][]any{{int64(1), "a"}}}, {nil, nil}, {int64(99), nil},
		} {
			s := hashIndexBag(q, tc.rows, tc.arg)
			s.nonContext = plain
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
	}
	run(referenceExec("INSERT INTO t VALUES(2,'b'),(NULL,NULL)", 2))
	for route, probe := range probes {
		s := hashIndexBag(q, [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}}, int64(2))
		s.nonContext = route == 1
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: probe, query: q}, b.recorder, s)
	}
	run(hashIndexBag("SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}, {nil, nil}}))
}

func TestReferenceHashIndexLookup(t *testing.T) {
	hashIndexScenario(t, hashIndexLookupContract)
}

func hashIndexProperties(run func(referenceStep), index string) {
	for _, tc := range []struct {
		prop string
		want any
	}{
		{"clusterable", false}, {"index_scan", true}, {"bitmap_scan", true}, {"backward_scan", true}, {"bogus", nil},
	} {
		s := indexPropertiesBoolStep("SELECT pg_index_has_property($1,$2) AS result", []string{"result"}, tc.want)
		s.args = []any{index, tc.prop}
		run(s)
		run(referencePrepared(s))
	}
	for _, tc := range []struct {
		prop string
		want any
	}{
		{"asc", false}, {"desc", false}, {"nulls_first", false}, {"nulls_last", false},
		{"orderable", false}, {"distance_orderable", false}, {"returnable", false}, {"search_array", false}, {"search_nulls", false}, {"bogus", nil},
	} {
		s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,1,$2) AS result", []string{"result"}, tc.want)
		s.args = []any{index, tc.prop}
		run(s)
		run(referencePrepared(s))
	}
	for _, ordinal := range []int{-1, 0, 2} {
		s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,'orderable') AS result", []string{"result"}, nil)
		s.args = []any{index, ordinal}
		run(referencePrepared(s))
	}
}

func TestReferenceHashIndexFeatureSmoke(t *testing.T) {
	t.Run("lookup-and-diagnostics", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
			hashIndexLookupContract(t, p, b, run)
			hashIndexProperties(run, "hn")
			for _, s := range []referenceStep{
				referenceImplicitError("CREATE INDEX bad ON t USING hash(n missing_ops)", referenceDiagnostic{Code: "42704", Message: `operator class "missing_ops" does not exist for access method "hash"`}),
				referenceImplicitError("CREATE INDEX bad ON t USING hash(n) WITH(fillfactor=9)", referenceDiagnostic{Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`}),
			} {
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
			}
			run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('hn'::regclass) AS definition", "CREATE INDEX hn ON public.t USING hash (n)"))
		})
	})
	t.Run("controls", hashIndexControlsContract)
}
