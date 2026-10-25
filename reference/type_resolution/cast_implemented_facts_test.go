package reference_test

import "strings"

// Independent release authority, never queried from either backend's pg_cast:
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_cast.dat
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/citext/citext--1.4.sql
// Generic rules: REL_18_6 parse_coerce.c find_coercion_pathway/coerce_type;
// function form: parse_func.c func_get_detail, PG18 typeconv-func steps 2–4.
type castReleasedEdge struct {
	source, target  string
	context, method byte
	function        string
	authority       string
}

var castReleasedEdges = func() []castReleasedEdge {
	var edges []castReleasedEdge
	add := func(s, t string, c, m byte, f string) {
		edges = append(edges, castReleasedEdge{s, t, c, m, f, "REL_18_6 pg_cast.dat"})
	}
	// Complete numeric submatrix; no transitive rule is used by the classifier.
	numeric := []string{"int2", "int4", "int8", "numeric", "float4", "float8"}
	for i, s := range numeric {
		for j, t := range numeric {
			if i == j {
				continue
			}
			c := byte('i')
			if j < i {
				c = 'a'
			}
			add(s, t, c, 'f', t+"("+s+")")
		}
	}
	for _, e := range []struct {
		s, t string
		c, m byte
		f    string
	}{
		{"money", "numeric", 'a', 'f', "numeric(money)"}, {"numeric", "money", 'a', 'f', "money(numeric)"}, {"int4", "money", 'a', 'f', "money(int4)"}, {"int8", "money", 'a', 'f', "money(int8)"},
		{"int4", "bool", 'e', 'f', "bool(int4)"}, {"bool", "int4", 'e', 'f', "int4(bool)"}, {"xid8", "xid", 'e', 'f', "xid(xid8)"},
		{"regproc", "regprocedure", 'i', 'b', ""}, {"regprocedure", "regproc", 'i', 'b', ""}, {"regoper", "regoperator", 'i', 'b', ""}, {"regoperator", "regoper", 'i', 'b', ""},
		{"text", "regclass", 'i', 'f', "regclass(text)"}, {"varchar", "regclass", 'i', 'f', "regclass(text)"},
		{"text", "bpchar", 'i', 'b', ""}, {"text", "varchar", 'i', 'b', ""}, {"varchar", "text", 'i', 'b', ""}, {"varchar", "bpchar", 'i', 'b', ""},
		{"bpchar", "text", 'i', 'f', "text(bpchar)"}, {"bpchar", "varchar", 'i', 'f', "text(bpchar)"},
		{`"char"`, "text", 'i', 'f', `text("char")`}, {`"char"`, "bpchar", 'a', 'f', `bpchar("char")`}, {`"char"`, "varchar", 'a', 'f', `text("char")`},
		{"name", "text", 'i', 'f', "text(name)"}, {"name", "bpchar", 'a', 'f', "bpchar(name)"}, {"name", "varchar", 'a', 'f', "varchar(name)"},
		{"text", `"char"`, 'a', 'f', `char(text)`}, {"varchar", `"char"`, 'a', 'f', `char(text)`}, {"bpchar", `"char"`, 'a', 'f', `char(text)`},
		{"text", "name", 'i', 'f', "name(text)"}, {"varchar", "name", 'i', 'f', "name(varchar)"}, {"bpchar", "name", 'i', 'f', "name(bpchar)"},
		{`"char"`, "int4", 'e', 'f', `int4("char")`}, {"int4", `"char"`, 'e', 'f', `char(int4)`}, {"pg_node_tree", "text", 'i', 'b', ""},
		{"date", "timestamp", 'i', 'f', "timestamp(date)"}, {"date", "timestamptz", 'i', 'f', "timestamptz(date)"},
		{"time", "interval", 'i', 'f', "interval(time)"}, {"time", "timetz", 'i', 'f', "timetz(time)"},
		{"timestamp", "date", 'a', 'f', "date(timestamp)"}, {"timestamp", "time", 'a', 'f', "time(timestamp)"}, {"timestamp", "timestamptz", 'i', 'f', "timestamptz(timestamp)"},
		{"timestamptz", "date", 'a', 'f', "date(timestamptz)"}, {"timestamptz", "time", 'a', 'f', "time(timestamptz)"}, {"timestamptz", "timestamp", 'a', 'f', "timestamp(timestamptz)"}, {"timestamptz", "timetz", 'a', 'f', "timetz(timestamptz)"},
		{"interval", "time", 'a', 'f', "time(interval)"}, {"timetz", "time", 'a', 'f', "time(timetz)"},
		{"point", "box", 'a', 'f', "box(point)"}, {"lseg", "point", 'e', 'f', "point(lseg)"}, {"path", "polygon", 'a', 'f', "polygon(path)"},
		{"box", "point", 'e', 'f', "point(box)"}, {"box", "lseg", 'e', 'f', "lseg(box)"}, {"box", "polygon", 'a', 'f', "polygon(box)"}, {"box", "circle", 'e', 'f', "circle(box)"},
		{"polygon", "point", 'e', 'f', "point(polygon)"}, {"polygon", "path", 'a', 'f', "path(polygon)"}, {"polygon", "box", 'e', 'f', "box(polygon)"}, {"polygon", "circle", 'e', 'f', "circle(polygon)"},
		{"circle", "point", 'e', 'f', "point(circle)"}, {"circle", "box", 'e', 'f', "box(circle)"}, {"circle", "polygon", 'e', 'f', "polygon(circle)"},
		{"macaddr", "macaddr8", 'i', 'f', "macaddr8(macaddr)"}, {"macaddr8", "macaddr", 'i', 'f', "macaddr(macaddr8)"},
		{"cidr", "inet", 'i', 'b', ""}, {"inet", "cidr", 'a', 'f', "cidr(inet)"},
		{"bit", "varbit", 'i', 'b', ""}, {"varbit", "bit", 'i', 'b', ""},
		{"int4", "bit", 'e', 'f', "bit(int4,int4)"}, {"int8", "bit", 'e', 'f', "bit(int8,int4)"}, {"bit", "int4", 'e', 'f', "int4(bit)"}, {"bit", "int8", 'e', 'f', "int8(bit)"},
		{"json", "jsonb", 'a', 'i', ""}, {"jsonb", "json", 'a', 'i', ""},
	} {
		add(e.s, e.t, e.c, e.m, e.f)
	}
	aliases := []string{"oid", "regclass", "regtype", "regproc", "regprocedure", "regrole", "regoper", "regoperator", "regcollation", "regnamespace", "regconfig", "regdictionary"}
	for _, alias := range aliases {
		add("int2", alias, 'i', 'f', "int4(int2)")
		add("int4", alias, 'i', 'b', "")
		add("int8", alias, 'i', 'f', "oid(int8)")
		add(alias, "int4", 'a', 'b', "")
		add(alias, "int8", 'a', 'f', "int8(oid)")
		if alias != "oid" {
			add("oid", alias, 'i', 'b', "")
			add(alias, "oid", 'i', 'b', "")
		}
	}
	for _, n := range []string{"int2", "int4", "int8"} {
		add(n, "bytea", 'e', 'f', "bytea("+n+")")
		add("bytea", n, 'e', 'f', n+"(bytea)")
	}
	for _, s := range []string{"cidr", "inet", "bool"} {
		for _, t := range []string{"text", "varchar", "bpchar"} {
			f := "text(inet)"
			if s == "bool" {
				f = "text(bool)"
			}
			add(s, t, 'a', 'f', f)
		}
	}
	for _, t := range []string{"bool", "int2", "int4", "int8", "numeric", "float4", "float8"} {
		add("jsonb", t, 'e', 'f', t+"(jsonb)")
	}
	for _, n := range []string{"bpchar", "varchar", "time", "timestamp", "timestamptz", "interval", "timetz", "bit", "varbit", "numeric"} {
		args := n + ",int4"
		if n == "bpchar" || n == "varchar" || n == "bit" || n == "varbit" {
			args += ",bool"
		}
		add(n, n, 'i', 'f', n+"("+args+")")
	}
	for _, s := range []string{"int4range", "int8range", "numrange", "cast_range", "cast_other_range"} {
		t := castInventoryType(s).companion
		add(s, t, 'e', 'f', t+"("+s+")")
	}
	for _, e := range []struct {
		s, t string
		c    byte
	}{
		{"citext", "text", 'i'}, {"citext", "varchar", 'i'}, {"citext", "bpchar", 'a'},
		{"text", "citext", 'a'}, {"varchar", "citext", 'a'}, {"bpchar", "citext", 'a'}, {"bool", "citext", 'a'}, {"inet", "citext", 'a'},
	} {
		m := byte('b')
		f := ""
		switch e.s {
		case "bpchar":
			m = 'f'
			f = "citext(bpchar)"
		case "bool":
			m = 'f'
			f = "citext(bool)"
		case "inet":
			m = 'f'
			f = "citext(inet)"
		}
		edges = append(edges, castReleasedEdge{e.s, e.t, e.c, m, f, "REL_18_6 citext--1.4.sql"})
	}
	return edges
}()

type castReleasedDecision struct {
	admitted             bool
	operation, authority string
	edge                 *castReleasedEdge
}

func castContextRank(c byte) int {
	switch c {
	case 'i':
		return 0
	case 'a':
		return 1
	case 'e':
		return 2
	}
	panic("invalid independently specified cast context")
}
func castReleasedPath(source, target castImplementedType, context byte) castReleasedDecision {
	// Domain identity is preserved by callers; base peeling selects the physical
	// operation, never authorizes bypassing destination constraints/typmods.
	if castInventoryIdentity(source) == castInventoryIdentity(target) {
		return castReleasedDecision{true, "identity/typmod", "parse_coerce.c coerce_type", nil}
	}
	if source.base != "" {
		source = castInventoryType(source.base)
	}
	if target.base != "" {
		return castReleasedPath(source, castInventoryType(target.base), context)
	}
	if source.base != "" {
		return castReleasedPath(source, target, context)
	}
	if source.name == target.name {
		return castReleasedDecision{true, "domain-base", "parse_coerce.c coerce_type", nil}
	}
	for i := range castReleasedEdges {
		e := &castReleasedEdges[i]
		if e.source == source.name && e.target == target.name {
			return castReleasedDecision{castContextRank(context) >= castContextRank(e.context), "direct", "REL_18_6 pg_cast.dat / citext--1.4.sql", e}
		}
	}
	if source.element != "" && target.name == "record[]" && castInventoryType(source.element).kind == "c" {
		return castReleasedDecision{true, "named-array-to-record-array", "REL_18_6 parse_coerce.c 506-510 original-node passthrough", nil}
	}
	if source.name == "record[]" && target.element != "" && castInventoryType(target.element).kind == "c" {
		return castReleasedDecision{false, "forbidden-record-array", "REL_18_6 parse_coerce.c NOT_USED reverse structural array coercion", nil}
	}
	// Vector input grammars are not generic array text grammars, but a vector
	// source may use element coercion to an ordinary array. Generic coercion
	// destinations explicitly exclude both vector identities in REL_18_6.
	if target.element!="" && (source.name=="int2vector" || source.name=="oidvector") {
		element:="int2"
		if source.name=="oidvector"{element="oid"}
		d:=castReleasedPath(castInventoryType(element),castInventoryType(target.element),context)
		d.operation="array("+d.operation+")"
		d.authority="REL_18_6 parse_coerce.c find_coercion_pathway vector source ARRAYCOERCE"
		return d
	}
	if source.element != "" && target.element != "" {
		d := castReleasedPath(castInventoryType(source.element), castInventoryType(target.element), context)
		d.operation = "array(" + d.operation + ")"
		d.authority = "parse_coerce.c find_coercion_pathway ARRAYCOERCE"
		return d
	}
	if castInventoryIdentity(target) == "record" && source.kind == "c" {
		return castReleasedDecision{true, "named-to-record", "parse_coerce.c coerce_type RECORDOID", nil}
	}
	if source.name == "cast_anonymous_record" && target.kind == "c" {
		return castReleasedDecision{true, "row-to-composite", "parse_coerce.c coerce_record_to_complex", nil}
	}
	if target.category == "S" && castContextRank(context) >= castContextRank('a') || source.category == "S" && context == 'e' {
		return castReleasedDecision{true, "io", "parse_coerce.c find_coercion_pathway CoerceViaIO", nil}
	}
	return castReleasedDecision{false, "forbidden", "parse_coerce.c find_coercion_pathway COERCION_PATH_NONE", nil}
}

func castFunctionName(v castImplementedType) string {
	if v.element != "" {
		identity := castInventoryIdentity(v)
		namespace := "pg_catalog"
		if v.oid == 0 {
			namespace = "public"
		}
		return namespace + `."_` + strings.TrimSuffix(identity, "[]") + `"`
	}
	if strings.Contains(v.name, ".") {
		parts := strings.Split(v.name, ".")
		return parts[0] + `."` + parts[1] + `"`
	}
	return `"` + strings.Trim(castInventoryIdentity(v), `"`) + `"`
}
