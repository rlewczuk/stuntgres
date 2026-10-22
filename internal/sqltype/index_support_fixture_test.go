package sqltype

// Exact released same-type support rows extracted from pinned PostgreSQL18.6.
// These fixtures supply immutable selected members to representation-only units;
// actual catalog selection and routine signatures are tested in engine units.
var indexSupportUnitRows = [...]struct {
	Class   uint32
	Slot    int
	Support IndexSupport
}{
	{10003, 1, IndexSupport{Family: 424, Member: 10003, Routine: 1693, Left: 16, Right: 16, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10004, 1, IndexSupport{Family: 426, Member: 10006, Routine: 1078, Left: 1042, Right: 1042, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10005, 1, IndexSupport{Family: 427, Member: 10132, Routine: 1080, Left: 1042, Right: 1042, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10005, 2, IndexSupport{Family: 427, Member: 10133, Routine: 972, Left: 1042, Right: 1042, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10006, 1, IndexSupport{Family: 428, Member: 10009, Routine: 1954, Left: 17, Right: 17, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10007, 1, IndexSupport{Family: 429, Member: 10012, Routine: 358, Left: 18, Right: 18, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10008, 1, IndexSupport{Family: 431, Member: 10134, Routine: 454, Left: 18, Right: 18, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10008, 2, IndexSupport{Family: 431, Member: 10135, Routine: 446, Left: 18, Right: 18, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{3122, 1, IndexSupport{Family: 434, Member: 10015, Routine: 1092, Left: 1082, Right: 1082, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{3127, 1, IndexSupport{Family: 434, Member: 10027, Routine: 1314, Left: 1184, Right: 1184, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{3128, 1, IndexSupport{Family: 434, Member: 10021, Routine: 2045, Left: 1114, Right: 1114, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10011, 1, IndexSupport{Family: 435, Member: 10136, Routine: 6415, Left: 1082, Right: 1082, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10011, 2, IndexSupport{Family: 435, Member: 10137, Routine: 6416, Left: 1082, Right: 1082, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{3123, 1, IndexSupport{Family: 1970, Member: 10039, Routine: 355, Left: 701, Right: 701, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10012, 1, IndexSupport{Family: 1970, Member: 10036, Routine: 354, Left: 700, Right: 700, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10013, 1, IndexSupport{Family: 1971, Member: 10140, Routine: 451, Left: 700, Right: 700, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10013, 2, IndexSupport{Family: 1971, Member: 10141, Routine: 443, Left: 700, Right: 700, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10014, 1, IndexSupport{Family: 1971, Member: 10142, Routine: 452, Left: 701, Right: 701, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10014, 2, IndexSupport{Family: 1971, Member: 10143, Routine: 444, Left: 701, Right: 701, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10009, 1, IndexSupport{Family: 1974, Member: 10044, Routine: 926, Left: 869, Right: 869, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10015, 1, IndexSupport{Family: 1974, Member: 10044, Routine: 926, Left: 869, Right: 869, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{1978, 1, IndexSupport{Family: 1976, Member: 10056, Routine: 351, Left: 23, Right: 23, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{1979, 1, IndexSupport{Family: 1976, Member: 10047, Routine: 350, Left: 21, Right: 21, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{3124, 1, IndexSupport{Family: 1976, Member: 10065, Routine: 842, Left: 20, Right: 20, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10019, 1, IndexSupport{Family: 1977, Member: 10146, Routine: 449, Left: 21, Right: 21, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10019, 2, IndexSupport{Family: 1977, Member: 10147, Routine: 441, Left: 21, Right: 21, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10020, 1, IndexSupport{Family: 1977, Member: 10148, Routine: 450, Left: 23, Right: 23, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10020, 2, IndexSupport{Family: 1977, Member: 10149, Routine: 425, Left: 23, Right: 23, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10021, 1, IndexSupport{Family: 1977, Member: 10150, Routine: 949, Left: 20, Right: 20, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10021, 2, IndexSupport{Family: 1977, Member: 10151, Routine: 442, Left: 20, Right: 20, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10022, 1, IndexSupport{Family: 1982, Member: 10072, Routine: 1315, Left: 1186, Right: 1186, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10023, 1, IndexSupport{Family: 1983, Member: 10152, Routine: 1697, Left: 1186, Right: 1186, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10023, 2, IndexSupport{Family: 1983, Member: 10153, Routine: 3418, Left: 1186, Right: 1186, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10024, 1, IndexSupport{Family: 1984, Member: 10074, Routine: 836, Left: 829, Right: 829, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{3125, 1, IndexSupport{Family: 1988, Member: 10077, Routine: 1769, Left: 1700, Right: 1700, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{1981, 1, IndexSupport{Family: 1989, Member: 10080, Routine: 356, Left: 26, Right: 26, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10031, 1, IndexSupport{Family: 1990, Member: 10156, Routine: 453, Left: 26, Right: 26, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10031, 2, IndexSupport{Family: 1990, Member: 10157, Routine: 445, Left: 26, Right: 26, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{3126, 1, IndexSupport{Family: 1994, Member: 10086, Routine: 360, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10028, 1, IndexSupport{Family: 1994, Member: 10089, Routine: 359, Left: 19, Right: 19, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10044, 1, IndexSupport{Family: 1994, Member: 10086, Routine: 360, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10029, 1, IndexSupport{Family: 1995, Member: 10162, Routine: 455, Left: 19, Right: 19, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10029, 2, IndexSupport{Family: 1995, Member: 10163, Routine: 447, Left: 19, Right: 19, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10037, 1, IndexSupport{Family: 1995, Member: 10160, Routine: 400, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10037, 2, IndexSupport{Family: 1995, Member: 10161, Routine: 448, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10045, 1, IndexSupport{Family: 1995, Member: 10160, Routine: 400, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10045, 2, IndexSupport{Family: 1995, Member: 10161, Routine: 448, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10038, 1, IndexSupport{Family: 1996, Member: 10094, Routine: 1107, Left: 1083, Right: 1083, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10039, 1, IndexSupport{Family: 1997, Member: 10164, Routine: 1688, Left: 1083, Right: 1083, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10039, 2, IndexSupport{Family: 1997, Member: 10165, Routine: 3409, Left: 1083, Right: 1083, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10030, 1, IndexSupport{Family: 1998, Member: 10166, Routine: 432, Left: 1700, Right: 1700, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10030, 2, IndexSupport{Family: 1998, Member: 10167, Routine: 780, Left: 1700, Right: 1700, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10040, 1, IndexSupport{Family: 1999, Member: 10168, Routine: 6425, Left: 1184, Right: 1184, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10040, 2, IndexSupport{Family: 1999, Member: 10169, Routine: 6426, Left: 1184, Right: 1184, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10041, 1, IndexSupport{Family: 2000, Member: 10097, Routine: 1358, Left: 1266, Right: 1266, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10042, 1, IndexSupport{Family: 2001, Member: 10170, Routine: 1696, Left: 1266, Right: 1266, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10042, 2, IndexSupport{Family: 2001, Member: 10171, Routine: 3410, Left: 1266, Right: 1266, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10046, 1, IndexSupport{Family: 2040, Member: 10172, Routine: 2039, Left: 1114, Right: 1114, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10046, 2, IndexSupport{Family: 2040, Member: 10173, Routine: 3411, Left: 1114, Right: 1114, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{4217, 1, IndexSupport{Family: 2095, Member: 10102, Routine: 2166, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{4218, 1, IndexSupport{Family: 2095, Member: 10102, Routine: 2166, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{4219, 1, IndexSupport{Family: 2097, Member: 10105, Routine: 2180, Left: 1042, Right: 1042, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10047, 1, IndexSupport{Family: 2099, Member: 10108, Routine: 377, Left: 790, Right: 790, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10048, 1, IndexSupport{Family: 2222, Member: 10174, Routine: 6417, Left: 16, Right: 16, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10048, 2, IndexSupport{Family: 2222, Member: 10175, Routine: 6418, Left: 16, Right: 16, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10049, 1, IndexSupport{Family: 2223, Member: 10176, Routine: 6413, Left: 17, Right: 17, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10049, 2, IndexSupport{Family: 2223, Member: 10177, Routine: 6414, Left: 17, Right: 17, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10051, 1, IndexSupport{Family: 2225, Member: 10178, Routine: 6419, Left: 28, Right: 28, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10051, 2, IndexSupport{Family: 2225, Member: 10179, Routine: 6420, Left: 28, Right: 28, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10054, 1, IndexSupport{Family: 2226, Member: 10182, Routine: 6423, Left: 29, Right: 29, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10054, 2, IndexSupport{Family: 2226, Member: 10183, Routine: 6424, Left: 29, Right: 29, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10055, 1, IndexSupport{Family: 2227, Member: 10184, Routine: 2233, Left: 27, Right: 27, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10055, 2, IndexSupport{Family: 2227, Member: 10185, Routine: 2234, Left: 27, Right: 27, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10056, 1, IndexSupport{Family: 2229, Member: 10186, Routine: 400, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10056, 2, IndexSupport{Family: 2229, Member: 10187, Routine: 448, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10057, 1, IndexSupport{Family: 2229, Member: 10186, Routine: 400, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10057, 2, IndexSupport{Family: 2229, Member: 10187, Routine: 448, Left: 25, Right: 25, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10058, 1, IndexSupport{Family: 2231, Member: 10188, Routine: 1080, Left: 1042, Right: 1042, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10058, 2, IndexSupport{Family: 2231, Member: 10189, Routine: 972, Left: 1042, Right: 1042, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10050, 1, IndexSupport{Family: 2789, Member: 10110, Routine: 2794, Left: 27, Right: 27, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10065, 1, IndexSupport{Family: 2968, Member: 10112, Routine: 2960, Left: 2950, Right: 2950, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10066, 1, IndexSupport{Family: 2969, Member: 10192, Routine: 2963, Left: 2950, Right: 2950, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10066, 2, IndexSupport{Family: 2969, Member: 10193, Routine: 3412, Left: 2950, Right: 2950, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10069, 1, IndexSupport{Family: 3522, Member: 10122, Routine: 3514, Left: 3500, Right: 3500, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10070, 1, IndexSupport{Family: 3523, Member: 10200, Routine: 3515, Left: 3500, Right: 3500, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10070, 2, IndexSupport{Family: 3523, Member: 10201, Routine: 3414, Left: 3500, Right: 3500, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10088, 1, IndexSupport{Family: 4033, Member: 10129, Routine: 4044, Left: 3802, Right: 3802, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10089, 1, IndexSupport{Family: 4034, Member: 10206, Routine: 4045, Left: 3802, Right: 3802, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10089, 2, IndexSupport{Family: 4034, Member: 10207, Routine: 3416, Left: 3802, Right: 3802, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10052, 1, IndexSupport{Family: 5032, Member: 10180, Routine: 6421, Left: 5069, Right: 5069, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10052, 2, IndexSupport{Family: 5032, Member: 10181, Routine: 6422, Left: 5069, Right: 5069, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
	{10053, 1, IndexSupport{Family: 5067, Member: 10130, Routine: 5096, Left: 5069, Right: 5069, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}},
}

func selectedTestIndexClass(class IndexClass) IndexClass {
	for _, row := range indexSupportUnitRows {
		if row.Class == class.OID() {
			class = class.WithSupport(row.Slot, row.Support)
		}
	}
	return class
}
func bindTestIndexOrdering(usage Usage, class IndexClass) (IndexComparison, error) {
	return BindIndexOrdering(usage, selectedTestIndexClass(class))
}
func bindTestIndexHash(usage Usage, class IndexClass) (IndexHash, error) {
	return BindIndexHash(usage, selectedTestIndexClass(class))
}
func bindTestPartitionHash(usage Usage, class IndexClass) (PartitionHash, error) {
	return BindPartitionHash(usage, selectedTestIndexClass(class))
}

var indexOperatorUnitRows = [...]struct {
	Family, Left, Right, Member uint32
	Strategy                    int16
}{
	{424, 16, 16, 10200, 1},
	{424, 16, 16, 10202, 3},
	{426, 1042, 1042, 10110, 1},
	{426, 1042, 1042, 10112, 3},
	{427, 1042, 1042, 10255, 1},
	{428, 17, 17, 10115, 1},
	{428, 17, 17, 10117, 3},
	{429, 18, 18, 10085, 1},
	{429, 18, 18, 10087, 3},
	{431, 18, 18, 10256, 1},
	{434, 1082, 1082, 10120, 1},
	{434, 1082, 1082, 10122, 3},
	{434, 1082, 1114, 10125, 1},
	{434, 1082, 1114, 10127, 3},
	{434, 1082, 1184, 10130, 1},
	{434, 1082, 1184, 10132, 3},
	{434, 1114, 1082, 10140, 1},
	{434, 1114, 1082, 10142, 3},
	{434, 1114, 1114, 10135, 1},
	{434, 1114, 1114, 10137, 3},
	{434, 1114, 1184, 10145, 1},
	{434, 1114, 1184, 10147, 3},
	{434, 1184, 1082, 10155, 1},
	{434, 1184, 1082, 10157, 3},
	{434, 1184, 1114, 10160, 1},
	{434, 1184, 1114, 10162, 3},
	{434, 1184, 1184, 10150, 1},
	{434, 1184, 1184, 10152, 3},
	{435, 1082, 1082, 10257, 1},
	{1970, 700, 700, 10065, 1},
	{1970, 700, 700, 10067, 3},
	{1970, 700, 701, 10070, 1},
	{1970, 700, 701, 10072, 3},
	{1970, 701, 700, 10080, 1},
	{1970, 701, 700, 10082, 3},
	{1970, 701, 701, 10075, 1},
	{1970, 701, 701, 10077, 3},
	{1971, 700, 700, 10258, 1},
	{1971, 700, 701, 10260, 1},
	{1971, 701, 700, 10261, 1},
	{1971, 701, 701, 10259, 1},
	{1974, 869, 869, 10190, 1},
	{1974, 869, 869, 10192, 3},
	{1976, 20, 20, 10030, 1},
	{1976, 20, 20, 10032, 3},
	{1976, 20, 21, 10035, 1},
	{1976, 20, 21, 10037, 3},
	{1976, 20, 23, 10040, 1},
	{1976, 20, 23, 10042, 3},
	{1976, 21, 20, 10010, 1},
	{1976, 21, 20, 10012, 3},
	{1976, 21, 21, 10000, 1},
	{1976, 21, 21, 10002, 3},
	{1976, 21, 23, 10005, 1},
	{1976, 21, 23, 10007, 3},
	{1976, 23, 20, 10025, 1},
	{1976, 23, 20, 10027, 3},
	{1976, 23, 21, 10020, 1},
	{1976, 23, 21, 10022, 3},
	{1976, 23, 23, 10015, 1},
	{1976, 23, 23, 10017, 3},
	{1977, 20, 20, 10265, 1},
	{1977, 20, 21, 10270, 1},
	{1977, 20, 23, 10271, 1},
	{1977, 21, 20, 10267, 1},
	{1977, 21, 21, 10263, 1},
	{1977, 21, 23, 10266, 1},
	{1977, 23, 20, 10269, 1},
	{1977, 23, 21, 10268, 1},
	{1977, 23, 23, 10264, 1},
	{1982, 1186, 1186, 10175, 1},
	{1982, 1186, 1186, 10177, 3},
	{1983, 1186, 1186, 10272, 1},
	{1988, 1700, 1700, 10195, 1},
	{1988, 1700, 1700, 10197, 3},
	{1989, 26, 26, 10045, 1},
	{1989, 26, 26, 10047, 3},
	{1990, 26, 26, 10275, 1},
	{1994, 19, 19, 10095, 1},
	{1994, 19, 19, 10097, 3},
	{1994, 19, 25, 10100, 1},
	{1994, 19, 25, 10102, 3},
	{1994, 25, 19, 10105, 1},
	{1994, 25, 19, 10107, 3},
	{1994, 25, 25, 10090, 1},
	{1994, 25, 25, 10092, 3},
	{1995, 19, 19, 10279, 1},
	{1995, 19, 25, 10280, 1},
	{1995, 25, 19, 10281, 1},
	{1995, 25, 25, 10278, 1},
	{1996, 1083, 1083, 10165, 1},
	{1996, 1083, 1083, 10167, 3},
	{1997, 1083, 1083, 10282, 1},
	{1998, 1700, 1700, 10297, 1},
	{1999, 1184, 1184, 10283, 1},
	{2000, 1266, 1266, 10170, 1},
	{2000, 1266, 1266, 10172, 3},
	{2001, 1266, 1266, 10284, 1},
	{2040, 1114, 1114, 10285, 1},
	{2095, 25, 25, 10215, 1},
	{2095, 25, 25, 10217, 3},
	{2097, 1042, 1042, 10220, 1},
	{2097, 1042, 1042, 10222, 3},
	{2099, 790, 790, 10225, 1},
	{2099, 790, 790, 10227, 3},
	{2222, 16, 16, 10286, 1},
	{2223, 17, 17, 10287, 1},
	{2225, 28, 28, 10288, 1},
	{2226, 29, 29, 10290, 1},
	{2227, 27, 27, 10291, 1},
	{2229, 25, 25, 10292, 1},
	{2231, 1042, 1042, 10293, 1},
	{2789, 27, 27, 10055, 1},
	{2789, 27, 27, 10057, 3},
	{2968, 2950, 2950, 10245, 1},
	{2968, 2950, 2950, 10247, 3},
	{2969, 2950, 2950, 10295, 1},
	{3522, 3500, 3500, 10353, 1},
	{3522, 3500, 3500, 10355, 3},
	{3523, 3500, 3500, 10358, 1},
	{3550, 869, 869, 10501, 3},
	{3550, 869, 869, 10502, 18},
	{3919, 3831, 3831, 10380, 1},
	{3919, 3831, 3831, 10384, 3},
	{3919, 3831, 3831, 10397, 18},
	{3919, 3831, 4537, 10381, 1},
	{3919, 3831, 4537, 10385, 3},
	{4033, 3802, 3802, 10450, 1},
	{4033, 3802, 3802, 10452, 3},
	{4034, 3802, 3802, 10455, 1},
	{5032, 5069, 5069, 10289, 1},
	{5067, 5069, 5069, 10050, 1},
	{5067, 5069, 5069, 10052, 3},
}

func bindTestExclusionOperator(usage Usage, class IndexClass, name string) (ExclusionOperator, error) {
	var fact IndexClassMetadata
	for i := range IndexClassMetadataCount() {
		m, _ := IndexClassMetadataAt(i)
		if m.OID == class.OID() {
			fact = m
			break
		}
	}
	input := usage.Type.BaseType().ID()
	if input == VarChar {
		input = Text
	}
	if input == Cidr && class.Accepts(usage.Type) {
		input = Inet
	}
	if usage.Type.BaseType().IsEnum() {
		input = 3500
	}
	if fact.Method == 783 && fact.Family == 3919 {
		input = 3831
	}
	if input == Regclass || input == Regtype || input == Regrole || IsRegObjectAlias(input) || IsRoutineAlias(input) || IsOperatorAlias(input) || IsRegTS(input) {
		input = Oid
	}
	strategy := int16(0)
	if name == "=" {
		strategy = 3
		if fact.Method == 405 {
			strategy = 1
		}
		if fact.Method == 783 && fact.Family == 3550 {
			strategy = 18
		}
	} else if name == "&&" && fact.Method == 783 {
		strategy = 3
	}
	var member uint32
	for _, r := range indexOperatorUnitRows {
		if r.Family == fact.Family && r.Left == uint32(input) && r.Right == uint32(input) && r.Strategy == strategy {
			member = r.Member
			break
		}
	}
	return BindExclusionOperator(usage, selectedTestIndexClass(class), name, member)
}
