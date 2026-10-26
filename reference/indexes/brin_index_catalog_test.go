package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBrinIndexCatalog(t *testing.T) {
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Select("SELECT oid::int,opcname,opcfamily::int,opcintype::int,opcdefault FROM pg_opclass WHERE opcmethod=3580 AND opcname IN ('int4_minmax_ops','inet_minmax_ops','inet_inclusion_ops') ORDER BY opcname", []string{"oid", "opcname", "opcfamily", "opcintype", "opcdefault"}, []string{"INT4", "NAME", "INT4", "INT4", "BOOL"}, [][]any{{int64(10130), "inet_inclusion_ops", int64(4102), int64(869), true}, {int64(10127), "inet_minmax_ops", int64(4075), int64(869), false}, {int64(10104), "int4_minmax_ops", int64(4054), int64(23), true}}))
		run(fixture.Select("SELECT oid::int,amopstrategy,amopopr::int FROM pg_amop WHERE amopfamily=4054 AND amoplefttype=23 AND amoprighttype=23 ORDER BY amopstrategy", []string{"oid", "amopstrategy", "amopopr"}, []string{"INT4", "INT2", "INT4"}, [][]any{{int64(10571), int64(1), int64(97)}, {int64(10572), int64(2), int64(523)}, {int64(10573), int64(3), int64(96)}, {int64(10574), int64(4), int64(525)}, {int64(10575), int64(5), int64(521)}}))
		run(fixture.Select("SELECT oid::int,amprocnum,amproc::oid::int FROM pg_amproc WHERE amprocfamily=4054 AND amproclefttype=23 AND amprocrighttype=23 ORDER BY amprocnum", []string{"oid", "amprocnum", "amproc"}, []string{"INT4", "INT2", "INT4"}, [][]any{{int64(10370), int64(1), int64(3383)}, {int64(10371), int64(2), int64(3384)}, {int64(10372), int64(3), int64(3385)}, {int64(10373), int64(4), int64(3386)}}))
		run(fixture.Select("SELECT oid::int,proname,prorettype::int,oid::regprocedure::text AS signature,proisstrict,provolatile,proparallel,proretset,prokind,prolang::int,prosrc FROM pg_proc WHERE oid IN (3383,3384,3385,3386) ORDER BY oid", []string{"oid", "proname", "prorettype", "signature", "proisstrict", "provolatile", "proparallel", "proretset", "prokind", "prolang", "prosrc"}, []string{"INT4", "NAME", "INT4", "TEXT", "BOOL", "CHAR", "CHAR", "BOOL", "CHAR", "INT4", "TEXT"}, [][]any{
			{int64(3383), "brin_minmax_opcinfo", int64(2281), "brin_minmax_opcinfo(internal)", true, "i", "s", false, "f", int64(12), "brin_minmax_opcinfo"},
			{int64(3384), "brin_minmax_add_value", int64(16), "brin_minmax_add_value(internal,internal,internal,internal)", true, "i", "s", false, "f", int64(12), "brin_minmax_add_value"},
			{int64(3385), "brin_minmax_consistent", int64(16), "brin_minmax_consistent(internal,internal,internal)", true, "i", "s", false, "f", int64(12), "brin_minmax_consistent"},
			{int64(3386), "brin_minmax_union", int64(16), "brin_minmax_union(internal,internal,internal)", true, "i", "s", false, "f", int64(12), "brin_minmax_union"},
		}))
	})
}
