package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func referencePruningHashBody(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TABLE pp_hash(k INT) PARTITION BY HASH(k)", 0))
	referenceHashChildren(run, "pp_hash", 4)
	run(fixture.Exec("INSERT INTO pp_hash VALUES(NULL),(0),(1),(1),(2),(3),(4)", 7))
	// Literal released vectors: NULL/0/1 -> 0, 2 -> 2, 3 -> 1, 4 -> 3.
	for r, keys := range [][]any{{nil, int64(0), int64(1), int64(1)}, {int64(3)}, {int64(2)}, {int64(4)}} {
		referencePruningKeys(run, b.PG, fmt.Sprintf("SELECT k FROM pp_hash_%d", r), keys...)
	}
	for _, c := range []struct {
		predicate string
		keys      []any
	}{
		{"k=1", []any{int64(1), int64(1)}},
		{"k=2", []any{int64(2)}},
		{"k=3", []any{int64(3)}},
		{"k=4", []any{int64(4)}},
		{"k IS NULL", []any{nil}},
		{"k=NULL", nil},
		{"k>1", []any{int64(2), int64(3), int64(4)}},
		{"k=1 OR k IS NULL", []any{int64(1), int64(1), nil}},
	} {
		referencePruningKeys(run, b.PG, "SELECT k FROM pp_hash WHERE "+c.predicate, c.keys...)
	}
	run(fixture.Exec("CREATE TABLE pp_hash_pair(a INT,b INT,id INT) PARTITION BY HASH(a,b)", 0))
	referenceHashChildren(run, "pp_hash_pair", 8)
	run(fixture.Exec("INSERT INTO pp_hash_pair VALUES(NULL,NULL,0),(0,NULL,1),(NULL,0,2),(0,0,3),(0,1,4),(1,0,5),(1,2,6),(2,1,7),(-1,42,8)", 9))
	for r, ids := range [][]any{{int64(0), int64(1), int64(2), int64(7)}, {int64(6)}, nil, {int64(3), int64(4)}, {int64(8)}, nil, nil, {int64(5)}} {
		rows := make([][]any, len(ids))
		for i, id := range ids {
			rows[i] = []any{id}
		}
		s := fixture.Select(fmt.Sprintf("SELECT id FROM pp_hash_pair_%d", r), []string{"id"}, []string{"INT4"}, rows)
		s.Bag = true
		run(s)
	}
	for _, c := range []struct {
		predicate string
		ids       []int64
	}{
		{"a IS NULL AND b IS NULL", []int64{0}},
		{"a=0 AND b IS NULL", []int64{1}},
		{"a IS NULL AND b=0", []int64{2}},
		{"a=1 AND b=2", []int64{6}},
		{"a=0", []int64{1, 3, 4}},
		{"a>0", []int64{5, 6, 7}},
	} {
		rows := make([][]any, len(c.ids))
		for i, id := range c.ids {
			rows[i] = []any{id}
		}
		s := fixture.Select("SELECT id FROM pp_hash_pair WHERE "+c.predicate, []string{"id"}, []string{"INT4"}, rows)
		s.Bag = true
		run(s)
	}
	for _, q := range []string{
		"CREATE TABLE pp_hash_mixed(k INT) PARTITION BY HASH(k)",
		"CREATE TABLE pp_hash_even PARTITION OF pp_hash_mixed FOR VALUES WITH(MODULUS 2,REMAINDER 0)",
		"CREATE TABLE pp_hash_one PARTITION OF pp_hash_mixed FOR VALUES WITH(MODULUS 4,REMAINDER 1)",
		"CREATE TABLE pp_hash_three PARTITION OF pp_hash_mixed FOR VALUES WITH(MODULUS 4,REMAINDER 3)",
		"CREATE TABLE pp_hash_sparse(k INT) PARTITION BY HASH(k)",
		"CREATE TABLE pp_hash_sparse_zero PARTITION OF pp_hash_sparse FOR VALUES WITH(MODULUS 4,REMAINDER 0)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO pp_hash_mixed VALUES(NULL),(1),(2),(3),(4)", 5))
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_hash_mixed WHERE k>=2", int64(2), int64(3), int64(4))
	run(fixture.Exec("INSERT INTO pp_hash_sparse VALUES(NULL),(1),(1)", 3))
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_hash_sparse WHERE k=2")
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_hash_sparse WHERE k=1", int64(1), int64(1))
}

func TestReferencePartitionPruningHash(t *testing.T) {
	referencePruningScenario(t, referencePruningHashBody)
}
