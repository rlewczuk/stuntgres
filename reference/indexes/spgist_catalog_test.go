package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTMetadataProperties(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		classRoutes(t, p, b, conn, fixture.Select(`SELECT oid::bigint,opcname,opcfamily::bigint,opcintype::bigint,opcdefault,opckeytype::bigint FROM pg_opclass WHERE opcmethod=4000 ORDER BY oid`, []string{"oid", "opcname", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"INT8", "NAME", "INT8", "INT8", "BOOL", "INT8"}, [][]any{
			{int64(10018), "inet_ops", int64(3794), int64(869), true, int64(0)},
			{int64(10079), "range_ops", int64(3474), int64(3831), true, int64(0)},
			{int64(10083), "box_ops", int64(5000), int64(603), true, int64(0)},
			{int64(10084), "quad_point_ops", int64(4015), int64(600), true, int64(0)},
			{int64(10085), "kd_point_ops", int64(4016), int64(600), false, int64(0)},
			{int64(10086), "text_ops", int64(4017), int64(25), true, int64(0)},
			{int64(10087), "poly_ops", int64(5008), int64(604), true, int64(603)},
		}))
		classRoutes(t, p, b, conn, fixture.Select(`SELECT oid::bigint,opfname,opfmethod::bigint,opfnamespace::bigint,opfowner::bigint FROM pg_opfamily WHERE opfmethod=4000 ORDER BY oid`, []string{"oid", "opfname", "opfmethod", "opfnamespace", "opfowner"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8"}, [][]any{
			{int64(3474), "range_ops", int64(4000), int64(11), int64(10)},
			{int64(3794), "network_ops", int64(4000), int64(11), int64(10)},
			{int64(4015), "quad_point_ops", int64(4000), int64(11), int64(10)},
			{int64(4016), "kd_point_ops", int64(4000), int64(11), int64(10)},
			{int64(4017), "text_ops", int64(4000), int64(11), int64(10)},
			{int64(5000), "box_ops", int64(4000), int64(11), int64(10)},
			{int64(5008), "poly_ops", int64(4000), int64(11), int64(10)},
		}))
		classRoutes(t, p, b, conn, fixture.Select(`SELECT oid::bigint,amopfamily::bigint,amoplefttype::bigint,amoprighttype::bigint,amopstrategy,amoppurpose,amopopr::bigint,amopmethod::bigint,amopsortfamily::bigint FROM pg_amop WHERE amopmethod=4000 ORDER BY oid`, []string{"oid", "amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amoppurpose", "amopopr", "amopmethod", "amopsortfamily"}, []string{"INT8", "INT8", "INT8", "INT8", "INT2", "CHAR", "INT8", "INT8", "INT8"}, [][]any{
			{int64(10422), int64(4015), int64(600), int64(600), int64(11), "s", int64(4161), int64(4000), int64(0)},
			{int64(10423), int64(4015), int64(600), int64(600), int64(30), "s", int64(506), int64(4000), int64(0)},
			{int64(10424), int64(4015), int64(600), int64(600), int64(1), "s", int64(507), int64(4000), int64(0)},
			{int64(10425), int64(4015), int64(600), int64(600), int64(5), "s", int64(508), int64(4000), int64(0)},
			{int64(10426), int64(4015), int64(600), int64(600), int64(10), "s", int64(4162), int64(4000), int64(0)},
			{int64(10427), int64(4015), int64(600), int64(600), int64(29), "s", int64(509), int64(4000), int64(0)},
			{int64(10428), int64(4015), int64(600), int64(600), int64(6), "s", int64(510), int64(4000), int64(0)},
			{int64(10429), int64(4015), int64(600), int64(603), int64(8), "s", int64(511), int64(4000), int64(0)},
			{int64(10430), int64(4015), int64(600), int64(600), int64(15), "o", int64(517), int64(4000), int64(1970)},
			{int64(10431), int64(4016), int64(600), int64(600), int64(11), "s", int64(4161), int64(4000), int64(0)},
			{int64(10432), int64(4016), int64(600), int64(600), int64(30), "s", int64(506), int64(4000), int64(0)},
			{int64(10433), int64(4016), int64(600), int64(600), int64(1), "s", int64(507), int64(4000), int64(0)},
			{int64(10434), int64(4016), int64(600), int64(600), int64(5), "s", int64(508), int64(4000), int64(0)},
			{int64(10435), int64(4016), int64(600), int64(600), int64(10), "s", int64(4162), int64(4000), int64(0)},
			{int64(10436), int64(4016), int64(600), int64(600), int64(29), "s", int64(509), int64(4000), int64(0)},
			{int64(10437), int64(4016), int64(600), int64(600), int64(6), "s", int64(510), int64(4000), int64(0)},
			{int64(10438), int64(4016), int64(600), int64(603), int64(8), "s", int64(511), int64(4000), int64(0)},
			{int64(10439), int64(4016), int64(600), int64(600), int64(15), "o", int64(517), int64(4000), int64(1970)},
			{int64(10440), int64(4017), int64(25), int64(25), int64(1), "s", int64(2314), int64(4000), int64(0)},
			{int64(10441), int64(4017), int64(25), int64(25), int64(2), "s", int64(2315), int64(4000), int64(0)},
			{int64(10442), int64(4017), int64(25), int64(25), int64(3), "s", int64(98), int64(4000), int64(0)},
			{int64(10443), int64(4017), int64(25), int64(25), int64(4), "s", int64(2317), int64(4000), int64(0)},
			{int64(10444), int64(4017), int64(25), int64(25), int64(5), "s", int64(2318), int64(4000), int64(0)},
			{int64(10445), int64(4017), int64(25), int64(25), int64(11), "s", int64(664), int64(4000), int64(0)},
			{int64(10446), int64(4017), int64(25), int64(25), int64(12), "s", int64(665), int64(4000), int64(0)},
			{int64(10447), int64(4017), int64(25), int64(25), int64(14), "s", int64(667), int64(4000), int64(0)},
			{int64(10448), int64(4017), int64(25), int64(25), int64(15), "s", int64(666), int64(4000), int64(0)},
			{int64(10449), int64(4017), int64(25), int64(25), int64(28), "s", int64(3877), int64(4000), int64(0)},
			{int64(10465), int64(3474), int64(3831), int64(3831), int64(1), "s", int64(3893), int64(4000), int64(0)},
			{int64(10466), int64(3474), int64(3831), int64(3831), int64(2), "s", int64(3895), int64(4000), int64(0)},
			{int64(10467), int64(3474), int64(3831), int64(3831), int64(3), "s", int64(3888), int64(4000), int64(0)},
			{int64(10468), int64(3474), int64(3831), int64(3831), int64(4), "s", int64(3896), int64(4000), int64(0)},
			{int64(10469), int64(3474), int64(3831), int64(3831), int64(5), "s", int64(3894), int64(4000), int64(0)},
			{int64(10470), int64(3474), int64(3831), int64(3831), int64(6), "s", int64(3897), int64(4000), int64(0)},
			{int64(10471), int64(3474), int64(3831), int64(3831), int64(7), "s", int64(3890), int64(4000), int64(0)},
			{int64(10472), int64(3474), int64(3831), int64(3831), int64(8), "s", int64(3892), int64(4000), int64(0)},
			{int64(10473), int64(3474), int64(3831), int64(2283), int64(16), "s", int64(3889), int64(4000), int64(0)},
			{int64(10474), int64(3474), int64(3831), int64(3831), int64(18), "s", int64(3882), int64(4000), int64(0)},
			{int64(10475), int64(5000), int64(603), int64(603), int64(1), "s", int64(493), int64(4000), int64(0)},
			{int64(10476), int64(5000), int64(603), int64(603), int64(2), "s", int64(494), int64(4000), int64(0)},
			{int64(10477), int64(5000), int64(603), int64(603), int64(3), "s", int64(500), int64(4000), int64(0)},
			{int64(10478), int64(5000), int64(603), int64(603), int64(4), "s", int64(495), int64(4000), int64(0)},
			{int64(10479), int64(5000), int64(603), int64(603), int64(5), "s", int64(496), int64(4000), int64(0)},
			{int64(10480), int64(5000), int64(603), int64(603), int64(6), "s", int64(499), int64(4000), int64(0)},
			{int64(10481), int64(5000), int64(603), int64(603), int64(7), "s", int64(498), int64(4000), int64(0)},
			{int64(10482), int64(5000), int64(603), int64(603), int64(8), "s", int64(497), int64(4000), int64(0)},
			{int64(10483), int64(5000), int64(603), int64(603), int64(9), "s", int64(2571), int64(4000), int64(0)},
			{int64(10484), int64(5000), int64(603), int64(603), int64(10), "s", int64(2570), int64(4000), int64(0)},
			{int64(10485), int64(5000), int64(603), int64(603), int64(11), "s", int64(2573), int64(4000), int64(0)},
			{int64(10486), int64(5000), int64(603), int64(603), int64(12), "s", int64(2572), int64(4000), int64(0)},
			{int64(10487), int64(5000), int64(603), int64(600), int64(15), "o", int64(606), int64(4000), int64(1970)},
			{int64(10488), int64(5008), int64(604), int64(604), int64(1), "s", int64(485), int64(4000), int64(0)},
			{int64(10489), int64(5008), int64(604), int64(604), int64(2), "s", int64(486), int64(4000), int64(0)},
			{int64(10490), int64(5008), int64(604), int64(604), int64(3), "s", int64(492), int64(4000), int64(0)},
			{int64(10491), int64(5008), int64(604), int64(604), int64(4), "s", int64(487), int64(4000), int64(0)},
			{int64(10492), int64(5008), int64(604), int64(604), int64(5), "s", int64(488), int64(4000), int64(0)},
			{int64(10493), int64(5008), int64(604), int64(604), int64(6), "s", int64(491), int64(4000), int64(0)},
			{int64(10494), int64(5008), int64(604), int64(604), int64(7), "s", int64(490), int64(4000), int64(0)},
			{int64(10495), int64(5008), int64(604), int64(604), int64(8), "s", int64(489), int64(4000), int64(0)},
			{int64(10496), int64(5008), int64(604), int64(604), int64(9), "s", int64(2575), int64(4000), int64(0)},
			{int64(10497), int64(5008), int64(604), int64(604), int64(10), "s", int64(2574), int64(4000), int64(0)},
			{int64(10498), int64(5008), int64(604), int64(604), int64(11), "s", int64(2577), int64(4000), int64(0)},
			{int64(10499), int64(5008), int64(604), int64(604), int64(12), "s", int64(2576), int64(4000), int64(0)},
			{int64(10500), int64(5008), int64(604), int64(600), int64(15), "o", int64(3289), int64(4000), int64(1970)},
			{int64(10512), int64(3794), int64(869), int64(869), int64(3), "s", int64(3552), int64(4000), int64(0)},
			{int64(10513), int64(3794), int64(869), int64(869), int64(18), "s", int64(1201), int64(4000), int64(0)},
			{int64(10514), int64(3794), int64(869), int64(869), int64(19), "s", int64(1202), int64(4000), int64(0)},
			{int64(10515), int64(3794), int64(869), int64(869), int64(20), "s", int64(1203), int64(4000), int64(0)},
			{int64(10516), int64(3794), int64(869), int64(869), int64(21), "s", int64(1204), int64(4000), int64(0)},
			{int64(10517), int64(3794), int64(869), int64(869), int64(22), "s", int64(1205), int64(4000), int64(0)},
			{int64(10518), int64(3794), int64(869), int64(869), int64(23), "s", int64(1206), int64(4000), int64(0)},
			{int64(10519), int64(3794), int64(869), int64(869), int64(24), "s", int64(931), int64(4000), int64(0)},
			{int64(10520), int64(3794), int64(869), int64(869), int64(25), "s", int64(932), int64(4000), int64(0)},
			{int64(10521), int64(3794), int64(869), int64(869), int64(26), "s", int64(933), int64(4000), int64(0)},
			{int64(10522), int64(3794), int64(869), int64(869), int64(27), "s", int64(934), int64(4000), int64(0)},
		}))
		classRoutes(t, p, b, conn, familyBuiltinVisible(fixture.Select(`SELECT p.oid::bigint,p.amprocfamily::bigint,p.amproclefttype::bigint,p.amprocrighttype::bigint,p.amprocnum,p.amproc::oid::bigint FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfmethod=4000 ORDER BY p.oid`, []string{"oid", "amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"INT8", "INT8", "INT8", "INT8", "INT2", "INT8"}, [][]any{
			{int64(10296), int64(3474), int64(3831), int64(3831), int64(1), int64(3469)},
			{int64(10297), int64(3474), int64(3831), int64(3831), int64(2), int64(3470)},
			{int64(10298), int64(3474), int64(3831), int64(3831), int64(3), int64(3471)},
			{int64(10299), int64(3474), int64(3831), int64(3831), int64(4), int64(3472)},
			{int64(10300), int64(3474), int64(3831), int64(3831), int64(5), int64(3473)},
			{int64(10301), int64(3794), int64(869), int64(869), int64(1), int64(3795)},
			{int64(10302), int64(3794), int64(869), int64(869), int64(2), int64(3796)},
			{int64(10303), int64(3794), int64(869), int64(869), int64(3), int64(3797)},
			{int64(10304), int64(3794), int64(869), int64(869), int64(4), int64(3798)},
			{int64(10305), int64(3794), int64(869), int64(869), int64(5), int64(3799)},
			{int64(10306), int64(4015), int64(600), int64(600), int64(1), int64(4018)},
			{int64(10307), int64(4015), int64(600), int64(600), int64(2), int64(4019)},
			{int64(10308), int64(4015), int64(600), int64(600), int64(3), int64(4020)},
			{int64(10309), int64(4015), int64(600), int64(600), int64(4), int64(4021)},
			{int64(10310), int64(4015), int64(600), int64(600), int64(5), int64(4022)},
			{int64(10311), int64(4016), int64(600), int64(600), int64(1), int64(4023)},
			{int64(10312), int64(4016), int64(600), int64(600), int64(2), int64(4024)},
			{int64(10313), int64(4016), int64(600), int64(600), int64(3), int64(4025)},
			{int64(10314), int64(4016), int64(600), int64(600), int64(4), int64(4026)},
			{int64(10315), int64(4016), int64(600), int64(600), int64(5), int64(4022)},
			{int64(10316), int64(4017), int64(25), int64(25), int64(1), int64(4027)},
			{int64(10317), int64(4017), int64(25), int64(25), int64(2), int64(4028)},
			{int64(10318), int64(4017), int64(25), int64(25), int64(3), int64(4029)},
			{int64(10319), int64(4017), int64(25), int64(25), int64(4), int64(4030)},
			{int64(10320), int64(4017), int64(25), int64(25), int64(5), int64(4031)},
			{int64(10321), int64(5000), int64(603), int64(603), int64(1), int64(5012)},
			{int64(10322), int64(5000), int64(603), int64(603), int64(2), int64(5013)},
			{int64(10323), int64(5000), int64(603), int64(603), int64(3), int64(5014)},
			{int64(10324), int64(5000), int64(603), int64(603), int64(4), int64(5015)},
			{int64(10325), int64(5000), int64(603), int64(603), int64(5), int64(5016)},
			{int64(10326), int64(5008), int64(604), int64(604), int64(1), int64(5010)},
			{int64(10327), int64(5008), int64(604), int64(604), int64(2), int64(5013)},
			{int64(10328), int64(5008), int64(604), int64(604), int64(3), int64(5014)},
			{int64(10329), int64(5008), int64(604), int64(604), int64(4), int64(5015)},
			{int64(10330), int64(5008), int64(604), int64(604), int64(5), int64(5016)},
			{int64(10331), int64(5008), int64(604), int64(604), int64(6), int64(5011)},
		}), b.PG))
		classRoutes(t, p, b, conn, fixture.Select(`SELECT oid::bigint,amname,amhandler::oid::bigint,amtype FROM pg_am WHERE oid=4000`, []string{"oid", "amname", "amhandler", "amtype"}, []string{"INT8", "NAME", "INT8", "CHAR"}, [][]any{{int64(4000), "spgist", int64(334), "i"}}))
		for _, c := range []struct {
			typ, class           string
			distance, returnable bool
		}{
			{"point", "quad_point_ops", true, true}, {"point", "kd_point_ops", true, true}, {"box", "box_ops", true, true}, {"polygon", "poly_ops", true, false}, {"text", "text_ops", false, true}, {"inet", "inet_ops", false, true}, {"int4range", "range_ops", false, true},
		} {
			run(fixture.Exec("CREATE TABLE spgist_props(v "+c.typ+",n integer)", 0))
			run(fixture.Exec("CREATE INDEX spgist_props_i ON spgist_props USING spgist(v "+c.class+") INCLUDE(n)", 0))
			for _, prop := range []struct {
				name          string
				key, included any
			}{
				{"distance_orderable", c.distance, nil}, {"returnable", c.returnable, true}, {"orderable", false, false}, {"search_nulls", true, nil}, {"search_array", false, nil}, {"asc", false, nil}, {"desc", false, nil}, {"nulls_first", false, nil}, {"nulls_last", false, nil}, {"unknown", nil, nil},
			} {
				classRoutes(t, p, b, conn, fixture.Select("SELECT pg_index_column_has_property('spgist_props_i'::regclass,1,$1) AS k,pg_index_column_has_property('spgist_props_i'::regclass,2,$1) AS included,pg_index_column_has_property('spgist_props_i'::regclass,0,$1) AS invalid,pg_index_column_has_property('spgist_props_i'::regclass,3,$1) AS beyond", []string{"k", "included", "invalid", "beyond"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{prop.key, prop.included, nil, nil}}, prop.name))
			}
			run(fixture.Exec("DROP TABLE spgist_props", 0))
		}
		for _, c := range []struct {
			name  string
			value bool
		}{{"can_order", false}, {"can_unique", false}, {"can_multi_col", false}, {"can_exclude", true}, {"can_include", true}} {
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_indexam_has_property(4000,$1) AS value", []string{"value"}, []string{"BOOL"}, [][]any{{c.value}}, c.name))
		}
		run(fixture.Exec("CREATE TABLE spgist_definitions(p point)", 0))
		run(fixture.Exec("CREATE INDEX spgist_def_default ON spgist_definitions USING spgist(p)", 0))
		run(fixture.Exec("CREATE INDEX spgist_def_explicit ON spgist_definitions USING spgist(p quad_point_ops)", 0))
		run(fixture.Exec("CREATE INDEX spgist_def_kd ON spgist_definitions USING spgist(p kd_point_ops)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT pg_get_indexdef('spgist_def_default'::regclass) AS d,pg_get_indexdef('spgist_def_explicit'::regclass) AS e,pg_get_indexdef('spgist_def_kd'::regclass) AS k", []string{"d", "e", "k"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"CREATE INDEX spgist_def_default ON public.spgist_definitions USING spgist (p)", "CREATE INDEX spgist_def_explicit ON public.spgist_definitions USING spgist (p)", "CREATE INDEX spgist_def_kd ON public.spgist_definitions USING spgist (p kd_point_ops)"}}))
		for _, c := range []struct {
			name  string
			value bool
		}{{"clusterable", false}, {"backward_scan", false}, {"index_scan", true}, {"bitmap_scan", true}} {
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_index_has_property('spgist_def_default'::regclass,$1) AS value", []string{"value"}, []string{"BOOL"}, [][]any{{c.value}}, c.name))
		}
	})
}
