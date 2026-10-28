package sqltype

// PostgreSQL REL_18_6 pg_cast.dat/pg_proc.dat. Generic domain, array and
// category-I/O operations deliberately have no catalog rows.
var releasedCastMetadata = releasedCatalogFacts()

func releasedCatalogFacts() []CastMetadata {
	rows := []CastMetadata{
		{0, Name, Text, 406, 'i', 'f'}, {0, Name, BPChar, 408, 'a', 'f'}, {0, Name, VarChar, 1401, 'a', 'f'},
		{0, Text, Name, 407, 'i', 'f'}, {0, VarChar, Name, 1400, 'i', 'f'}, {0, BPChar, Name, 409, 'i', 'f'},
		{0, Char, BPChar, 860, 'a', 'f'}, {0, Char, VarChar, 946, 'a', 'f'},
		{0, Text, Char, 944, 'a', 'f'}, {0, VarChar, Char, 944, 'a', 'f'}, {0, BPChar, Char, 944, 'a', 'f'},
		{0, Char, Int4, 77, 'e', 'f'}, {0, Int4, Char, 78, 'e', 'f'},
		{0, Text, Regclass, 1079, 'i', 'f'}, {0, VarChar, Regclass, 1079, 'i', 'f'},
		{0, Int2, Bytea, 6367, 'e', 'f'}, {0, Int4, Bytea, 6368, 'e', 'f'}, {0, Int8, Bytea, 6369, 'e', 'f'},
		{0, Bytea, Int2, 6370, 'e', 'f'}, {0, Bytea, Int4, 6371, 'e', 'f'}, {0, Bytea, Int8, 6372, 'e', 'f'},
		{0, Interval, Time, 1419, 'a', 'f'}, {0, Int4, Bit, 1683, 'e', 'f'}, {0, Int8, Bit, 2075, 'e', 'f'},
		{0, Bit, Int4, 1684, 'e', 'f'}, {0, Bit, Int8, 2076, 'e', 'f'},
		{0, Bit, Bit, 1685, 'i', 'f'}, {0, VarBit, VarBit, 1687, 'i', 'f'},
		{0, Macaddr, Macaddr8, 4123, 'i', 'f'}, {0, Macaddr8, Macaddr, 4124, 'i', 'f'}, {0, Inet, Cidr, 1715, 'a', 'f'},
		{0, JSONB, Bool, 3556, 'e', 'f'}, {0, JSONB, Numeric, 3449, 'e', 'f'},
		{0, JSONB, Int2, 3450, 'e', 'f'}, {0, JSONB, Int4, 3451, 'e', 'f'}, {0, JSONB, Int8, 3452, 'e', 'f'},
		{0, JSONB, Float4, 3453, 'e', 'f'}, {0, JSONB, Float8, 2580, 'e', 'f'},
		{0, Point, Box, 4091, 'a', 'f'}, {0, Lseg, Point, 1532, 'e', 'f'}, {0, Path, Polygon, 1449, 'a', 'f'},
		{0, Box, Point, 1534, 'e', 'f'}, {0, Box, Lseg, 1541, 'e', 'f'}, {0, Box, Polygon, 1448, 'a', 'f'}, {0, Box, Circle, 1479, 'e', 'f'},
		{0, Polygon, Point, 1540, 'e', 'f'}, {0, Polygon, Path, 1447, 'a', 'f'}, {0, Polygon, Box, 1446, 'e', 'f'}, {0, Polygon, Circle, 1474, 'e', 'f'},
		{0, Circle, Point, 1416, 'e', 'f'}, {0, Circle, Box, 1480, 'e', 'f'}, {0, Circle, Polygon, 1544, 'e', 'f'},
		{0, Int4Range, Int4Multirange, 4281, 'e', 'f'}, {0, NumRange, NumMultirange, 4284, 'e', 'f'}, {0, Int8Range, Int8Multirange, 4296, 'e', 'f'},
		{0, XML, Text, 0, 'a', 'b'}, {0, XML, VarChar, 0, 'a', 'b'}, {0, XML, BPChar, 0, 'a', 'b'},
		{0, Text, XML, 2896, 'e', 'f'}, {0, VarChar, XML, 2896, 'e', 'f'}, {0, BPChar, XML, 2896, 'e', 'f'},
	}
	for _, pair := range [][2]ID{{Regproc, Regprocedure}, {Regprocedure, Regproc}, {Regoper, Regoperator}, {Regoperator, Regoper}, {Cidr, Inet}, {Bit, VarBit}, {VarBit, Bit}} {
		rows = append(rows, CastMetadata{0, pair[0], pair[1], 0, 'i', 'b'})
	}
	for _, alias := range []ID{Oid, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary} {
		rows = append(rows, CastMetadata{0, Int2, alias, 313, 'i', 'f'}, CastMetadata{0, Int4, alias, 0, 'i', 'b'}, CastMetadata{0, Int8, alias, 1287, 'i', 'f'}, CastMetadata{0, alias, Int4, 0, 'a', 'b'}, CastMetadata{0, alias, Int8, 1288, 'a', 'f'})
		if alias != Oid {
			rows = append(rows, CastMetadata{0, Oid, alias, 0, 'i', 'b'}, CastMetadata{0, alias, Oid, 0, 'i', 'b'})
		}
	}
	for _, source := range []ID{Bool, Inet, Cidr} {
		function := uint32(730)
		if source == Bool {
			function = 2971
		}
		for _, target := range []ID{Text, VarChar, BPChar} {
			rows = append(rows, CastMetadata{0, source, target, function, 'a', 'f'})
		}
	}
	return rows
}
