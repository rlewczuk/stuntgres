package engine

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func pruningSelection(t *testing.T, e *Engine, query string, parameters []sqltype.Value) ([]string, exec.QueryBranchSelection) {
	t.Helper()
	bound, source, release := hashImplicitQuery(t, e, query)
	defer release()
	project := bound.Root.(*plan.ProjectNode)
	filter := project.Child.(*plan.FilterNode)
	state := &exec.State{Source: source, Frame: project.Frame, Parameters: parameters}
	evaluation := state.Evaluation(context.Background())
	if scope := scopeOf(source.LiveHost()); scope != nil {
		evaluation.Input = scope.input
	}
	if source.BranchPruner == nil {
		t.Fatal("missing captured branch proof authority")
	}
	selected, err := source.BranchPruner.Select(context.Background(), &evaluation, filter.Predicate)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	start := 0
	for i, segment := range source.Inheritance {
		end := source.InheritanceEnds[i]
		kept := !selected.Applied
		for _, interval := range selected.Intervals {
			kept = kept || max(start, interval.Start) < min(end, interval.End)
		}
		if kept && end > start {
			names = append(names, segment.Leaf.Relation.Name())
		}
		start = end
	}
	sort.Strings(names)
	return names, selected
}

func TestPartitionPruningProof(t *testing.T) {
	t.Run("range", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE pp(k INT) PARTITION BY RANGE(k)",
			"CREATE TABLE pp_a PARTITION OF pp FOR VALUES FROM(1) TO(3)",
			"CREATE TABLE pp_b PARTITION OF pp FOR VALUES FROM(3) TO(5)",
			"CREATE TABLE pp_d PARTITION OF pp DEFAULT",
			"INSERT INTO pp VALUES(0),(1),(1),(2),(3),(4),(7),(NULL)",
		} {
			run(t, e, q)
		}
		for _, c := range []struct {
			predicate string
			names     []string
		}{
			{"k=1", []string{"pp_a"}}, {"1=k", []string{"pp_a"}},
			{"k>=3 AND k<5", []string{"pp_b"}}, {"k<3", []string{"pp_a", "pp_d"}},
			{"k=7", []string{"pp_d"}}, {"k IS NULL", []string{"pp_d"}},
			{"k=NULL", nil}, {"k>3 AND k<3", nil},
			{"k=1 OR k IS NULL", []string{"pp_a", "pp_d"}},
			{"k=1 OR abs(k)=7", []string{"pp_a", "pp_b", "pp_d"}},
		} {
			got, _ := pruningSelection(t, e, "SELECT k FROM pp WHERE "+c.predicate, nil)
			if !reflect.DeepEqual(got, c.names) {
				t.Fatalf("%s kept=%v want=%v", c.predicate, got, c.names)
			}
		}
		integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
		for _, n := range []int64{1, 3, 7, 1} {
			v, _ := sqltype.Integer(integer, n)
			got, _ := pruningSelection(t, e, "SELECT k FROM pp WHERE k=$1", []sqltype.Value{v})
			name := "pp_a"
			if n == 3 {
				name = "pp_b"
			}
			if n == 7 {
				name = "pp_d"
			}
			if !reflect.DeepEqual(got, []string{name}) {
				t.Fatal(n, got)
			}
		}
	})
	t.Run("list", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE pp(k INT) PARTITION BY LIST(k)",
			"CREATE TABLE pp_a PARTITION OF pp FOR VALUES IN(1,3,NULL)",
			"CREATE TABLE pp_b PARTITION OF pp FOR VALUES IN(2)",
			"CREATE TABLE pp_d PARTITION OF pp DEFAULT",
			"INSERT INTO pp VALUES(1),(3),(NULL),(2),(7)",
		} {
			run(t, e, q)
		}
		for _, c := range []struct {
			predicate string
			names     []string
		}{
			{"k=1", []string{"pp_a"}}, {"k IS NULL", []string{"pp_a"}},
			{"k=99", []string{"pp_d"}}, {"k<>1", []string{"pp_a", "pp_b", "pp_d"}},
			{"k<2", []string{"pp_a", "pp_d"}}, {"k IS NOT NULL", []string{"pp_a", "pp_b", "pp_d"}},
		} {
			got, _ := pruningSelection(t, e, "SELECT k FROM pp WHERE "+c.predicate, nil)
			if !reflect.DeepEqual(got, c.names) {
				t.Fatalf("%s kept=%v want=%v", c.predicate, got, c.names)
			}
		}
	})
	t.Run("hash", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE pp(k INT) PARTITION BY HASH(k)",
			"CREATE TABLE pp_a PARTITION OF pp FOR VALUES WITH(MODULUS 2,REMAINDER 0)",
			"CREATE TABLE pp_b PARTITION OF pp FOR VALUES WITH(MODULUS 4,REMAINDER 1)",
			"CREATE TABLE pp_c PARTITION OF pp FOR VALUES WITH(MODULUS 4,REMAINDER 3)",
			"INSERT INTO pp VALUES(1),(1),(2),(3),(4),(NULL)",
		} {
			run(t, e, q)
		}
		for _, c := range []struct {
			predicate string
			names     []string
		}{
			{"k=1", []string{"pp_a"}}, {"k=2", []string{"pp_a"}}, {"k=3", []string{"pp_b"}},
			{"k=4", []string{"pp_c"}}, {"k IS NULL", []string{"pp_a"}},
			{"k>1", []string{"pp_a", "pp_b", "pp_c"}},
		} {
			got, _ := pruningSelection(t, e, "SELECT k FROM pp WHERE "+c.predicate, nil)
			if !reflect.DeepEqual(got, c.names) {
				t.Fatalf("%s kept=%v want=%v", c.predicate, got, c.names)
			}
		}
	})
	t.Run("inheritance", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE pp(k INT,CONSTRAINT local CHECK(k>0) NO INHERIT)",
			"CREATE TABLE pp_a(CHECK(k>10)) INHERITS(pp)",
			"CREATE TABLE pp_b(CHECK(k<0)) INHERITS(pp)",
			"CREATE TABLE pp_old() INHERITS(pp)",
			"INSERT INTO pp VALUES(1)", "INSERT INTO pp_a VALUES(11),(NULL)", "INSERT INTO pp_b VALUES(-1),(NULL)", "INSERT INTO pp_old VALUES(-7)",
			"ALTER TABLE pp_old ADD CHECK(k>100) NOT VALID",
		} {
			run(t, e, q)
		}
		for _, c := range []struct {
			predicate string
			names     []string
		}{
			{"k<0", []string{"pp_b", "pp_old"}}, {"k IS NULL", []string{"pp", "pp_a", "pp_b", "pp_old"}},
			{"k=1", []string{"pp", "pp_old"}},
		} {
			got, _ := pruningSelection(t, e, "SELECT k FROM pp WHERE "+c.predicate, nil)
			if !reflect.DeepEqual(got, c.names) {
				t.Fatalf("%s kept=%v want=%v", c.predicate, got, c.names)
			}
		}
	})
	t.Run("nested_ancestor", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE pp(k INT) PARTITION BY RANGE(k)",
			"CREATE TABLE pp_mid PARTITION OF pp FOR VALUES FROM(1) TO(5) PARTITION BY LIST(k)",
			"CREATE TABLE pp_a PARTITION OF pp_mid FOR VALUES IN(1,3)",
			"CREATE TABLE pp_b PARTITION OF pp_mid DEFAULT",
			"CREATE TABLE pp_outer PARTITION OF pp DEFAULT PARTITION BY HASH(k)",
			"CREATE TABLE pp_c PARTITION OF pp_outer FOR VALUES WITH(MODULUS 1,REMAINDER 0)",
			"INSERT INTO pp VALUES(1),(1),(2),(3),(7),(NULL)",
		} {
			run(t, e, q)
		}
		for _, c := range []struct {
			predicate string
			names     []string
		}{
			{"k=1", []string{"pp_a"}}, {"k=2", []string{"pp_b"}},
			{"k=7", []string{"pp_c"}}, {"k IS NULL", []string{"pp_c"}},
			{"k=1 OR k=7", []string{"pp_a", "pp_c"}},
		} {
			got, _ := pruningSelection(t, e, "SELECT k FROM pp WHERE "+c.predicate, nil)
			if !reflect.DeepEqual(got, c.names) {
				t.Fatalf("%s kept=%v want=%v", c.predicate, got, c.names)
			}
		}
	})
	t.Run("lexicographic", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE pp(a INT,b INT) PARTITION BY RANGE(a,b)",
			"CREATE TABLE pp_low PARTITION OF pp FOR VALUES FROM(MINVALUE,MINVALUE) TO(1,2)",
			"CREATE TABLE pp_mid PARTITION OF pp FOR VALUES FROM(1,2) TO(3,4)",
			"CREATE TABLE pp_high PARTITION OF pp FOR VALUES FROM(3,4) TO(MAXVALUE,MAXVALUE)",
			"CREATE TABLE pp_def PARTITION OF pp DEFAULT",
			"INSERT INTO pp VALUES(0,9),(1,1),(1,2),(2,-9),(3,3),(3,4),(NULL,2),(2,NULL)",
		} {
			run(t, e, q)
		}
		for _, c := range []struct {
			predicate string
			names     []string
		}{
			{"a=1 AND b=1", []string{"pp_low"}},
			{"a=2", []string{"pp_def", "pp_mid"}},
			{"a=3 AND b<4", []string{"pp_mid"}},
			{"a=3 AND b=4", []string{"pp_high"}},
			{"a IS NULL OR b IS NULL", []string{"pp_def"}},
			{"b=2", []string{"pp_def", "pp_high", "pp_low", "pp_mid"}},
		} {
			got, _ := pruningSelection(t, e, "SELECT a FROM pp WHERE "+c.predicate, nil)
			if !reflect.DeepEqual(got, c.names) {
				t.Fatalf("%s kept=%v want=%v", c.predicate, got, c.names)
			}
		}
	})
	t.Run("complete_hash_tuple", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE pp(a INT,b INT) PARTITION BY HASH(a,b)",
			"CREATE TABLE pp_zero PARTITION OF pp FOR VALUES WITH(MODULUS 2,REMAINDER 0)",
			"CREATE TABLE pp_one PARTITION OF pp FOR VALUES WITH(MODULUS 4,REMAINDER 1)",
			"CREATE TABLE pp_three PARTITION OF pp FOR VALUES WITH(MODULUS 4,REMAINDER 3)",
			"INSERT INTO pp VALUES(NULL,NULL),(0,NULL),(NULL,0),(0,0),(0,1),(1,0),(1,2),(2,1),(-1,42)",
		} {
			run(t, e, q)
		}
		for _, c := range []struct {
			predicate string
			names     []string
		}{
			{"a IS NULL AND b IS NULL", []string{"pp_zero"}},
			{"a=0 AND b IS NULL", []string{"pp_zero"}},
			{"a IS NULL AND b=0", []string{"pp_zero"}},
			{"a=1 AND b=2", []string{"pp_one"}},
			{"a=0 AND b=1", []string{"pp_three"}},
			{"a=1", []string{"pp_one", "pp_three", "pp_zero"}},
		} {
			got, _ := pruningSelection(t, e, "SELECT a FROM pp WHERE "+c.predicate, nil)
			if !reflect.DeepEqual(got, c.names) {
				t.Fatalf("%s kept=%v want=%v", c.predicate, got, c.names)
			}
		}
	})
}
