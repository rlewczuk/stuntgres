package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func brinIndexProperties(run func(fixture.Step)) {
	for _, tc := range []struct {
		name string
		want any
	}{
		{"can_order", false}, {"can_unique", false}, {"can_multi_col", true}, {"can_exclude", false}, {"can_include", false}, {"bogus", nil},
	} {
		s := indexPropertiesBoolStep("SELECT pg_indexam_has_property(3580,$1) AS result", []string{"result"}, tc.want)
		s.Args = []any{tc.name}
		run(s)
		run(fixture.Prepared(s))
	}
	for _, tc := range []struct {
		name string
		want any
	}{
		{"clusterable", false}, {"index_scan", false}, {"bitmap_scan", true}, {"backward_scan", false}, {"bogus", nil},
	} {
		s := indexPropertiesBoolStep("SELECT pg_index_has_property('bn',$1) AS result", []string{"result"}, tc.want)
		s.Args = []any{tc.name}
		run(s)
		run(fixture.Prepared(s))
	}
	for _, tc := range []struct {
		name string
		want any
	}{
		{"asc", false}, {"desc", false}, {"nulls_first", false}, {"nulls_last", false}, {"orderable", false}, {"distance_orderable", false}, {"returnable", false}, {"search_array", false}, {"search_nulls", true}, {"bogus", nil},
	} {
		s := indexPropertiesBoolStep("SELECT pg_index_column_has_property('bn',1,$1) AS result", []string{"result"}, tc.want)
		s.Args = []any{tc.name}
		run(s)
		run(fixture.Prepared(s))
	}
	for _, ordinal := range []int{-1, 0, 2} {
		s := indexPropertiesBoolStep("SELECT pg_index_column_has_property('bn',$1,'orderable') AS result", []string{"result"}, nil)
		s.Args = []any{ordinal}
		run(s)
	}
	run(fixture.Select("SELECT oid::int,amname,amhandler::oid::int,amtype FROM pg_am WHERE amname='brin'", []string{"oid", "amname", "amhandler", "amtype"}, []string{"INT4", "NAME", "INT4", "CHAR"}, [][]any{{int64(3580), "brin", int64(335), "i"}}))
}

func TestReferenceBrinIndexProperties(t *testing.T) {
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		brinIndexSeed(run)
		run(fixture.Exec("CREATE INDEX bn ON t USING brin(n)", 0))
		brinIndexProperties(run)
	})
}
