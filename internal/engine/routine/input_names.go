package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Released pg_proc.dat input names with system_functions.sql replacements.
// Fixed OUT declarations retain their existing complete RoutineOutputs owner.
// All other instantiated declarations are genuinely unnamed.
var builtinRoutineInputNames = map[ids.OID]*catalog.RoutineInputNames{
	3436: catalog.NewRoutineInputNames("wait", "wait_seconds"),
	6363: catalog.NewRoutineInputNames("kwargs"),
	6398: catalog.NewRoutineInputNames("schemaname", "relname", "attname", "inherited"),
	6362: catalog.NewRoutineInputNames("kwargs"),
	6397: catalog.NewRoutineInputNames("schemaname", "relname"),
	1268: catalog.NewRoutineInputNames("str", "strict"),                                             // parse_ident
	2284: catalog.NewRoutineInputNames("string", "pattern", "replacement"),                          // regexp_replace
	2285: catalog.NewRoutineInputNames("string", "pattern", "replacement", "flags"),                 // regexp_replace
	2763: catalog.NewRoutineInputNames("string", "pattern"),                                         // regexp_matches
	2764: catalog.NewRoutineInputNames("string", "pattern", "flags"),                                // regexp_matches
	2765: catalog.NewRoutineInputNames("string", "pattern"),                                         // regexp_split_to_table
	2766: catalog.NewRoutineInputNames("string", "pattern", "flags"),                                // regexp_split_to_table
	2767: catalog.NewRoutineInputNames("string", "pattern"),                                         // regexp_split_to_array
	2768: catalog.NewRoutineInputNames("string", "pattern", "flags"),                                // regexp_split_to_array
	3197: catalog.NewRoutineInputNames("key", "value"),                                              // json_object_agg
	3214: catalog.NewRoutineInputNames("from_json", "field_name"),                                   // jsonb_object_field_text
	3215: catalog.NewRoutineInputNames("from_json", "element_index"),                                // jsonb_array_element
	3216: catalog.NewRoutineInputNames("from_json", "element_index"),                                // jsonb_array_element_text
	3217: catalog.NewRoutineInputNames("from_json", "path_elems"),                                   // jsonb_extract_path
	3261: catalog.NewRoutineInputNames("target", "strip_in_arrays"),                                 // json_strip_nulls
	3270: catalog.NewRoutineInputNames("key", "value"),                                              // jsonb_object_agg
	3305: catalog.NewRoutineInputNames("jsonb_in", "path", "replacement", "create_if_missing"),      // jsonb_set
	3396: catalog.NewRoutineInputNames("string", "pattern"),                                         // regexp_match
	3397: catalog.NewRoutineInputNames("string", "pattern", "flags"),                                // regexp_match
	3461: catalog.NewRoutineInputNames("year", "month", "mday", "hour", "min", "sec"),               // make_timestamp
	3462: catalog.NewRoutineInputNames("year", "month", "mday", "hour", "min", "sec"),               // make_timestamptz
	3463: catalog.NewRoutineInputNames("year", "month", "mday", "hour", "min", "sec", "timezone"),   // make_timestamptz
	3464: catalog.NewRoutineInputNames("years", "months", "weeks", "days", "hours", "mins", "secs"), // make_interval
	3478: catalog.NewRoutineInputNames("from_json", "field_name"),                                   // jsonb_object_field
	3538: catalog.NewRoutineInputNames("value", "delimiter"),                                        // string_agg
	3545: catalog.NewRoutineInputNames("value", "delimiter"),                                        // string_agg
	3579: catalog.NewRoutineInputNames("jsonb_in", "path", "replacement", "insert_after"),           // jsonb_insert
	3846: catalog.NewRoutineInputNames("year", "month", "day"),                                      // make_date
	3847: catalog.NewRoutineInputNames("hour", "min", "sec"),                                        // make_time
	3940: catalog.NewRoutineInputNames("from_json", "path_elems"),                                   // jsonb_extract_path_text
	3947: catalog.NewRoutineInputNames("from_json", "field_name"),                                   // json_object_field
	3948: catalog.NewRoutineInputNames("from_json", "field_name"),                                   // json_object_field_text
	3949: catalog.NewRoutineInputNames("from_json", "element_index"),                                // json_array_element
	3950: catalog.NewRoutineInputNames("from_json", "element_index"),                                // json_array_element_text
	3951: catalog.NewRoutineInputNames("from_json", "path_elems"),                                   // json_extract_path
	3953: catalog.NewRoutineInputNames("from_json", "path_elems"),                                   // json_extract_path_text
	3960: catalog.NewRoutineInputNames("base", "from_json", "use_json_as_text"),                     // json_populate_record
	3961: catalog.NewRoutineInputNames("base", "from_json", "use_json_as_text"),                     // json_populate_recordset
	4005: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),                          // jsonb_path_exists
	4006: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),                          // jsonb_path_query
	4007: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),                          // jsonb_path_query_array
	4008: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),                          // jsonb_path_query_first
	4009: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),                          // jsonb_path_match
	1177: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),
	2030: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),
	1179: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),
	1180: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),
	2023: catalog.NewRoutineInputNames("target", "path", "vars", "silent"),
	5054: catalog.NewRoutineInputNames("jsonb_in", "path", "replacement", "create_if_missing", "null_value_treatment"), // jsonb_set_lax
	6212: catalog.NewRoutineInputNames("mean", "stddev"),                                                               // random_normal
	6251: catalog.NewRoutineInputNames("string", "pattern", "replacement", "start", "N", "flags"),                      // regexp_replace
	6252: catalog.NewRoutineInputNames("string", "pattern", "replacement", "start", "N"),                               // regexp_replace
	6253: catalog.NewRoutineInputNames("string", "pattern", "replacement", "start"),                                    // regexp_replace
	6254: catalog.NewRoutineInputNames("string", "pattern"),                                                            // regexp_count
	6255: catalog.NewRoutineInputNames("string", "pattern", "start"),                                                   // regexp_count
	6256: catalog.NewRoutineInputNames("string", "pattern", "start", "flags"),                                          // regexp_count
	6257: catalog.NewRoutineInputNames("string", "pattern"),                                                            // regexp_instr
	6258: catalog.NewRoutineInputNames("string", "pattern", "start"),                                                   // regexp_instr
	6259: catalog.NewRoutineInputNames("string", "pattern", "start", "N"),                                              // regexp_instr
	6260: catalog.NewRoutineInputNames("string", "pattern", "start", "N", "endoption"),                                 // regexp_instr
	6261: catalog.NewRoutineInputNames("string", "pattern", "start", "N", "endoption", "flags"),                        // regexp_instr
	6262: catalog.NewRoutineInputNames("string", "pattern", "start", "N", "endoption", "flags", "subexpr"),             // regexp_instr
	6263: catalog.NewRoutineInputNames("string", "pattern"),                                                            // regexp_like
	6264: catalog.NewRoutineInputNames("string", "pattern", "flags"),                                                   // regexp_like
	6265: catalog.NewRoutineInputNames("string", "pattern"),                                                            // regexp_substr
	6266: catalog.NewRoutineInputNames("string", "pattern", "start"),                                                   // regexp_substr
	6267: catalog.NewRoutineInputNames("string", "pattern", "start", "N"),                                              // regexp_substr
	6268: catalog.NewRoutineInputNames("string", "pattern", "start", "N", "flags"),                                     // regexp_substr
	6269: catalog.NewRoutineInputNames("string", "pattern", "start", "N", "flags", "subexpr"),                          // regexp_substr
	6280: catalog.NewRoutineInputNames("key", "value"),                                                                 // json_object_agg_strict
	6281: catalog.NewRoutineInputNames("key", "value"),                                                                 // json_object_agg_unique
	6282: catalog.NewRoutineInputNames("key", "value"),                                                                 // json_object_agg_unique_strict
	6288: catalog.NewRoutineInputNames("key", "value"),                                                                 // jsonb_object_agg_strict
	6289: catalog.NewRoutineInputNames("key", "value"),                                                                 // jsonb_object_agg_unique
	6290: catalog.NewRoutineInputNames("key", "value"),                                                                 // jsonb_object_agg_unique_strict
	6339: catalog.NewRoutineInputNames("min", "max"),                                                                   // random
	6340: catalog.NewRoutineInputNames("min", "max"),                                                                   // random
	6341: catalog.NewRoutineInputNames("min", "max"),                                                                   // random
	6385: catalog.NewRoutineInputNames("classid", "objid", "objsubid"),                                                 // pg_get_acl
	6389: catalog.NewRoutineInputNames("array", "descending"),                                                          // array_sort
	6390: catalog.NewRoutineInputNames("array", "descending", "nulls_first"),                                           // array_sort
	6430: catalog.NewRoutineInputNames("shift"),                                                                        // uuidv7
}
