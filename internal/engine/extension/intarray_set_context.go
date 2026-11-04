package extension

// Exact selected declaration contexts from PostgreSQL REL_18_6 intarray--1.2.sql.
// Keep the release spelling/whitespace; these are diagnostics, not execution recipes.
var intarraySetProcedureContexts = [...]string{
	"SQL statement \"CREATE FUNCTION _int_overlap(_int4, _int4)\nRETURNS bool\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 90",
	"SQL statement \"CREATE FUNCTION _int_contains(_int4, _int4)\nRETURNS bool\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 76",
	"SQL statement \"CREATE FUNCTION _int_contained(_int4, _int4)\nRETURNS bool\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 83",
	"SQL statement \"CREATE FUNCTION intarray_push_elem(_int4, int4)\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 287",
	"SQL statement \"CREATE FUNCTION intarray_push_array(_int4, _int4)\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 298",
	"SQL statement \"CREATE FUNCTION intarray_del_elem(_int4, int4)\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 310",
	"SQL statement \"CREATE FUNCTION intset_subtract(_int4, _int4)\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 339",
	"SQL statement \"CREATE FUNCTION intset_union_elem(_int4, int4)\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 321",
	"SQL statement \"CREATE FUNCTION _int_union(_int4, _int4)\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 113",
	"SQL statement \"CREATE FUNCTION _int_inter(_int4, _int4)\nRETURNS _int4\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 118",
	"SQL statement \"CREATE FUNCTION _int_overlap_sel(internal, oid, internal, integer)\nRETURNS float8\nAS '$libdir/_int'\nLANGUAGE C STRICT STABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 123",
	"SQL statement \"CREATE FUNCTION _int_contains_sel(internal, oid, internal, integer)\nRETURNS float8\nAS '$libdir/_int'\nLANGUAGE C STRICT STABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 128",
	"SQL statement \"CREATE FUNCTION _int_contained_sel(internal, oid, internal, integer)\nRETURNS float8\nAS '$libdir/_int'\nLANGUAGE C STRICT STABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 133",
	"SQL statement \"CREATE FUNCTION _int_overlap_joinsel(internal, oid, internal, smallint, internal)\nRETURNS float8\nAS '$libdir/_int'\nLANGUAGE C STRICT STABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 138",
	"SQL statement \"CREATE FUNCTION _int_contains_joinsel(internal, oid, internal, smallint, internal)\nRETURNS float8\nAS '$libdir/_int'\nLANGUAGE C STRICT STABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 143",
	"SQL statement \"CREATE FUNCTION _int_contained_joinsel(internal, oid, internal, smallint, internal)\nRETURNS float8\nAS '$libdir/_int'\nLANGUAGE C STRICT STABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 148",
}

var intarraySetOperatorContexts = [...]string{
	"SQL statement \"CREATE OPERATOR && (\n\tLEFTARG = _int4,\n\tRIGHTARG = _int4,\n\tPROCEDURE = _int_overlap,\n\tCOMMUTATOR = '&&',\n\tRESTRICT = _int_overlap_sel,\n\tJOIN = _int_overlap_joinsel\n)\"\nextension script file \"intarray--1.2.sql\", near line 157",
	"SQL statement \"CREATE OPERATOR @> (\n\tLEFTARG = _int4,\n\tRIGHTARG = _int4,\n\tPROCEDURE = _int_contains,\n\tCOMMUTATOR = '<@',\n\tRESTRICT = _int_contains_sel,\n\tJOIN = _int_contains_joinsel\n)\"\nextension script file \"intarray--1.2.sql\", near line 188",
	"SQL statement \"CREATE OPERATOR <@ (\n\tLEFTARG = _int4,\n\tRIGHTARG = _int4,\n\tPROCEDURE = _int_contained,\n\tCOMMUTATOR = '@>',\n\tRESTRICT = _int_contained_sel,\n\tJOIN = _int_contained_joinsel\n)\"\nextension script file \"intarray--1.2.sql\", near line 197",
	"SQL statement \"CREATE OPERATOR + (\n\tLEFTARG = _int4,\n\tRIGHTARG = int4,\n\tPROCEDURE = intarray_push_elem\n)\"\nextension script file \"intarray--1.2.sql\", near line 292",
	"SQL statement \"CREATE OPERATOR + (\n\tLEFTARG = _int4,\n\tRIGHTARG = _int4,\n\tCOMMUTATOR = +,\n\tPROCEDURE = intarray_push_array\n)\"\nextension script file \"intarray--1.2.sql\", near line 303",
	"SQL statement \"CREATE OPERATOR - (\n\tLEFTARG = _int4,\n\tRIGHTARG = int4,\n\tPROCEDURE = intarray_del_elem\n)\"\nextension script file \"intarray--1.2.sql\", near line 315",
	"SQL statement \"CREATE OPERATOR - (\n\tLEFTARG = _int4,\n\tRIGHTARG = _int4,\n\tPROCEDURE = intset_subtract\n)\"\nextension script file \"intarray--1.2.sql\", near line 344",
	"SQL statement \"CREATE OPERATOR | (\n\tLEFTARG = _int4,\n\tRIGHTARG = int4,\n\tPROCEDURE = intset_union_elem\n)\"\nextension script file \"intarray--1.2.sql\", near line 326",
	"SQL statement \"CREATE OPERATOR | (\n\tLEFTARG = _int4,\n\tRIGHTARG = _int4,\n\tCOMMUTATOR = |,\n\tPROCEDURE = _int_union\n)\"\nextension script file \"intarray--1.2.sql\", near line 332",
	"SQL statement \"CREATE OPERATOR & (\n\tLEFTARG = _int4,\n\tRIGHTARG = _int4,\n\tCOMMUTATOR = &,\n\tPROCEDURE = _int_inter\n)\"\nextension script file \"intarray--1.2.sql\", near line 350",
}
