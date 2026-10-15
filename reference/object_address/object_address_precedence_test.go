package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressStrictConstantNull(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE addr_constant_effect", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE addr_constant_input(a OID)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO addr_constant_input VALUES(NULL)", 1))
		for _, tc := range []struct {
			name, expr, typ string
			advances        bool
		}{
			{"describe_first", "pg_describe_object(NULL,nextval('addr_constant_effect')::oid,0)", "TEXT", false},
			{"describe_second", "pg_describe_object(nextval('addr_constant_effect')::oid,NULL,0)", "TEXT", false},
			{"describe_third", "pg_describe_object(1247,nextval('addr_constant_effect')::oid,NULL)", "TEXT", false},
			{"identify", "pg_identify_object(NULL,nextval('addr_constant_effect')::oid,0)", "RECORD", true},
			{"as_address", "pg_identify_object_as_address(NULL,nextval('addr_constant_effect')::oid,0)", "RECORD", true},
			{"inverse", "pg_get_object_address(NULL,ARRAY[nextval('addr_constant_effect')::text],ARRAY[]::text[])", "RECORD", true},
			{"describe_nonconstant", "pg_describe_object(a,nextval('addr_constant_effect')::oid,0)", "TEXT", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setval('addr_constant_effect',40,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
				q := "SELECT " + tc.expr + " AS v FROM addr_constant_input"
				objectAddressRun(t, p, b, c, fixture.Select(q, []string{"v"}, []string{tc.typ}, [][]any{{nil}}))
				want := int64(40)
				if tc.advances {
					want = 42
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT last_value FROM addr_constant_effect", []string{"last_value"}, []string{"INT8"}, [][]any{{want}}))
			})
		}
	})
}

func TestReferenceObjectAddressStrictConstantNullFrom(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE addr_from_constant", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE addr_from_input(a OID)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO addr_from_input VALUES(NULL)", 1))
		for _, tc := range []struct {
			name, source string
			names, types []string
			advances     bool
		}{
			{"describe_first", "pg_describe_object(NULL,nextval('addr_from_constant')::oid,0) AS f(v)", []string{"v"}, []string{"TEXT"}, false},
			{"describe_second", "pg_describe_object(nextval('addr_from_constant')::oid,NULL,0) AS f(v)", []string{"v"}, []string{"TEXT"}, false},
			{"describe_third", "pg_describe_object(1247,nextval('addr_from_constant')::oid,NULL) AS f(v)", []string{"v"}, []string{"TEXT"}, false},
			{"describe_ordinality", "pg_describe_object(NULL,nextval('addr_from_constant')::oid,0) WITH ORDINALITY AS f(v,n)", []string{"v", "n"}, []string{"TEXT", "INT8"}, false},
			{"describe_rows_from", "ROWS FROM (pg_describe_object(NULL,nextval('addr_from_constant')::oid,0),pg_describe_object(1247,nextval('addr_from_constant')::oid,NULL)) WITH ORDINALITY AS f(v,w,n)", []string{"v", "w", "n"}, []string{"TEXT", "TEXT", "INT8"}, false},
			{"identify", "pg_identify_object(NULL,nextval('addr_from_constant')::oid,0) AS f(kind,namespace,name,identity)", []string{"kind", "namespace", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, true},
			{"identify_second", "pg_identify_object(nextval('addr_from_constant')::oid,NULL,0) AS f(kind,namespace,name,identity)", []string{"kind", "namespace", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, true},
			{"identify_third", "pg_identify_object(1247,nextval('addr_from_constant')::oid,NULL) AS f(kind,namespace,name,identity)", []string{"kind", "namespace", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, true},
			{"as_address", "pg_identify_object_as_address(NULL,nextval('addr_from_constant')::oid,0) AS f(kind,names,args)", []string{"kind", "names", "args"}, []string{"TEXT", "_TEXT", "_TEXT"}, true},
			{"as_address_second", "pg_identify_object_as_address(nextval('addr_from_constant')::oid,NULL,0) AS f(kind,names,args)", []string{"kind", "names", "args"}, []string{"TEXT", "_TEXT", "_TEXT"}, true},
			{"as_address_third", "pg_identify_object_as_address(1247,nextval('addr_from_constant')::oid,NULL) AS f(kind,names,args)", []string{"kind", "names", "args"}, []string{"TEXT", "_TEXT", "_TEXT"}, true},
			{"inverse", "pg_get_object_address(NULL,ARRAY[nextval('addr_from_constant')::text],ARRAY[]::text[]) AS f(class,object,sub)", []string{"class", "object", "sub"}, []string{"OID", "OID", "INT4"}, true},
			{"inverse_second", "pg_get_object_address(nextval('addr_from_constant')::text,NULL,ARRAY[]::text[]) AS f(class,object,sub)", []string{"class", "object", "sub"}, []string{"OID", "OID", "INT4"}, true},
			{"inverse_third", "pg_get_object_address(nextval('addr_from_constant')::text,ARRAY['integer'],NULL) AS f(class,object,sub)", []string{"class", "object", "sub"}, []string{"OID", "OID", "INT4"}, true},
			{"describe_nonconstant", "addr_from_input i, LATERAL pg_describe_object(i.a,nextval('addr_from_constant')::oid,0) AS f(v)", []string{"v"}, []string{"TEXT"}, true},
			{"identify_nonconstant", "addr_from_input i, LATERAL pg_identify_object(i.a,nextval('addr_from_constant')::oid,0) AS f(kind,namespace,name,identity)", []string{"kind", "namespace", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, true},
			{"as_address_nonconstant", "addr_from_input i, LATERAL pg_identify_object_as_address(i.a,nextval('addr_from_constant')::oid,0) AS f(kind,names,args)", []string{"kind", "names", "args"}, []string{"TEXT", "_TEXT", "_TEXT"}, true},
			{"inverse_nonconstant", "addr_from_input i, LATERAL pg_get_object_address(i.a::text,ARRAY[nextval('addr_from_constant')::text],ARRAY[]::text[]) AS f(class,object,sub)", []string{"class", "object", "sub"}, []string{"OID", "OID", "INT4"}, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT f.* FROM " + tc.source
				for _, route := range []string{"direct", "fresh", "held"} {
					t.Run(route, func(t *testing.T) {
						var executor fixture.Executor = c
						if route == "held" {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						for repeat := range 2 {
							t.Run(fmt.Sprintf("execution%d", repeat+1), func(t *testing.T) {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setval('addr_from_constant',40,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
								row := make([]any, len(tc.names))
								if tc.types[len(tc.types)-1] == "INT8" {
									row[len(row)-1] = int64(1)
								}
								s := fixture.Select(q, tc.names, tc.types, [][]any{row})
								s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
									objectAddressMetadata(t, rows, b.PG, tc.names, tc.types)
								}
								if route == "fresh" {
									s = fixture.Prepared(s)
								}
								fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
								want := int64(40)
								if tc.advances {
									want++
								}
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT last_value FROM addr_from_constant", []string{"last_value"}, []string{"INT8"}, [][]any{{want}}))
							})
						}
					})
				}
			})
		}
	})
}

func TestReferenceObjectAddressTypeArgumentOrder(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var cases []objectAddressDiagnosticCase
		for _, tc := range []struct{ kind, names string }{
			{"function", "ARRAY['pg_catalog','pg_identify_object']"},
			{"aggregate", "ARRAY['pg_catalog','count']"},
			{"procedure", "ARRAY['pg_catalog','pg_identify_object']"},
			{"operator", "ARRAY['pg_catalog','+']"},
			{"cast", "ARRAY['integer']"},
		} {
			for _, malformedFirst := range []bool{true, false} {
				args := "ARRAY['NONE',NULL]::text[]"
				want := fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "NONE"`, Position: "1", Context: `invalid type name "NONE"`}
				name := tc.kind + "_syntax_first"
				if !malformedFirst {
					args = "ARRAY[NULL,'NONE']::text[]"
					want = fixture.Diagnostic{Code: "22023", Message: "name or argument lists may not contain nulls"}
					name = tc.kind + "_null_first"
				}
				q := "SELECT pg_get_object_address('" + tc.kind + "'," + tc.names + "," + args + ")"
				cases = append(cases, objectAddressDiagnosticCase{name, q, want, true})
			}
		}
		// Cast accepts exactly one argument; its name is a type too. Keep
		// name/argument precedence distinct from the multi-type argument loop.
		cases = append(cases,
			objectAddressDiagnosticCase{"cast_syntax_name_null_arg", "SELECT pg_get_object_address('cast',ARRAY['NONE'],ARRAY[NULL]::text[])", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "NONE"`, Position: "1", Context: `invalid type name "NONE"`}, true},
			objectAddressDiagnosticCase{"cast_null_name_syntax_arg", "SELECT pg_get_object_address('cast',ARRAY[NULL]::text[],ARRAY['NONE'])", fixture.Diagnostic{Code: "22023", Message: "name or argument lists may not contain nulls"}, true},
		)
		objectAddressDiagnostics(t, p, b, c, cases)
	})
}
