package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGiSTMetadataProperties(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		classRoutes(t, p, b, conn, fixture.Select(`SELECT oid::bigint,opcname,opcfamily::bigint,opcintype::bigint,opcdefault,opckeytype::bigint FROM pg_opclass WHERE opcmethod=783 ORDER BY oid`, []string{"oid", "opcname", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"INT8", "NAME", "INT8", "INT8", "BOOL", "INT8"}, [][]any{
			{int64(10017), "inet_ops", int64(3550), int64(869), false, int64(0)},
			{int64(10060), "box_ops", int64(2593), int64(603), true, int64(0)},
			{int64(10061), "point_ops", int64(1029), int64(600), true, int64(603)},
			{int64(10062), "poly_ops", int64(2594), int64(604), true, int64(603)},
			{int64(10063), "circle_ops", int64(2595), int64(718), true, int64(603)},
			{int64(10072), "tsvector_ops", int64(3655), int64(3614), true, int64(3642)},
			{int64(10075), "tsquery_ops", int64(3702), int64(3615), true, int64(20)},
			{int64(10078), "range_ops", int64(3919), int64(3831), true, int64(0)},
			{int64(10082), "multirange_ops", int64(6158), int64(4537), true, int64(3831)},
		}))
		run(fixture.Select(`SELECT oid::bigint,amopfamily::bigint,amoplefttype::bigint,amoprighttype::bigint,amopstrategy,amoppurpose,amopopr::bigint,amopmethod::bigint,amopsortfamily::bigint FROM pg_amop WHERE amopmethod=783 ORDER BY oid`, []string{"oid", "amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amoppurpose", "amopopr", "amopmethod", "amopsortfamily"}, []string{"INT8", "INT8", "INT8", "INT8", "INT2", "CHAR", "INT8", "INT8", "INT8"}, [][]any{
			{int64(10299), int64(2593), int64(603), int64(603), int64(1), "s", int64(493), int64(783), int64(0)},
			{int64(10300), int64(2593), int64(603), int64(603), int64(2), "s", int64(494), int64(783), int64(0)},
			{int64(10301), int64(2593), int64(603), int64(603), int64(3), "s", int64(500), int64(783), int64(0)},
			{int64(10302), int64(2593), int64(603), int64(603), int64(4), "s", int64(495), int64(783), int64(0)},
			{int64(10303), int64(2593), int64(603), int64(603), int64(5), "s", int64(496), int64(783), int64(0)},
			{int64(10304), int64(2593), int64(603), int64(603), int64(6), "s", int64(499), int64(783), int64(0)},
			{int64(10305), int64(2593), int64(603), int64(603), int64(7), "s", int64(498), int64(783), int64(0)},
			{int64(10306), int64(2593), int64(603), int64(603), int64(8), "s", int64(497), int64(783), int64(0)},
			{int64(10307), int64(2593), int64(603), int64(603), int64(9), "s", int64(2571), int64(783), int64(0)},
			{int64(10308), int64(2593), int64(603), int64(603), int64(10), "s", int64(2570), int64(783), int64(0)},
			{int64(10309), int64(2593), int64(603), int64(603), int64(11), "s", int64(2573), int64(783), int64(0)},
			{int64(10310), int64(2593), int64(603), int64(603), int64(12), "s", int64(2572), int64(783), int64(0)},
			{int64(10311), int64(2593), int64(603), int64(600), int64(15), "o", int64(606), int64(783), int64(1970)},
			{int64(10312), int64(1029), int64(600), int64(600), int64(11), "s", int64(4161), int64(783), int64(0)},
			{int64(10313), int64(1029), int64(600), int64(600), int64(30), "s", int64(506), int64(783), int64(0)},
			{int64(10314), int64(1029), int64(600), int64(600), int64(1), "s", int64(507), int64(783), int64(0)},
			{int64(10315), int64(1029), int64(600), int64(600), int64(5), "s", int64(508), int64(783), int64(0)},
			{int64(10316), int64(1029), int64(600), int64(600), int64(10), "s", int64(4162), int64(783), int64(0)},
			{int64(10317), int64(1029), int64(600), int64(600), int64(29), "s", int64(509), int64(783), int64(0)},
			{int64(10318), int64(1029), int64(600), int64(600), int64(6), "s", int64(510), int64(783), int64(0)},
			{int64(10319), int64(1029), int64(600), int64(600), int64(15), "o", int64(517), int64(783), int64(1970)},
			{int64(10320), int64(1029), int64(600), int64(603), int64(28), "s", int64(511), int64(783), int64(0)},
			{int64(10321), int64(1029), int64(600), int64(604), int64(48), "s", int64(756), int64(783), int64(0)},
			{int64(10322), int64(1029), int64(600), int64(718), int64(68), "s", int64(758), int64(783), int64(0)},
			{int64(10323), int64(2594), int64(604), int64(604), int64(1), "s", int64(485), int64(783), int64(0)},
			{int64(10324), int64(2594), int64(604), int64(604), int64(2), "s", int64(486), int64(783), int64(0)},
			{int64(10325), int64(2594), int64(604), int64(604), int64(3), "s", int64(492), int64(783), int64(0)},
			{int64(10326), int64(2594), int64(604), int64(604), int64(4), "s", int64(487), int64(783), int64(0)},
			{int64(10327), int64(2594), int64(604), int64(604), int64(5), "s", int64(488), int64(783), int64(0)},
			{int64(10328), int64(2594), int64(604), int64(604), int64(6), "s", int64(491), int64(783), int64(0)},
			{int64(10329), int64(2594), int64(604), int64(604), int64(7), "s", int64(490), int64(783), int64(0)},
			{int64(10330), int64(2594), int64(604), int64(604), int64(8), "s", int64(489), int64(783), int64(0)},
			{int64(10331), int64(2594), int64(604), int64(604), int64(9), "s", int64(2575), int64(783), int64(0)},
			{int64(10332), int64(2594), int64(604), int64(604), int64(10), "s", int64(2574), int64(783), int64(0)},
			{int64(10333), int64(2594), int64(604), int64(604), int64(11), "s", int64(2577), int64(783), int64(0)},
			{int64(10334), int64(2594), int64(604), int64(604), int64(12), "s", int64(2576), int64(783), int64(0)},
			{int64(10335), int64(2594), int64(604), int64(600), int64(15), "o", int64(3289), int64(783), int64(1970)},
			{int64(10336), int64(2595), int64(718), int64(718), int64(1), "s", int64(1506), int64(783), int64(0)},
			{int64(10337), int64(2595), int64(718), int64(718), int64(2), "s", int64(1507), int64(783), int64(0)},
			{int64(10338), int64(2595), int64(718), int64(718), int64(3), "s", int64(1513), int64(783), int64(0)},
			{int64(10339), int64(2595), int64(718), int64(718), int64(4), "s", int64(1508), int64(783), int64(0)},
			{int64(10340), int64(2595), int64(718), int64(718), int64(5), "s", int64(1509), int64(783), int64(0)},
			{int64(10341), int64(2595), int64(718), int64(718), int64(6), "s", int64(1512), int64(783), int64(0)},
			{int64(10342), int64(2595), int64(718), int64(718), int64(7), "s", int64(1511), int64(783), int64(0)},
			{int64(10343), int64(2595), int64(718), int64(718), int64(8), "s", int64(1510), int64(783), int64(0)},
			{int64(10344), int64(2595), int64(718), int64(718), int64(9), "s", int64(2589), int64(783), int64(0)},
			{int64(10345), int64(2595), int64(718), int64(718), int64(10), "s", int64(1515), int64(783), int64(0)},
			{int64(10346), int64(2595), int64(718), int64(718), int64(11), "s", int64(1514), int64(783), int64(0)},
			{int64(10347), int64(2595), int64(718), int64(718), int64(12), "s", int64(2590), int64(783), int64(0)},
			{int64(10348), int64(2595), int64(718), int64(600), int64(15), "o", int64(3291), int64(783), int64(1970)},
			{int64(10364), int64(3655), int64(3614), int64(3615), int64(1), "s", int64(3636), int64(783), int64(0)},
			{int64(10372), int64(3702), int64(3615), int64(3615), int64(7), "s", int64(3693), int64(783), int64(0)},
			{int64(10373), int64(3702), int64(3615), int64(3615), int64(8), "s", int64(3694), int64(783), int64(0)},
			{int64(10380), int64(3919), int64(3831), int64(3831), int64(1), "s", int64(3893), int64(783), int64(0)},
			{int64(10381), int64(3919), int64(3831), int64(4537), int64(1), "s", int64(4395), int64(783), int64(0)},
			{int64(10382), int64(3919), int64(3831), int64(3831), int64(2), "s", int64(3895), int64(783), int64(0)},
			{int64(10383), int64(3919), int64(3831), int64(4537), int64(2), "s", int64(2875), int64(783), int64(0)},
			{int64(10384), int64(3919), int64(3831), int64(3831), int64(3), "s", int64(3888), int64(783), int64(0)},
			{int64(10385), int64(3919), int64(3831), int64(4537), int64(3), "s", int64(2866), int64(783), int64(0)},
			{int64(10386), int64(3919), int64(3831), int64(3831), int64(4), "s", int64(3896), int64(783), int64(0)},
			{int64(10387), int64(3919), int64(3831), int64(4537), int64(4), "s", int64(3585), int64(783), int64(0)},
			{int64(10388), int64(3919), int64(3831), int64(3831), int64(5), "s", int64(3894), int64(783), int64(0)},
			{int64(10389), int64(3919), int64(3831), int64(4537), int64(5), "s", int64(4398), int64(783), int64(0)},
			{int64(10390), int64(3919), int64(3831), int64(3831), int64(6), "s", int64(3897), int64(783), int64(0)},
			{int64(10391), int64(3919), int64(3831), int64(4537), int64(6), "s", int64(4179), int64(783), int64(0)},
			{int64(10392), int64(3919), int64(3831), int64(3831), int64(7), "s", int64(3890), int64(783), int64(0)},
			{int64(10393), int64(3919), int64(3831), int64(4537), int64(7), "s", int64(4539), int64(783), int64(0)},
			{int64(10394), int64(3919), int64(3831), int64(3831), int64(8), "s", int64(3892), int64(783), int64(0)},
			{int64(10395), int64(3919), int64(3831), int64(4537), int64(8), "s", int64(2873), int64(783), int64(0)},
			{int64(10396), int64(3919), int64(3831), int64(2283), int64(16), "s", int64(3889), int64(783), int64(0)},
			{int64(10397), int64(3919), int64(3831), int64(3831), int64(18), "s", int64(3882), int64(783), int64(0)},
			{int64(10398), int64(6158), int64(4537), int64(4537), int64(1), "s", int64(4397), int64(783), int64(0)},
			{int64(10399), int64(6158), int64(4537), int64(3831), int64(1), "s", int64(4396), int64(783), int64(0)},
			{int64(10400), int64(6158), int64(4537), int64(4537), int64(2), "s", int64(2877), int64(783), int64(0)},
			{int64(10401), int64(6158), int64(4537), int64(3831), int64(2), "s", int64(2876), int64(783), int64(0)},
			{int64(10402), int64(6158), int64(4537), int64(4537), int64(3), "s", int64(2868), int64(783), int64(0)},
			{int64(10403), int64(6158), int64(4537), int64(3831), int64(3), "s", int64(2867), int64(783), int64(0)},
			{int64(10404), int64(6158), int64(4537), int64(4537), int64(4), "s", int64(4142), int64(783), int64(0)},
			{int64(10405), int64(6158), int64(4537), int64(3831), int64(4), "s", int64(4035), int64(783), int64(0)},
			{int64(10406), int64(6158), int64(4537), int64(4537), int64(5), "s", int64(4400), int64(783), int64(0)},
			{int64(10407), int64(6158), int64(4537), int64(3831), int64(5), "s", int64(4399), int64(783), int64(0)},
			{int64(10408), int64(6158), int64(4537), int64(4537), int64(6), "s", int64(4198), int64(783), int64(0)},
			{int64(10409), int64(6158), int64(4537), int64(3831), int64(6), "s", int64(4180), int64(783), int64(0)},
			{int64(10410), int64(6158), int64(4537), int64(4537), int64(7), "s", int64(2871), int64(783), int64(0)},
			{int64(10411), int64(6158), int64(4537), int64(3831), int64(7), "s", int64(2870), int64(783), int64(0)},
			{int64(10412), int64(6158), int64(4537), int64(4537), int64(8), "s", int64(2874), int64(783), int64(0)},
			{int64(10413), int64(6158), int64(4537), int64(3831), int64(8), "s", int64(4540), int64(783), int64(0)},
			{int64(10414), int64(6158), int64(4537), int64(2283), int64(16), "s", int64(2869), int64(783), int64(0)},
			{int64(10415), int64(6158), int64(4537), int64(4537), int64(18), "s", int64(2860), int64(783), int64(0)},
			{int64(10501), int64(3550), int64(869), int64(869), int64(3), "s", int64(3552), int64(783), int64(0)},
			{int64(10502), int64(3550), int64(869), int64(869), int64(18), "s", int64(1201), int64(783), int64(0)},
			{int64(10503), int64(3550), int64(869), int64(869), int64(19), "s", int64(1202), int64(783), int64(0)},
			{int64(10504), int64(3550), int64(869), int64(869), int64(20), "s", int64(1203), int64(783), int64(0)},
			{int64(10505), int64(3550), int64(869), int64(869), int64(21), "s", int64(1204), int64(783), int64(0)},
			{int64(10506), int64(3550), int64(869), int64(869), int64(22), "s", int64(1205), int64(783), int64(0)},
			{int64(10507), int64(3550), int64(869), int64(869), int64(23), "s", int64(1206), int64(783), int64(0)},
			{int64(10508), int64(3550), int64(869), int64(869), int64(24), "s", int64(931), int64(783), int64(0)},
			{int64(10509), int64(3550), int64(869), int64(869), int64(25), "s", int64(932), int64(783), int64(0)},
			{int64(10510), int64(3550), int64(869), int64(869), int64(26), "s", int64(933), int64(783), int64(0)},
			{int64(10511), int64(3550), int64(869), int64(869), int64(27), "s", int64(934), int64(783), int64(0)},
		}))
		run(familyBuiltinVisible(fixture.Select(`SELECT p.oid::bigint,p.amprocfamily::bigint,p.amproclefttype::bigint,p.amprocrighttype::bigint,p.amprocnum,p.amproc::oid::bigint FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfmethod=783 ORDER BY p.oid`, []string{"oid", "amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"INT8", "INT8", "INT8", "INT8", "INT2", "INT8"}, [][]any{
			{int64(10208), int64(1029), int64(600), int64(600), int64(1), int64(2179)},
			{int64(10209), int64(1029), int64(600), int64(600), int64(2), int64(2583)},
			{int64(10210), int64(1029), int64(600), int64(600), int64(3), int64(1030)},
			{int64(10211), int64(1029), int64(600), int64(600), int64(5), int64(2581)},
			{int64(10212), int64(1029), int64(600), int64(600), int64(6), int64(2582)},
			{int64(10213), int64(1029), int64(600), int64(600), int64(7), int64(2584)},
			{int64(10214), int64(1029), int64(600), int64(600), int64(8), int64(3064)},
			{int64(10215), int64(1029), int64(600), int64(600), int64(9), int64(3282)},
			{int64(10216), int64(1029), int64(600), int64(600), int64(11), int64(3435)},
			{int64(10217), int64(2593), int64(603), int64(603), int64(1), int64(2578)},
			{int64(10218), int64(2593), int64(603), int64(603), int64(2), int64(2583)},
			{int64(10219), int64(2593), int64(603), int64(603), int64(5), int64(2581)},
			{int64(10220), int64(2593), int64(603), int64(603), int64(6), int64(2582)},
			{int64(10221), int64(2593), int64(603), int64(603), int64(7), int64(2584)},
			{int64(10222), int64(2593), int64(603), int64(603), int64(8), int64(3998)},
			{int64(10223), int64(2593), int64(2276), int64(2276), int64(12), int64(6347)},
			{int64(10224), int64(2594), int64(604), int64(604), int64(1), int64(2585)},
			{int64(10225), int64(2594), int64(604), int64(604), int64(2), int64(2583)},
			{int64(10226), int64(2594), int64(604), int64(604), int64(3), int64(2586)},
			{int64(10227), int64(2594), int64(604), int64(604), int64(5), int64(2581)},
			{int64(10228), int64(2594), int64(604), int64(604), int64(6), int64(2582)},
			{int64(10229), int64(2594), int64(604), int64(604), int64(7), int64(2584)},
			{int64(10230), int64(2594), int64(604), int64(604), int64(8), int64(3288)},
			{int64(10231), int64(2594), int64(2276), int64(2276), int64(12), int64(6347)},
			{int64(10232), int64(2595), int64(718), int64(718), int64(1), int64(2591)},
			{int64(10233), int64(2595), int64(718), int64(718), int64(2), int64(2583)},
			{int64(10234), int64(2595), int64(718), int64(718), int64(3), int64(2592)},
			{int64(10235), int64(2595), int64(718), int64(718), int64(5), int64(2581)},
			{int64(10236), int64(2595), int64(718), int64(718), int64(6), int64(2582)},
			{int64(10237), int64(2595), int64(718), int64(718), int64(7), int64(2584)},
			{int64(10238), int64(2595), int64(718), int64(718), int64(8), int64(3280)},
			{int64(10239), int64(2595), int64(2276), int64(2276), int64(12), int64(6347)},
			{int64(10240), int64(3655), int64(3614), int64(3614), int64(1), int64(3654)},
			{int64(10241), int64(3655), int64(3614), int64(3614), int64(2), int64(3651)},
			{int64(10242), int64(3655), int64(3614), int64(3614), int64(3), int64(3648)},
			{int64(10243), int64(3655), int64(3614), int64(3614), int64(4), int64(3649)},
			{int64(10244), int64(3655), int64(3614), int64(3614), int64(5), int64(3653)},
			{int64(10245), int64(3655), int64(3614), int64(3614), int64(6), int64(3650)},
			{int64(10246), int64(3655), int64(3614), int64(3614), int64(7), int64(3652)},
			{int64(10247), int64(3655), int64(3614), int64(3614), int64(10), int64(3434)},
			{int64(10248), int64(3702), int64(3615), int64(3615), int64(1), int64(3701)},
			{int64(10249), int64(3702), int64(3615), int64(3615), int64(2), int64(3698)},
			{int64(10250), int64(3702), int64(3615), int64(3615), int64(3), int64(3695)},
			{int64(10251), int64(3702), int64(3615), int64(3615), int64(5), int64(3700)},
			{int64(10252), int64(3702), int64(3615), int64(3615), int64(6), int64(3697)},
			{int64(10253), int64(3702), int64(3615), int64(3615), int64(7), int64(3699)},
			{int64(10254), int64(3919), int64(3831), int64(3831), int64(1), int64(3875)},
			{int64(10255), int64(3919), int64(3831), int64(3831), int64(2), int64(3876)},
			{int64(10256), int64(3919), int64(3831), int64(3831), int64(5), int64(3879)},
			{int64(10257), int64(3919), int64(3831), int64(3831), int64(6), int64(3880)},
			{int64(10258), int64(3919), int64(3831), int64(3831), int64(7), int64(3881)},
			{int64(10259), int64(3919), int64(3831), int64(3831), int64(11), int64(6391)},
			{int64(10260), int64(3919), int64(2276), int64(2276), int64(12), int64(6347)},
			{int64(10261), int64(3550), int64(869), int64(869), int64(1), int64(3553)},
			{int64(10262), int64(3550), int64(869), int64(869), int64(2), int64(3554)},
			{int64(10263), int64(3550), int64(869), int64(869), int64(3), int64(3555)},
			{int64(10264), int64(3550), int64(869), int64(869), int64(5), int64(3557)},
			{int64(10265), int64(3550), int64(869), int64(869), int64(6), int64(3558)},
			{int64(10266), int64(3550), int64(869), int64(869), int64(7), int64(3559)},
			{int64(10267), int64(3550), int64(869), int64(869), int64(9), int64(3573)},
			{int64(10268), int64(3550), int64(2276), int64(2276), int64(12), int64(6347)},
			{int64(10269), int64(6158), int64(4537), int64(4537), int64(1), int64(6154)},
			{int64(10270), int64(6158), int64(4537), int64(4537), int64(2), int64(3876)},
			{int64(10271), int64(6158), int64(4537), int64(4537), int64(3), int64(6156)},
			{int64(10272), int64(6158), int64(4537), int64(4537), int64(5), int64(3879)},
			{int64(10273), int64(6158), int64(4537), int64(4537), int64(6), int64(3880)},
			{int64(10274), int64(6158), int64(4537), int64(4537), int64(7), int64(3881)},
			{int64(10275), int64(6158), int64(2276), int64(2276), int64(12), int64(6347)},
		}), b.PG))
		run(fixture.Exec(`CREATE TABLE gist_properties(p point,b box,g polygon,c circle,i inet,r int4range,m int4multirange,q tsquery,v tsvector,n integer)`, 0))
		for _, tc := range []struct {
			name, key string
			ret, dist bool
		}{
			{"p", "p", true, true}, {"b", "b", true, true}, {"g", "g", false, true}, {"c", "c", false, true},
			{"i", "i inet_ops", true, false}, {"r", "r", true, false}, {"m", "m", false, false}, {"q", "q", false, false}, {"v", "v", false, false},
		} {
			suffix := ""
			if tc.name == "p" {
				suffix = " INCLUDE(n)"
			}
			run(fixture.Exec("CREATE INDEX gist_prop_"+tc.name+" ON gist_properties USING gist("+tc.key+")"+suffix, 0))
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_index_column_has_property('gist_prop_"+tc.name+"'::regclass,1,'returnable') AS r,pg_index_column_has_property('gist_prop_"+tc.name+"'::regclass,1,'distance_orderable') AS d,pg_index_column_has_property('gist_prop_"+tc.name+"'::regclass,1,'orderable') AS o", []string{"r", "d", "o"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{tc.ret, tc.dist, false}}))
		}
		classRoutes(t, p, b, conn, fixture.Select(`SELECT pg_index_column_has_property('gist_prop_p'::regclass,2,'returnable') AS r,pg_index_column_has_property('gist_prop_p'::regclass,2,'distance_orderable') AS d,pg_index_column_has_property('gist_prop_p'::regclass,2,'orderable') AS o`, []string{"r", "d", "o"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, false}}))
		classRoutes(t, p, b, conn, fixture.Select(`SELECT c.relname,a.attnum,a.atttypid::bigint,a.attlen,a.attbyval,a.attalign,a.attstorage,a.atttypmod FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN ('gist_prop_p','gist_prop_b','gist_prop_g','gist_prop_c','gist_prop_i','gist_prop_r','gist_prop_m','gist_prop_q','gist_prop_v') AND a.attnum>0 ORDER BY c.relname,a.attnum`, []string{"relname", "attnum", "atttypid", "attlen", "attbyval", "attalign", "attstorage", "atttypmod"}, []string{"NAME", "INT2", "INT8", "INT2", "BOOL", "CHAR", "CHAR", "INT4"}, [][]any{
			{"gist_prop_b", int64(1), int64(603), int64(32), false, "d", "p", int64(-1)},
			{"gist_prop_c", int64(1), int64(603), int64(32), false, "d", "p", int64(-1)},
			{"gist_prop_g", int64(1), int64(603), int64(32), false, "d", "p", int64(-1)},
			{"gist_prop_i", int64(1), int64(869), int64(-1), false, "i", "m", int64(-1)},
			{"gist_prop_m", int64(1), int64(3831), int64(-1), false, "d", "x", int64(-1)},
			{"gist_prop_p", int64(1), int64(603), int64(32), false, "d", "p", int64(-1)},
			{"gist_prop_p", int64(2), int64(23), int64(4), true, "i", "p", int64(-1)},
			{"gist_prop_q", int64(1), int64(20), int64(8), true, "d", "p", int64(-1)},
			{"gist_prop_r", int64(1), int64(3904), int64(-1), false, "i", "x", int64(-1)},
			{"gist_prop_v", int64(1), int64(3642), int64(-1), false, "i", "p", int64(-1)},
		}))
		run(fixture.Exec(`CREATE OPERATOR CLASS public.gist_alias FOR TYPE point USING gist FAMILY pg_catalog.point_ops AS STORAGE box`, 0))
		run(fixture.Exec(`CREATE INDEX gist_alias_i ON gist_properties USING gist(p public.gist_alias)`, 0))
		const utility = `CREATE INDEX gist_alias_later ON gist_properties USING gist(p public.gist_alias)`
		held := classHeld(t, p, conn, utility)
		run(fixture.Exec(`ALTER OPERATOR CLASS public.gist_alias USING gist RENAME TO gist_renamed`, 0))
		failure := fixture.ImplicitError(utility, fixture.Diagnostic{Code: "42704", Message: `operator class "public.gist_alias" does not exist for access method "gist"`})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
		classRoutes(t, p, b, conn, fixture.Select(`SELECT pg_get_indexdef('gist_alias_i'::regclass) AS definition,pg_index_column_has_property('gist_alias_i'::regclass,1,'distance_orderable') AS distance`, []string{"definition", "distance"}, []string{"TEXT", "BOOL"}, [][]any{{"CREATE INDEX gist_alias_i ON public.gist_properties USING gist (p gist_renamed)", true}}))
		run(fixture.Exec(`CREATE OPERATOR CLASS public.gist_alias FOR TYPE point USING gist FAMILY pg_catalog.point_ops AS STORAGE box`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(utility, 0))
		classRoutes(t, p, b, conn, fixture.Select(`SELECT i.indclass[0]=c.oid AS same FROM pg_index i JOIN pg_opclass c ON c.opcname='gist_alias' AND c.opcnamespace='public'::regnamespace WHERE i.indexrelid='gist_alias_later'::regclass`, []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec(`DROP OPERATOR CLASS public.gist_renamed USING gist CASCADE`, 0))
		classRoutes(t, p, b, conn, fixture.Select(`SELECT relname FROM pg_class WHERE relname IN ('gist_alias_i','gist_alias_later') ORDER BY relname`, []string{"relname"}, []string{"NAME"}, [][]any{{"gist_alias_later"}}))
	})
}
