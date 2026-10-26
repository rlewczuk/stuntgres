package reference_test

import "strings"

// User-authorized sparse runtime basis. DirectEdges owns declared conversions;
// these ordinary fixtures cover distinct providers and resolution behavior, not
// a source×destination×route product. Full context admission is a kernel test.
type castRuntimeCase struct{ source, target, family string }

func castCaseCell(source, target string) castMatrixCell {
	s, t := castInventoryType(source), castInventoryType(target)
	cell := castMatrixCell{source: s, target: t}
	for i, c := range []byte{'i', 'a', 'e'} {
		cell.contexts[i] = castReleasedPath(s, t, c)
	}
	return cell
}
func castHasDirect(source, target string) bool {
	for _, e := range castReleasedEdges {
		if e.source == source && e.target == target {
			return true
		}
	}
	return false
}
func castRuntimeCases(function bool) []castRuntimeCase {
	var cases []castRuntimeCase
	add := func(source, target, family string) {
		for _, c := range cases {
			if c.source == source && c.target == target {
				return
			}
		}
		cases = append(cases, castRuntimeCase{source, target, family})
	}
	if function {
		for _, fn := range castReleasedCallables {
			add(fn.argument, fn.callee, castInventoryType(fn.argument).family)
		}
		for _, pair := range [][2]string{{"int4", "int4"}, {"uuid", "text"}, {"text", "int4[]"}, {"int4[]", "int4[]"}, {"int4[]", "int8[]"}, {"cast_pair", "text"}, {"cast_anonymous_record", "text"}, {"int2", "bool"}, {"date", "time"}, {"cast_positive", "float8"}, {"void", "void"}, {"cast_anonymous_record", "cast_deferred_record"}, {"cast_deferred_record", "cast_deferred_record"}, {"record[]", "record[]"}, {"cast_enum_a[]", "cast_enum_a[]"}, {"cast_enum_b[]", "cast_enum_b[]"}} {
			add(pair[0], pair[1], castInventoryType(pair[0]).family)
		}
		for _, pair := range [][2]string{{"\"char\"", "regclass"}, {"pg_node_tree", "\"char\""}, {"pg_node_tree", "regclass"}, {"cidr", "text"}} {
			add(pair[0], pair[1], castInventoryType(pair[0]).family)
		}
	} else {
		for _, fact := range castImplementedTypes {
			if fact.base != "" {
				continue
			}
			if fact.name != "cast_anonymous_record" && !castHasDirect("text", fact.name) {
				add("text", fact.name, fact.family)
			}
			if !castHasDirect(fact.name, "text") {
				add(fact.name, "text", fact.family)
			}
			if !fact.target || fact.oid == 2249 || fact.name == "void" {
				add(fact.name, fact.name, fact.family)
			}
		}
		for _, pair := range [][2]string{{"bool", "name"}, {"inet", "name"}, {"varchar", "uuid"}, {"bpchar", "uuid"}, {"name", "uuid"}, {"citext", "uuid"}, {"information_schema.yes_or_no", "int4"}, {"cast_short", "uuid"}, {"cube", "earth"}, {"earth", "cube"}, {"cast_pair", "cast_deferred_record"}, {"cast_pair[]", "record[]"}, {"cast_anonymous_record", "cast_pair"}, {"cast_deferred_record", "cast_pair"}, {"record[]", "cast_pair[]"}, {"cast_pair", "cast_pair_peer"}} {
			add(pair[0], pair[1], castInventoryType(pair[0]).family)
		}
	}
	for _, alias := range []string{"regconfig[]", "regdictionary[]"} {
		add("text", alias, "Arrays")
		add(alias, "text", "Arrays")
		add(alias, alias, "Arrays")
		add("int4[]", alias, "Arrays")
		add(alias, "int8[]", "Arrays")
	}
	for _, pair := range [][2]string{{"int8", "bool"}, {"refcursor", "int4"}, {"regclass", "regtype"}, {"date", "int4"}, {"uuid", "int4"}, {"json", "int4"}, {"line", "point"}, {"int4range", "int8range"}, {"bool[]", "int8[]"}, {"cast_positive", "uuid"}, {"cube", "int4"}, {"cast_enum_a", "cast_enum_b"}} {
		add(pair[0], pair[1], castInventoryType(pair[0]).family)
	}
	return cases
}
func castPrerequisiteGroup(cell castMatrixCell) string {
	for _, fact := range []castImplementedType{cell.source, cell.target} {
		p := castPhysicalFact(fact)
		for p.element != "" {
			p = castPhysicalFact(castInventoryType(p.element))
		}
		if p.family == "Installed" {
			return "Installed"
		}
	}
	if strings.Contains(cell.source.name, "cast_") || strings.Contains(cell.target.name, "cast_") {
		return "UserShapes"
	}
	return "Builtin"
}
