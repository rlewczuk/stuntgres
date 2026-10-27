package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRoutineInputNameDeclarations(t *testing.T) {
	expected := map[ids.OID][]string{
		1268: {"str", "strict"},
		1689: {"acl", "grantor", "grantee", "privilege_type", "is_grantable"},
		2284: {"string", "pattern", "replacement"},
		2285: {"string", "pattern", "replacement", "flags"},
		2623: {"filename", "size", "access", "modification", "change", "creation", "isdir"},
		2763: {"string", "pattern"},
		2764: {"string", "pattern", "flags"},
		2765: {"string", "pattern"},
		2766: {"string", "pattern", "flags"},
		2767: {"string", "pattern"},
		2768: {"string", "pattern", "flags"},
		3084: {"name", "source", "target", "path"},
		3197: {"key", "value"},
		3208: {"from_json", "key", "value"},
		3214: {"from_json", "field_name"},
		3215: {"from_json", "element_index"},
		3216: {"from_json", "element_index"},
		3217: {"from_json", "path_elems"},
		3219: {"from_json", "value"},
		3261: {"target", "strip_in_arrays"},
		3270: {"key", "value"},
		3305: {"jsonb_in", "path", "replacement", "create_if_missing"},
		3307: {"filename", "missing_ok", "size", "access", "modification", "change", "creation", "isdir"},
		3353: {"name", "size", "modification"},
		3354: {"name", "size", "modification"},
		3382: {"classid", "objid", "objsubid", "type", "object_names", "object_args"},
		3396: {"string", "pattern"},
		3397: {"string", "pattern", "flags"},
		3423: {"rootrelid", "relid", "parentrelid", "isleaf", "level"},
		3425: {"partitionid", "relid"},
		3461: {"year", "month", "mday", "hour", "min", "sec"},
		3462: {"year", "month", "mday", "hour", "min", "sec"},
		3463: {"year", "month", "mday", "hour", "min", "sec", "timezone"},
		3464: {"years", "months", "weeks", "days", "hours", "mins", "secs"},
		3465: {"from_json", "value"},
		3478: {"from_json", "field_name"},
		3538: {"value", "delimiter"},
		3545: {"value", "delimiter"},
		3579: {"jsonb_in", "path", "replacement", "insert_after"},
		3583: {"xid", "timestamp", "roident"},
		3713: {"parser_oid", "tokid", "alias", "description"},
		3714: {"parser_name", "tokid", "alias", "description"},
		3715: {"parser_oid", "txt", "tokid", "token"},
		3716: {"parser_name", "txt", "tokid", "token"},
		3819: {"multixid", "xid", "mode"},
		3839: {"classid", "objid", "objsubid", "type", "schema", "name", "identity"},
		3846: {"year", "month", "day"},
		3847: {"hour", "min", "sec"},
		3932: {"from_json", "key", "value"},
		3940: {"from_json", "path_elems"},
		3947: {"from_json", "field_name"},
		3948: {"from_json", "field_name"},
		3949: {"from_json", "element_index"},
		3950: {"from_json", "element_index"},
		3951: {"from_json", "path_elems"},
		3953: {"from_json", "path_elems"},
		3954: {"type", "object_names", "object_args", "classid", "objid", "objsubid"},
		3955: {"from_json", "value"},
		3958: {"from_json", "key", "value"},
		3959: {"from_json", "key", "value"},
		3960: {"base", "from_json", "use_json_as_text"},
		3961: {"base", "from_json", "use_json_as_text"},
		3969: {"from_json", "value"},
		4005: {"target", "path", "vars", "silent"},
		4006: {"target", "path", "vars", "silent"},
		4007: {"target", "path", "vars", "silent"},
		4008: {"target", "path", "vars", "silent"},
		4009: {"target", "path", "vars", "silent"},
		5029: {"name", "size", "modification"},
		5030: {"tablespace", "name", "size", "modification"},
		5031: {"name", "size", "modification"},
		5054: {"jsonb_in", "path", "replacement", "create_if_missing", "null_value_treatment"},
		6159: {"fktable", "fkcols", "pktable", "pkcols", "is_array", "is_opt"},
		6168: {"xid", "timestamp", "roident"},
		6212: {"mean", "stddev"},
		6251: {"string", "pattern", "replacement", "start", "N", "flags"},
		6252: {"string", "pattern", "replacement", "start", "N"},
		6253: {"string", "pattern", "replacement", "start"},
		6254: {"string", "pattern"},
		6255: {"string", "pattern", "start"},
		6256: {"string", "pattern", "start", "flags"},
		6257: {"string", "pattern"},
		6258: {"string", "pattern", "start"},
		6259: {"string", "pattern", "start", "N"},
		6260: {"string", "pattern", "start", "N", "endoption"},
		6261: {"string", "pattern", "start", "N", "endoption", "flags"},
		6262: {"string", "pattern", "start", "N", "endoption", "flags", "subexpr"},
		6263: {"string", "pattern"},
		6264: {"string", "pattern", "flags"},
		6265: {"string", "pattern"},
		6266: {"string", "pattern", "start"},
		6267: {"string", "pattern", "start", "N"},
		6268: {"string", "pattern", "start", "N", "flags"},
		6269: {"string", "pattern", "start", "N", "flags", "subexpr"},
		6270: {"name", "size", "modification"},
		6271: {"name", "size", "modification"},
		6272: {"slot_name", "name", "size", "modification"},
		6280: {"key", "value"},
		6281: {"key", "value"},
		6282: {"key", "value"},
		6288: {"key", "value"},
		6289: {"key", "value"},
		6290: {"key", "value"},
		6339: {"min", "max"},
		6340: {"min", "max"},
		6341: {"min", "max"},
		6385: {"classid", "objid", "objsubid"},
		6389: {"array", "descending"},
		6390: {"array", "descending", "nulls_first"},
		6400: {"name", "size", "modification"},
		6430: {"shift"},
	}
	seen := 0
	for _, d := range BuiltinProcedures(authorization.PrincipalRef{}) {
		want, named := expected[d.Address().ObjectID]
		if !named {
			if d.InputNames != nil || d.OutputDeclaration() != nil {
				t.Fatal("unexpected named declaration", d.Address())
			}
			continue
		}
		seen++
		if outputs := d.OutputDeclaration(); outputs != nil {
			if d.InputNames != nil || outputs.Inputs()+outputs.Len() != len(want) {
				t.Fatal("duplicate name owner", d.Address())
			}
			for i, name := range want {
				if outputs.Name(i) != name {
					t.Fatal("fixed name", d.Address(), i)
				}
			}
		} else {
			if d.InputNames.Len() != len(want) {
				t.Fatal("input name width", d.Address())
			}
			for i, name := range want {
				if d.InputNames.At(i) != name {
					t.Fatal("input name", d.Address(), i)
				}
			}
		}
	}
	if seen != len(expected) {
		t.Fatal("incomplete named declaration inventory", seen)
	}
}

func TestRoutineInputNameOwnership(t *testing.T) {
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	again := BuiltinProcedures(authorization.PrincipalRef{})
	for i, d := range procedures {
		if d.InputNames != again[i].InputNames {
			t.Fatal("per-engine name allocation", d.Address())
		}
		if d.LiteralDefaults != again[i].LiteralDefaults {
			t.Fatal("per-engine recipe allocation", d.Address())
		}
	}
}
