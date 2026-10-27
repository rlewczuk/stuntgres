package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL 18.6 pg_type.dat, information_schema.sql and installed extension
// definitions specify identities/categories. Roles are the currently executable
// project surface, not inferred from pg_type presence or associated-array OIDs.
// Alias spellings are not distinct Cartesian identities.
type castImplementedType struct {
	name, diagnostic, family   string
	oid                        int
	category, kind             string
	seed, output               string
	base, element, companion   string
	storage, parameter, target bool
	source                     string // genuine expression/provider route when not a stored column
}

func castScalar(name, diagnostic, family string, oid int, category, seed, output string) castImplementedType {
	return castImplementedType{name: name, diagnostic: diagnostic, family: family, oid: oid, category: category, kind: "b", seed: seed, output: output, storage: true, parameter: true, target: true}
}

var castImplementedTypes = func() []castImplementedType {
	types := []castImplementedType{
		castScalar("bool", "boolean", "Numeric", 16, "B", "t", "t"),
		castScalar("bytea", "bytea", "BinaryNetwork", 17, "U", `\x00000001`, `\x00000001`),
		castScalar(`"char"`, `"char"`, "Strings", 18, "Z", "r", "r"),
		castScalar("name", "name", "Strings", 19, "S", "red", "red"),
		castScalar("int2", "smallint", "Numeric", 21, "N", "1", "1"),
		castScalar("int4", "integer", "Numeric", 23, "N", "1", "1"),
		castScalar("int8", "bigint", "Numeric", 20, "N", "1", "1"),
		castScalar("float4", "real", "Numeric", 700, "N", "1", "1"),
		castScalar("float8", "double precision", "Numeric", 701, "N", "1", "1"),
		castScalar("numeric", "numeric", "Numeric", 1700, "N", "1", "1"),
		castScalar("money", "money", "Numeric", 790, "N", "1.00", "$1.00"),
		castScalar("text", "text", "Strings", 25, "S", "red", "red"),
		castScalar("varchar", "character varying", "Strings", 1043, "S", "red", "red"),
		castScalar("bpchar", "character", "Strings", 1042, "S", "red", "red"),
		castScalar("refcursor", "refcursor", "Strings", 1790, "U", "cast_cursor", "cast_cursor"),
		castScalar("oid", "oid", "Identifiers", 26, "N", "0", "0"),
		castScalar("int2vector", "int2vector", "Identifiers", 22, "A", "1 2 3", "1 2 3"),
		castScalar("oidvector", "oidvector", "Identifiers", 30, "A", "1 2 3", "1 2 3"),
		castScalar("regclass", "regclass", "Identifiers", 2205, "N", "0", "-"),
		castScalar("regtype", "regtype", "Identifiers", 2206, "N", "0", "-"),
		castScalar("regproc", "regproc", "Identifiers", 24, "N", "0", "-"),
		castScalar("regprocedure", "regprocedure", "Identifiers", 2202, "N", "0", "-"),
		castScalar("regrole", "regrole", "Identifiers", 4096, "N", "0", "-"),
		castScalar("regoper", "regoper", "Identifiers", 2203, "N", "0", "0"),
		castScalar("regoperator", "regoperator", "Identifiers", 2204, "N", "0", "0"),
		castScalar("regcollation", "regcollation", "Identifiers", 4191, "N", "0", "-"),
		castScalar("regnamespace", "regnamespace", "Identifiers", 4089, "N", "0", "-"),
		castScalar("regconfig", "regconfig", "Identifiers", 3734, "N", "0", "-"),
		castScalar("regdictionary", "regdictionary", "Identifiers", 3769, "N", "0", "-"),
		castScalar("tid", "tid", "Identifiers", 27, "U", "(1,1)", "(1,1)"),
		castScalar("xid", "xid", "Identifiers", 28, "U", "1", "1"),
		castScalar("cid", "cid", "Identifiers", 29, "U", "1", "1"),
		castScalar("xid8", "xid8", "Identifiers", 5069, "U", "1", "1"),
		castScalar("pg_lsn", "pg_lsn", "Identifiers", 3220, "U", "0/1", "0/1"),
		castScalar("pg_snapshot", "pg_snapshot", "Identifiers", 5038, "U", "10:20:10,15", "10:20:10,15"),
		castScalar("uuid", "uuid", "BinaryNetwork", 2950, "U", "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000001"),
		castScalar("date", "date", "Temporal", 1082, "D", "2000-01-01", "2000-01-01"),
		castScalar("time", "time without time zone", "Temporal", 1083, "D", "00:00:00", "00:00:00"),
		castScalar("timetz", "time with time zone", "Temporal", 1266, "D", "00:00:00+00", "00:00:00+00"),
		castScalar("timestamp", "timestamp without time zone", "Temporal", 1114, "D", "2000-01-01 00:00:00", "2000-01-01 00:00:00"),
		castScalar("timestamptz", "timestamp with time zone", "Temporal", 1184, "D", "2000-01-01 00:00:00+00", "2000-01-01 00:00:00+00"),
		castScalar("interval", "interval", "Temporal", 1186, "T", "1 hour", "01:00:00"),
		castScalar("bit", "bit", "BinaryNetwork", 1560, "V", "1", "1"),
		castScalar("varbit", "bit varying", "BinaryNetwork", 1562, "V", "1", "1"),
		castScalar("inet", "inet", "BinaryNetwork", 869, "I", "10.0.0.0/8", "10.0.0.0/8"),
		castScalar("cidr", "cidr", "BinaryNetwork", 650, "I", "10.0.0.0/8", "10.0.0.0/8"),
		castScalar("macaddr", "macaddr", "BinaryNetwork", 829, "U", "00:34:56:ab:cd:ef", "00:34:56:ab:cd:ef"),
		castScalar("macaddr8", "macaddr8", "BinaryNetwork", 774, "U", "00:34:56:ff:fe:ab:cd:ef", "00:34:56:ff:fe:ab:cd:ef"),
		castScalar("aclitem", "aclitem", "BinaryNetwork", 1033, "U", "=r/cast_acl_grantor", "=r/cast_acl_grantor"),
		castScalar("json", "json", "JSONFTS", 114, "U", "1", "1"),
		castScalar("jsonb", "jsonb", "JSONFTS", 3802, "U", "1", "1"),
		castScalar("jsonpath", "jsonpath", "JSONFTS", 4072, "U", "$", "$"),
		castScalar("tsvector", "tsvector", "JSONFTS", 3614, "U", "red", "'red'"),
		castScalar("tsquery", "tsquery", "JSONFTS", 3615, "U", "red", "'red'"),
		castScalar("point", "point", "Geometry", 600, "G", "(2,3)", "(2,3)"),
		castScalar("lseg", "lseg", "Geometry", 601, "G", "[(2,4),(8,10)]", "[(2,4),(8,10)]"),
		castScalar("path", "path", "Geometry", 602, "G", "((0,0),(2,0),(0,2))", "((0,0),(2,0),(0,2))"),
		castScalar("box", "box", "Geometry", 603, "G", "(4,4),(0,0)", "(4,4),(0,0)"),
		castScalar("polygon", "polygon", "Geometry", 604, "G", "((0,0),(2,0),(0,2))", "((0,0),(2,0),(0,2))"),
		castScalar("line", "line", "Geometry", 628, "G", "{1,-1,0}", "{1,-1,0}"),
		castScalar("circle", "circle", "Geometry", 718, "G", "<(0,0),2>", "<(0,0),2>"),
		castScalar("pg_node_tree", "pg_node_tree", "Strings", 194, "Z", "", ""),
		castScalar("void", "void", "Structural", 2278, "P", "", ""),
	}
	for i := range types {
		switch types[i].name {
		case "pg_node_tree":
			types[i].storage = false
			types[i].parameter = false
			types[i].source = `(SELECT conbin FROM pg_constraint WHERE conname='cast_inventory_check' AND conrelid='cast_inventory_source'::regclass)`
		case "void":
			types[i].storage = false
			types[i].parameter = false
			types[i].kind = "p"
			types[i].source = "pg_sleep(0)"
		}
	}
	for _, r := range []struct {
		name      string
		oid       int
		seed      string
		companion string
	}{
		{"int4range", 3904, "[1,3)", "int4multirange"}, {"int8range", 3926, "[1,3)", "int8multirange"}, {"numrange", 3906, "[1,3)", "nummultirange"},
		{"daterange", 3912, "[2000-01-01,2000-01-03)", ""}, {"tsrange", 3908, `["2000-01-01 00:00:00","2000-01-03 00:00:00")`, ""}, {"tstzrange", 3910, `["2000-01-01 00:00:00+00","2000-01-03 00:00:00+00")`, ""},
		{"int4multirange", 4451, "{[1,3)}", ""}, {"int8multirange", 4536, "{[1,3)}", ""}, {"nummultirange", 4532, "{[1,3)}", ""},
	} {
		v := castScalar(r.name, r.name, "Ranges", r.oid, "R", r.seed, r.seed)
		v.companion = r.companion
		if strings.Contains(r.name, "multirange") {
			v.kind = "m"
		} else {
			v.kind = "r"
		}
		types = append(types, v)
	}
	for _, a := range []struct {
		name string
		oid  int
	}{
		{`"char"`, 1002},
		{"bool", 1000}, {"bytea", 1001}, {"name", 1003}, {"int2", 1005}, {"int4", 1007}, {"int8", 1016}, {"text", 1009}, {"oid", 1028}, {"json", 199}, {"jsonb", 3807}, {"jsonpath", 4073}, {"bpchar", 1014}, {"varchar", 1015}, {"float4", 1021}, {"float8", 1022}, {"date", 1182}, {"time", 1183}, {"timetz", 1270}, {"timestamp", 1115}, {"timestamptz", 1185}, {"interval", 1187}, {"numeric", 1231}, {"uuid", 2951}, {"regclass", 2210}, {"regtype", 2211}, {"regproc", 1008}, {"regprocedure", 2207}, {"regrole", 4097}, {"regcollation", 4192}, {"regnamespace", 4090}, {"aclitem", 1034}, {"xid", 1011},
		{"regconfig", 3735}, {"regdictionary", 3770},
		{"tid", 1010},
	} {
		var e castImplementedType
		for _, v := range types {
			if v.name == a.name {
				e = v
				break
			}
		}
		v := castScalar(a.name+"[]", e.diagnostic+"[]", "Arrays", a.oid, "A", castArrayText(e.seed), castArrayText(e.output))
		v.element = a.name
		if a.name == "xid" {
			v.storage = false
			v.target = false
			v.parameter = false
			v.source = "ARRAY['1'::xid,NULL::xid]"
		}
		types = append(types, v)
	}
	for _, d := range []struct{ name, base, seed string }{
		{"information_schema.cardinal_number", "int4", "1"}, {"information_schema.character_data", "varchar", "red"}, {"information_schema.sql_identifier", "name", "red"}, {"information_schema.time_stamp", "timestamptz", "2000-01-01 00:00:00+00"}, {"information_schema.yes_or_no", "varchar", "YES"},
		{"cast_int_domain", "int4", "1"}, {"cast_positive", "int4", "1"}, {"cast_nested", "cast_positive", "1"}, {"cast_required", "int4", "1"}, {"cast_short", "varchar", "red"}, {"cast_array_domain", "int4[]", "{1,NULL,2}"},
	} {
		category := "N"
		for _, v := range types {
			if v.name == d.base {
				category = v.category
			}
		}
		v := castScalar(d.name, d.name, "Domains", 0, category, d.seed, d.seed)
		v.base = d.base
		v.kind = "d"
		types = append(types, v)
	}
	for _, e := range []struct{ name, category, seed, output, base string }{
		{"cube", "U", "(1,2)", "(1, 2)", ""}, {"query_int", "U", "1", "1", ""}, {"citext", "S", "red", "red", ""}, {"ean13", "U", "1234567890128", "123-456789012-8", ""}, {"earth", "U", "(6378168,0,0)", "(6378168, 0, 0)", "cube"},
	} {
		v := castScalar(e.name, e.name, "Installed", 0, e.category, e.seed, e.output)
		v.base = e.base
		if e.base != "" {
			v.kind = "d"
		}
		types = append(types, v)
	}
	for _, name := range []string{"cast_enum_a", "cast_enum_b"} {
		v := castScalar(name, name, "Structural", 0, "E", "red", "red")
		v.kind = "e"
		types = append(types, v)
		a := castScalar(name+"[]", name+"[]", "Arrays", 0, "A", "{red,NULL}", "{red,NULL}")
		a.element = name
		a.storage = false
		a.parameter = false
		a.target = false
		a.source = "ARRAY['red'::" + name + ",NULL::" + name + "]"
		types = append(types, a)
	}
	for _, name := range []string{"cast_pair", "cast_pair_peer", "cast_row"} {
		v := castScalar(name, name, "Structural", 0, "C", "(1,red)", "(1,red)")
		v.kind = "c"
		types = append(types, v)
	}
	for _, name := range []string{"cast_range", "cast_other_range"} {
		v := castScalar(name, name, "Ranges", 0, "R", "[1,3)", "[1,3)")
		v.kind = "r"
		v.companion = strings.TrimSuffix(name, "range") + "multirange"
		types = append(types, v)
		m := castScalar(v.companion, v.companion, "Ranges", 0, "R", "{[1,3)}", "{[1,3)}")
		m.kind = "m"
		types = append(types, m)
	}
	for _, name := range []string{"cast_int_domain", "cast_positive", "cast_nested", "cast_required", "cast_short", "cast_array_domain", "cast_pair", "cast_pair_peer", "cast_row", "cast_range", "cast_multirange", "cast_other_range", "cast_other_multirange"} {
		var e castImplementedType
		for _, v := range types {
			if v.name == name {
				e = v
			}
		}
		seed, output := castArrayText(e.seed), castArrayText(e.output)
		if name == "cast_required" {
			seed = "{1,2}"
			output = seed
		}
		v := castScalar(name+"[]", name+"[]", "Arrays", 0, "A", seed, output)
		v.element = name
		types = append(types, v)
	}
	for _, r := range []struct{ name, source string }{{"cast_anonymous_record", "ROW(1,'red'::text)"}, {"cast_deferred_record", "NULL::record"}, {"record[]", "ARRAY[ROW(1,'red'::text),NULL::record]"}} {
		v := castScalar(r.name, "record", "Structural", 2249, "P", "(1,red)", "(1,red)")
		v.kind = "p"
		v.storage = false
		v.parameter = false
		v.target = r.name != "record[]"
		v.source = r.source
		if r.name == "record[]" {
			v.oid = 2287
			v.seed, v.output = `{"(1,red)",NULL}`, `{"(1,red)",NULL}`
			v.element = "cast_anonymous_record"
			v.diagnostic = "record[]"
			v.family = "Arrays"
		}
		types = append(types, v)
	}
	return types
}()

func castArrayText(s string) string {
	if s == "" || strings.EqualFold(s, "NULL") || strings.ContainsAny(s, "{},\"\\ \t\r\n") {
		s = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return "{" + s + ",NULL}"
}
func castInventoryType(name string) castImplementedType {
	for _, v := range castImplementedTypes {
		if v.name == name {
			return v
		}
	}
	panic("missing independently listed cast identity: " + name)
}
func castInventoryIdentity(v castImplementedType) string {
	if v.name == "cast_anonymous_record" || v.name == "cast_deferred_record" {
		return "record"
	}
	return v.name
}

func castSetupQueries(t *testing.T, ctx context.Context, b fixture.Backend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		n := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			n = 1
		}
		fixture.RunStep(t, ctx, b.DB, b.Recorder, fixture.Exec(q, n))
	}
}
func castBuiltinSetup(t *testing.T, ctx context.Context, b fixture.Backend) {
	t.Helper()
	castSetupQueries(t, ctx, b, "SET TIME ZONE 'UTC'", "CREATE TABLE cast_inventory_source(n integer CONSTRAINT cast_inventory_check CHECK(n>0))", "INSERT INTO cast_inventory_source VALUES(1)", "CREATE TABLE cast_direct(n integer)", "CREATE TABLE r(n integer)", "CREATE TABLE x(n integer)")
}
func castUserShapesSetup(t *testing.T, ctx context.Context, b fixture.Backend) {
	t.Helper()
	castSetupQueries(t, ctx, b,
		"CREATE TYPE cast_enum_a AS ENUM ('red','blue')", "CREATE TYPE cast_enum_b AS ENUM ('red','blue')",
		"CREATE DOMAIN cast_int_domain AS integer", "CREATE DOMAIN cast_positive AS integer CHECK(VALUE>0)", "CREATE DOMAIN cast_nested AS cast_positive", "CREATE DOMAIN cast_required AS integer NOT NULL", "CREATE DOMAIN cast_short AS varchar(3) COLLATE \"C\"", "CREATE DOMAIN cast_array_domain AS integer[] CHECK(cardinality(VALUE)>0)",
		"CREATE TYPE cast_pair AS(n integer,s text)", "CREATE TYPE cast_pair_peer AS(n integer,s text)", "CREATE TABLE cast_row(n integer,s text)",
		"CREATE TYPE cast_range AS RANGE(subtype=integer)", "CREATE TYPE cast_other_range AS RANGE(subtype=integer)")
}
func castInstalledSetup(t *testing.T, ctx context.Context, b fixture.Backend) {
	t.Helper()
	castSetupQueries(t, ctx, b, "CREATE EXTENSION cube", "CREATE EXTENSION intarray", "CREATE EXTENSION citext", "CREATE EXTENSION isn", "CREATE EXTENSION earthdistance")
}
func castInventorySetup(t *testing.T, ctx context.Context, b fixture.Backend) {
	t.Helper()
	castBuiltinSetup(t, ctx, b)
	castUserShapesSetup(t, ctx, b)
	castInstalledSetup(t, ctx, b)
}

func TestReferenceCastImplementedInventory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			castInventorySetup(t, p.Ctx, b)
			seen := map[string]bool{}
			var expressions, columns, types []string
			var values []any
			flush := func() {
				if len(expressions) == 0 {
					return
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT "+strings.Join(expressions, ","), columns, types, [][]any{values}))
				expressions, columns, types, values = nil, nil, nil, nil
			}
			for _, v := range castImplementedTypes {
				if seen[v.name] {
					t.Fatalf("duplicate identity %s", v.name)
				}
				seen[v.name] = true
				if v.family == "" || v.category == "" || v.kind == "" {
					t.Fatalf("unclassified inventory role %+v", v)
				}
				if v.name == "pg_snapshot" && !b.PG {
					// The executable descriptor is guarded independently in sqltype.
					// Native pg_type population is a separate, existing catalog gap.
					q := "SELECT CAST($1 AS pg_snapshot) AS v"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, castResultStep(t, q, v, v.output, b.PG, v.seed))
					// One real executable-value witness; no second route product.
					continue
				}
				identity := comparisonSQLString(castInventoryIdentity(v)) + "::regtype"
				if len(expressions)+3 > 32 {
					flush()
				}
				oidExpr, oidType, oidValue := "oid::int8", "INT8", any(int64(v.oid))
				if v.oid == 0 {
					oidExpr, oidType, oidValue = "oid>0", "BOOL", true
				}
				for _, field := range []struct {
					expression, typ string
					value           any
				}{{oidExpr, oidType, oidValue}, {"typtype::text", "TEXT", v.kind}, {"typcategory::text", "TEXT", v.category}} {
					name := fmt.Sprintf("v%d", len(expressions))
					expressions = append(expressions, "(SELECT "+field.expression+" FROM pg_type WHERE oid="+identity+") AS "+name)
					columns = append(columns, name)
					types = append(types, field.typ)
					values = append(values, field.value)
				}
			}
			flush()
			// Identical labels/layout/subtype are not logical identity equivalence.
			for _, pair := range [][2]string{{"cast_enum_a", "cast_enum_b"}, {"cast_pair", "cast_pair_peer"}, {"cast_range", "cast_other_range"}, {"cast_multirange", "cast_other_multirange"}} {
				q := "SELECT " + comparisonSQLString(pair[0]) + "::regtype::oid<>" + comparisonSQLString(pair[1]) + "::regtype::oid AS different"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"different"}, []string{"BOOL"}, [][]any{{true}}))
			}
		})
	}
}
