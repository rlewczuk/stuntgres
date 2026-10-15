package reference_test

import "stuntgres/reference/fixture"

// PostgreSQL 18.0 pinned observations from the first oracle round. These fixed
// expectations are independent of native execution, including backend metadata
// differences; they are not a native/PostgreSQL parity oracle.
var arrayTransformContracts = map[string]arrayTransformContract{
	"SELECT array_append(ARRAY[1::at_leaf],2::at_leaf) AS a,pg_typeof(array_append(ARRAY[1::at_leaf],2::at_leaf))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_append(ARRAY[1::at_leaf],NULL) AS a,pg_typeof(array_append(ARRAY[1::at_leaf],NULL))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,NULL}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_append(ARRAY[1::at_leaf],2::int) AS a,pg_typeof(array_append(ARRAY[1::at_leaf],2::int))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_INT4", "TEXT"}, pgTypes: []string{"_INT4", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "integer[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_cat(ARRAY[1::at_leaf],ARRAY[2::at_leaf]) AS a,pg_typeof(array_cat(ARRAY[1::at_leaf],ARRAY[2::at_leaf]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_prepend(1::at_leaf,ARRAY[2::at_leaf]) AS a,pg_typeof(array_prepend(1::at_leaf,ARRAY[2::at_leaf]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT ARRAY[1::at_leaf] || 2::at_leaf AS a,pg_typeof(ARRAY[1::at_leaf] || 2::at_leaf)::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_cat(ARRAY[1::at_leaf],ARRAY[2::int]) AS a,pg_typeof(array_cat(ARRAY[1::at_leaf],ARRAY[2::int]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_INT4", "TEXT"}, pgTypes: []string{"_INT4", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "integer[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT trim_array(ARRAY[1::at_leaf,2::at_leaf],1) AS a,pg_typeof(trim_array(ARRAY[1::at_leaf,2::at_leaf],1))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_sort('{2,1}'::at_outer) AS a,pg_typeof(array_sort('{2,1}'::at_outer))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_INT4", "TEXT"}, pgTypes: []string{"_INT4", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "integer[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_reverse(NULL::at_leaf[]) AS a,pg_typeof(array_reverse(NULL::at_leaf[]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{nil, "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_reverse('{1,2}'::at_outer) AS a,pg_typeof(array_reverse('{1,2}'::at_outer))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_INT4", "TEXT"}, pgTypes: []string{"_INT4", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{2,1}", "integer[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_cat('{1}'::at_outer,ARRAY[2]) AS a,pg_typeof(array_cat('{1}'::at_outer,ARRAY[2]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_INT4", "TEXT"}, pgTypes: []string{"_INT4", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,2}", "integer[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_fill(1::at_leaf,ARRAY[2]) AS a,pg_typeof(array_fill(1::at_leaf,ARRAY[2]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1,1}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_fill('{1,2}'::at_vector,ARRAY[2]) AS a,pg_typeof(array_fill('{1,2}'::at_vector,ARRAY[2]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_VECTOR", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{\"{1,2}\",\"{1,2}\"}", "at_vector[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_append(ARRAY['{1}'::at_vector],'{2}'::at_vector) AS a,pg_typeof(array_append(ARRAY['{1}'::at_vector],'{2}'::at_vector))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_VECTOR", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{\"{1}\",\"{2}\"}", "at_vector[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_fill('low'::at_mood,ARRAY[2]) AS a,pg_typeof(array_fill('low'::at_mood,ARRAY[2]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_MOOD", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{low,low}", "at_mood[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_reverse(ARRAY[ROW(1,'a')::at_pair,ROW(2,'b')::at_pair]) AS a,pg_typeof(array_reverse(ARRAY[ROW(1,'a')::at_pair,ROW(2,'b')::at_pair]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_PAIR", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{\"(2,b)\",\"(1,a)\"}", "at_pair[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_sort(ARRAY[1::at_leaf]) AS a,pg_typeof(array_sort(ARRAY[1::at_leaf]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_AT_LEAF", "TEXT"}, pgTypes: []string{"", "TEXT"}, raw: []string{"_INT4", "TEXT"}, rows: [][]any{{"{1}", "at_leaf[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_sort(ARRAY[1::at_leaf]) AS a FROM at_one WHERE FALSE": {
		names: []string{"a"}, nativeTypes: []string{"_AT_LEAF"}, pgTypes: []string{""}, raw: []string{"_INT4"}, rows: nil,
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_cat(ARRAY['ab'::varchar(2)],ARRAY['cde'::varchar(3)]) AS a,pg_typeof(array_cat(ARRAY['ab'::varchar(2)],ARRAY['cde'::varchar(3)]))::text AS type_name FROM at_one": {
		names: []string{"a", "type_name"}, nativeTypes: []string{"_VARCHAR", "TEXT"}, pgTypes: []string{"_VARCHAR", "TEXT"}, raw: []string{"_VARCHAR", "TEXT"}, rows: [][]any{{"{ab,cde}", "character varying[]"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}, {scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_to_string(attmissingval,',') AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{nil}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_reverse(attmissingval) AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		names: []string{"a"}, nativeTypes: []string{"ANYARRAY"}, pgTypes: []string{"ANYARRAY"}, raw: []string{"_INT4"}, rows: [][]any{{nil}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_sort(attmissingval) AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		names: []string{"a"}, nativeTypes: []string{"ANYARRAY"}, pgTypes: []string{"ANYARRAY"}, raw: []string{"_INT4"}, rows: [][]any{{nil}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT trim_array(attmissingval,0) AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		names: []string{"a"}, nativeTypes: []string{"ANYARRAY"}, pgTypes: []string{"ANYARRAY"}, raw: []string{"_INT4"}, rows: [][]any{{nil}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_append(attmissingval,NULL) AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_append(anyarray, unknown) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_cat(attmissingval,attmissingval) AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_cat(anyarray, anyarray) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_prepend(NULL,attmissingval) AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_prepend(unknown, anyarray) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_fill(attmissingval,ARRAY[1]) AS a FROM pg_catalog.pg_attribute WHERE attrelid='at_one'::regclass::oid AND attname='id'": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "could not find array type for data type anyarray"}, analysis: true,
	},
	"SELECT 'a'::text || 'b' AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"ab"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT '\\x01'::bytea || '\\x02' AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"BYTEA"}, pgTypes: []string{"BYTEA"}, raw: []string{"BYTEA"}, rows: [][]any{{"\u0001\u0002"}},
		pgMetadata: []arrayTransformColumn{{scan: "[]uint8", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT B'10' || '01' AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"VARBIT"}, pgTypes: []string{"VARBIT"}, raw: []string{"VARBIT"}, rows: [][]any{{"1001"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: -1, lengthKnown: true}},
	},
	"SELECT array_fill(1,ARRAY[-1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "array size exceeds the maximum allowed (134217727)"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[0,-1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "array size exceeds the maximum allowed (134217727)"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[-1,0]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "array size exceeds the maximum allowed (134217727)"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[2147483647,2]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "array size exceeds the maximum allowed (134217727)"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[2],ARRAY[2147483647]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "array lower bound is too large: 2147483647"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[0],ARRAY[-2147483648]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_fill(1,ARRAY[0],ARRAY[2147483647]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_fill(1,ARRAY[0,NULL]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "dimension values cannot be null"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[0],ARRAY[NULL]::int[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "dimension values cannot be null"}, analysis: false,
	},
	"SELECT array_fill(1,NULL::int[],NULL::int[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "dimension array or low bound array cannot be null"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[1,NULL],ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "dimension values cannot be null"}, analysis: false,
	},
	"SELECT array_fill(1,ARRAY[1,1,1,1,1,1,1],NULL::int[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "dimension array or low bound array cannot be null"}, analysis: false,
	},
	"SELECT array_sort(ARRAY[2::at_sort_leaf,1::at_sort_leaf]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_AT_SORT_LEAF"}, pgTypes: []string{""}, raw: []string{"_INT4"}, rows: [][]any{{"{1,2}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_sort(ARRAY['first'::at_sort_enum,'second'::at_sort_enum]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_AT_SORT_ENUM"}, pgTypes: []string{""}, raw: []string{"_INT4"}, rows: [][]any{{"{second,first}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_sort(ARRAY[ROW(2,'a')::at_sort_pair,ROW(1,'b')::at_sort_pair]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_AT_SORT_PAIR"}, pgTypes: []string{""}, raw: []string{"_INT4"}, rows: [][]any{{"{\"(1,b)\",\"(2,a)\"}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_sort(ARRAY[NULL::json,NULL::json]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "could not identify a comparison function for type json"}, analysis: false,
	},
	"SELECT array_sort('{{NULL},{NULL}}'::json[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify a comparison function for type json"}, analysis: false,
	},
	"SELECT array_sort('{{1},{2}}'::json[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify a comparison function for type json"}, analysis: false,
	},
	"SELECT array_sort('{{NULL,1},{NULL,2}}'::json[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify a comparison function for type json"}, analysis: false,
	},
	"SELECT array_sort(ARRAY['a'] COLLATE \"C\",false) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_TEXT"}, pgTypes: []string{"_TEXT"}, raw: []string{"_TEXT"}, rows: [][]any{{"{a}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_to_string(ARRAY['{1,2}'::at_output_vector,'{3}'::at_output_vector],'|') AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"{1,2}|{3}"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET bytea_output='hex'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"\\x0001ff"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET bytea_output='escape'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"\\000\\001\\377"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET DateStyle='ISO, MDY'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"2020-02-03"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET DateStyle='SQL, DMY'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"03/02/2020"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET TimeZone='UTC'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"03/02/2020 04:05:06 UTC"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET TimeZone='America/New_York'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"02/02/2020 23:05:06 EST"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET IntervalStyle='postgres'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"1 day 02:03:04"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SET IntervalStyle='iso_8601'": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"P1DT2H3M4S"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"SELECT array_append($1::int[],$2::int) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22000", Message: "argument must be empty or one-dimensional array"}, analysis: false,
	},
	"SELECT array_fill($1::int,$2::int[],$3::int[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "dimension values cannot be null"}, analysis: false,
	},
	"SELECT trim_array($1::int[],$2::int) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2202E", Message: "number of elements to trim must be between 0 and 2"}, analysis: false,
	},
	"SELECT array_reverse($1::int[]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type integer: \"oops\"", Context: "unnamed portal parameter $1 = '...'"}, analysis: false,
	},
	"SELECT ARRAY[1,2] || $1 AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "malformed array literal: \"7\"", Detail: "Array value must start with \"{\" or dimension information.", Context: "unnamed portal parameter $1 = '...'"}, analysis: false,
	},
	"SELECT array_append() AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_append() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_append(ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_append(integer[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_append(ARRAY[true],1::numeric) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_append(boolean[], numeric) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_cat(ARRAY[true],ARRAY[1::numeric]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_cat(boolean[], numeric[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_prepend(true,ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_prepend(boolean, integer[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_fill('x',ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, analysis: true,
	},
	"SELECT array_fill(NULL,ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, analysis: true,
	},
	"SELECT array_fill(ARRAY[1],ARRAY[2]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "could not find array type for data type integer[]"}, analysis: true,
	},
	"SELECT array_reverse(NULL) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, analysis: true,
	},
	"SELECT array_sort(NULL) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, analysis: true,
	},
	"SELECT trim_array(NULL,1) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, analysis: true,
	},
	"SELECT array_to_string(NULL,',') AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, analysis: true,
	},
	"SELECT array_fill(1,ARRAY[1::bigint]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_fill(integer, bigint[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_reverse(1) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_reverse(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(ARRAY[1],1) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_sort(integer[], integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT trim_array(ARRAY[1],1::bigint) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function trim_array(integer[], bigint) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_to_string(ARRAY[1],1) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_to_string(integer[], integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(array => ARRAY[2,1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"=>\"", Position: "25"}, analysis: true,
	},
	"SELECT array_sort(descending => true) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_sort(descending => boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(array => ARRAY[2,1],unknown => false) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"=>\"", Position: "25"}, analysis: true,
	},
	"SELECT array_sort(array => ARRAY[2,1],descending => false,descending => true) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"=>\"", Position: "25"}, analysis: true,
	},
	"SELECT array_sort(descending => false,ARRAY[2,1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "positional argument cannot follow named argument", Position: "39"}, analysis: true,
	},
	"SELECT array_append(array => ARRAY[1],element => 2) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"=>\"", Position: "27"}, analysis: true,
	},
	"SELECT array_sort(VARIADIC ARRAY[1,2]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{1,2}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_append(VARIADIC ARRAY[1,2]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_append(integer[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(DISTINCT ARRAY[1,2]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "DISTINCT specified, but array_sort is not an aggregate function", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(ALL ARRAY[1,2]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{1,2}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_sort(*) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_sort() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(ARRAY[1,2] ORDER BY id) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "ORDER BY specified, but array_sort is not an aggregate function", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(ARRAY[1,2]) FILTER (WHERE true) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "FILTER specified, but array_sort is not an aggregate function", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(ARRAY[1,2]) OVER () AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "OVER specified, but array_sort is not a window function nor an aggregate function", Position: "8"}, analysis: true,
	},
	"SELECT array_sort(ARRAY[1,2]) WITHIN GROUP (ORDER BY id) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_sort(integer[], integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT public.array_sort(ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function public.array_sort(integer[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT \"Array_Sort\"(ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function Array_Sort(integer[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, analysis: true,
	},
	"SELECT pg_catalog.array_sort(ARRAY[1]) AS a FROM at_one": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{1}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"SELECT array_reverse(missing) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" does not exist", Position: "22"}, analysis: true,
	},
	"SELECT array_sort(missing,unknown => false) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" does not exist", Position: "19"}, analysis: true,
	},
	"SELECT array_sort(ARRAY[1]) FILTER (WHERE missing) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" does not exist", Position: "43"}, analysis: true,
	},
	"SELECT ARRAY[1,2] || '7' AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "malformed array literal: \"7\"", Detail: "Array value must start with \"{\" or dimension information.", Position: "22"}, analysis: true,
	},
	"SELECT ARRAY[true] || 1::numeric AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "operator does not exist: boolean[] || numeric", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "20"}, analysis: true,
	},
	"SELECT array_cat(ARRAY['a'] COLLATE \"C\",ARRAY['b'] COLLATE \"POSIX\") AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: "collation mismatch between explicit collations \"C\" and \"POSIX\"", Position: "52"}, analysis: true,
	},
	"/*é*/ SELECT array_reverse(1) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function array_reverse(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "14"}, analysis: true,
	},
	"/*é*/ SELECT ARRAY[1] || '7' AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "malformed array literal: \"7\"", Detail: "Array value must start with \"{\" or dimension information.", Position: "26"}, analysis: true,
	},
	"SELECT array_sort(ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1],ARRAY[1]) AS a FROM at_one": {
		diagnostic: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}, analysis: true,
	},
	"append_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"[0:2]={1,2,3}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"prepend_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"[0:2]={3,1,2}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"cat_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{1,2}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"fill_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{7,7}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"reverse_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{2,1}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"sort_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{2,1,NULL}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
	"string_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"TEXT"}, pgTypes: []string{"TEXT"}, raw: []string{"TEXT"}, rows: [][]any{{"1,*,2"}},
		pgMetadata: []arrayTransformColumn{{scan: "string", length: 9223372036854775807, lengthKnown: true}},
	},
	"operator_all_accessors": {
		names: []string{"a"}, nativeTypes: []string{"_INT4"}, pgTypes: []string{"_INT4"}, raw: []string{"_INT4"}, rows: [][]any{{"{1,2}"}},
		pgMetadata: []arrayTransformColumn{{scan: "interface {}", length: 0, lengthKnown: false}},
	},
}
