package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressArrays(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE TABLE addr_array(a INT)", 0))
		for _, tc := range []struct{ name, names, args string }{
			{"flat", "'{public,addr_array}'::text[]", "'{}'::text[]"},
			{"lower_bound", "'[0:1]={public,addr_array}'::text[]", "'{}'::text[]"},
			{"negative_bound", "'[-3:-2]={public,addr_array}'::text[]", "'[9:9]={ignored}'::text[]"},
			{"two_dimensions", "'{{public,addr_array}}'::text[]", "'{{ignored,extra}}'::text[]"},
			{"ignored_args", "'{public,addr_array}'::text[]", "'{not_a_type,another}'::text[]"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT classid::bigint AS class,objid='addr_array'::regclass::oid AS same,objsubid FROM pg_get_object_address('table'," + tc.names + "," + tc.args + ")"
				objectAddressRun(t, p, b, c, fixture.Select(q, []string{"class", "same", "objsubid"}, []string{"INT8", "BOOL", "INT4"}, [][]any{{int64(1259), true, int64(0)}}))
			})
		}
		var cases []objectAddressDiagnosticCase
		add := func(name, kind, names, args, message string) {
			cases = append(cases, objectAddressDiagnosticCase{name, "SELECT pg_get_object_address('" + kind + "'," + names + "," + args + ")", fixture.Diagnostic{Code: "22023", Message: message}, true})
		}
		for _, kind := range []string{"routine", "stone", "TABLE", " table", "table "} {
			add("unknown_"+kind, kind, "'{}'::text[]", "'{}'::text[]", `unrecognized object type "`+kind+`"`)
		}
		for _, kind := range []string{"role membership", "view column", "index column", "composite type", "materialized view column", "sequence column"} {
			add("unsupported_"+kind, kind, "'{public,addr_array,a}'::text[]", "'{}'::text[]", `unsupported object type "`+kind+`"`)
		}
		for _, kind := range []string{"table", "schema", "role", "database", "tablespace", "extension", "function", "operator", "operator class", "operator family"} {
			add(kind+"_empty", kind, "'{}'::text[]", "'{}'::text[]", "name list length must be at least 1")
			add(kind+"_null_name", kind, "ARRAY[NULL]::text[]", "'{}'::text[]", "name or argument lists may not contain nulls")
			add(kind+"_null_arg", kind, "'{public,addr_array}'::text[]", "ARRAY[NULL]::text[]", "name or argument lists may not contain nulls")
		}
		for _, kind := range []string{"type", "domain", "cast", "domain constraint"} {
			for _, names := range []string{"'{}'::text[]", "'{integer,text}'::text[]", "ARRAY[NULL,NULL]::text[]"} {
				message := "name list length must be exactly 1"
				if kind == "domain" {
					message = `unrecognized object type "domain"`
				}
				add(kind+"_name_length_"+names, kind, names, "'{}'::text[]", message)
			}
			message := "name or argument lists may not contain nulls"
			if kind == "domain" {
				message = `unrecognized object type "domain"`
			}
			add(kind+"_one_null", kind, "ARRAY[NULL]::text[]", "'{}'::text[]", message)
		}
		for _, kind := range []string{"schema", "role", "database", "tablespace", "extension"} {
			add(kind+"_two_names", kind, "'{a,b}'::text[]", "'{}'::text[]", "name list length must be exactly 1")
		}
		for _, kind := range []string{"operator class", "operator family"} {
			add(kind+"_short", kind, "'{btree}'::text[]", "'{}'::text[]", "name list length must be at least 2")
		}
		for _, kind := range []string{"cast", "domain constraint"} {
			for _, args := range []string{"'{}'::text[]", "'{integer,text}'::text[]"} {
				add(kind+"_args_"+args, kind, "'{integer}'::text[]", args, "argument list length must be exactly 1")
			}
		}
		for _, args := range []string{"'{}'::text[]", "'{integer}'::text[]", "'{integer,integer,integer}'::text[]"} {
			add("operator_"+args, "operator", "'{pg_catalog,+}'::text[]", args, "argument list length must be exactly 2")
		}
		// Released subordinate qualification errors are not uniform 22023
		// array-length errors.
		for _, kind := range []string{"table column", "default value", "table constraint", "rule", "policy"} {
			message := "must specify relation and object name"
			if kind == "table column" || kind == "default value" {
				message = "column name must be qualified"
			}
			q := "SELECT pg_get_object_address('" + kind + "','{unqualified}','{}')"
			cases = append(cases, objectAddressDiagnosticCase{kind + "_unqualified", q, fixture.Diagnostic{Code: "42601", Message: message}, true})
		}
		objectAddressDiagnostics(t, p, b, c, cases)
		var publicID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&publicID); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			kind, names, args string
			class, id         int64
		}{
			{"schema", "'[0:0]={public}'", "'{ignored}'", 2615, publicID},
			{"type", "'{{integer}}'", "'{ignored}'", 1247, 23},
			{"function", "'{{pg_catalog,pg_identify_object}}'", "'[0:2]={oid,oid,integer}'", 1255, 3839},
		} {
			q := fmt.Sprintf("SELECT classid::bigint AS class,objid::bigint AS id,objsubid FROM pg_get_object_address('%s',%s::text[],%s::text[])", tc.kind, tc.names, tc.args)
			objectAddressRun(t, p, b, c, fixture.Select(q, []string{"class", "id", "objsubid"}, []string{"INT8", "INT8", "INT4"}, [][]any{{tc.class, tc.id, int64(0)}}))
		}
	})
}

func TestReferenceObjectAddressMissingNames(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE TABLE addr_missing(a INT)", 0))
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE VIEW addr_missing_view AS SELECT a FROM addr_missing", 0))
		var cases []objectAddressDiagnosticCase
		for _, tc := range []struct {
			name, kind    string
			names, args   []string
			code, message string
		}{
			{"schema", "schema", []string{"addr_absent_schema"}, nil, "3F000", `schema "addr_absent_schema" does not exist`},
			{"table", "table", []string{"public", "addr_absent_table"}, nil, "42P01", `relation "public.addr_absent_table" does not exist`},
			{"qualified_missing_schema", "table", []string{"addr_absent_schema", "t"}, nil, "3F000", `schema "addr_absent_schema" does not exist`},
			{"column", "table column", []string{"public", "addr_missing", "absent"}, nil, "42703", `column "absent" of relation "public.addr_missing" does not exist`},
			{"type", "type", []string{"public.addr_absent_type"}, nil, "42704", `type "public.addr_absent_type" does not exist`},
			{"routine", "function", []string{"public", "addr_absent_function"}, []string{"integer"}, "42883", `function public.addr_absent_function(integer) does not exist`},
			{"wrong_relation_kind", "view", []string{"public", "addr_missing"}, nil, "42809", `"addr_missing" is not a view`},
			{"wrong_routine_kind", "aggregate", []string{"pg_catalog", "pg_identify_object"}, []string{"oid", "oid", "integer"}, "42809", `function pg_catalog.pg_identify_object(oid, oid, integer) is not an aggregate`},
			{"role", "role", []string{"addr_absent_role"}, nil, "42704", `role "addr_absent_role" does not exist`},
			{"database", "database", []string{"addr_absent_database"}, nil, "3D000", `database "addr_absent_database" does not exist`},
			{"tablespace", "tablespace", []string{"addr_absent_tablespace"}, nil, "42704", `tablespace "addr_absent_tablespace" does not exist`},
			{"extension", "extension", []string{"addr_absent_extension"}, nil, "42704", `extension "addr_absent_extension" does not exist`},
			{"default", "default value", []string{"public", "addr_missing", "a"}, nil, "42703", `default value for column "a" of relation "public.addr_missing" does not exist`},
			{"constraint", "table constraint", []string{"public", "addr_missing", "absent"}, nil, "42704", `constraint "absent" for table "addr_missing" does not exist`},
			{"rule", "rule", []string{"public", "addr_missing", "absent"}, nil, "42704", `rule "absent" for relation "addr_missing" does not exist`},
			{"policy", "policy", []string{"public", "addr_missing", "absent"}, nil, "42704", `policy "absent" for table "addr_missing" does not exist`},
		} {
			q := "SELECT pg_get_object_address(" + objectAddressLiteral(tc.kind) + "," + objectAddressArray(tc.names) + "," + objectAddressArray(tc.args) + ")"
			cases = append(cases, objectAddressDiagnosticCase{tc.name, q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, true})
		}
		objectAddressDiagnostics(t, p, b, c, cases)
		objectAddressRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM addr_missing", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
