package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualOuterJoins(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE l(x INT)`, 0))
		run(fixture.Exec(`INSERT INTO l VALUES(1),(2)`, 2))
		// The only base attribute is nullable. A real all-NULL row must remain
		// distinguishable from an absent operand, even for constant recipes.
		run(fixture.Exec(`CREATE TABLE g(a INT,c INT GENERATED ALWAYS AS(1) VIRTUAL,b INT GENERATED ALWAYS AS(coalesce(a,9)) VIRTUAL,o OID GENERATED ALWAYS AS(tableoid) VIRTUAL)`, 0))
		columns := []string{"x", "a", "c", "b", "owner"}
		types := []string{"INT4", "INT4", "INT4", "INT4", "BOOL"}
		for _, from := range []string{`l LEFT JOIN g ON TRUE`, `g RIGHT JOIN l ON TRUE`, `l FULL JOIN g ON l.x=g.b-8`} {
			s := fixture.Select(`SELECT l.x,g.a,g.c,g.b,g.o=(SELECT oid FROM pg_class WHERE relname='g') AS owner FROM `+from+` ORDER BY l.x`, columns, types, [][]any{{int64(1), nil, nil, nil, nil}, {int64(2), nil, nil, nil, nil}})
			run(s)
		}
		run(fixture.Exec(`INSERT INTO g(a) VALUES(NULL)`, 1))
		for _, prepared := range []bool{false, true} {
			for _, from := range []string{`l LEFT JOIN g ON l.x=1`, `g RIGHT JOIN l ON l.x=1`, `l FULL JOIN g ON l.x=g.b-8`} {
				s := fixture.Select(`SELECT l.x,g.a,g.c,g.b,g.o=(SELECT oid FROM pg_class WHERE relname='g') AS owner FROM `+from+` ORDER BY l.x`, columns, types, [][]any{{int64(1), nil, int64(1), int64(9), true}, {int64(2), nil, nil, nil, nil}})
				s.Prepared = prepared
				run(s)
			}
			s := fixture.Select(`SELECT l.x,g.c,g.b,g.o=(SELECT oid FROM pg_class WHERE relname='g') AS owner FROM l FULL JOIN g ON l.x=g.b+10`, []string{"x", "c", "b", "owner"}, []string{"INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(1), nil, nil, nil}, {int64(2), nil, nil, nil}, {nil, int64(1), int64(9), true}})
			s.Bag = true
			s.Prepared = prepared
			run(s)
			for _, predicate := range []struct {
				where string
				x     int64
			}{
				{`g.c IS NULL AND g.b IS NULL AND g.o IS NULL`, 2},
				{`g.c=1 AND g.b=9 AND g.o=(SELECT oid FROM pg_class WHERE relname='g')`, 1},
			} {
				s := fixture.Select(`SELECT l.x FROM l LEFT JOIN g ON l.x=1 WHERE `+predicate.where, []string{"x"}, []string{"INT4"}, [][]any{{predicate.x}})
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Select(`SELECT l.x,g.c FROM l LEFT JOIN g ON l.x=g.c ORDER BY l.x`, []string{"x", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), nil}}))
		run(fixture.Exec(`CREATE TABLE u(a INT,c INT GENERATED ALWAYS AS(coalesce(a,1)) VIRTUAL,b INT GENERATED ALWAYS AS(coalesce(a,9)) VIRTUAL,o OID GENERATED ALWAYS AS(tableoid) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO u(a) VALUES(NULL)`, 1))
		run(fixture.Exec(`CREATE TABLE r(a INT,c INT GENERATED ALWAYS AS(coalesce(a,2)) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO r(a) VALUES(NULL)`, 1))
		// FULL USING must merge the present key, not recompute the absent key.
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(`SELECT c,g.c AS gc,r.c AS rc,g.b,g.o=(SELECT oid FROM pg_class WHERE relname='u') AS owner FROM u g FULL JOIN r USING(c) ORDER BY c`, []string{"c", "gc", "rc", "b", "owner"}, []string{"INT4", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(1), nil, int64(9), true}, {int64(2), nil, int64(2), nil, nil}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec(`INSERT INTO r(a) VALUES(1)`, 1))
		run(fixture.Select(`SELECT c,g.c AS gc,r.c AS rc FROM u g FULL JOIN r USING(c) ORDER BY c`, []string{"c", "gc", "rc"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), nil, int64(2)}}))
	})
}
