package routine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// One closed REL_18_6 analyzed recipe owns both parsed structure and display.
// Typed slots are metadata identities, never checked executable expressions.
var builtinRoutineDeclarationBodies = func() map[ids.OID]sqltype.StructuralTree {
	t, a, f, typ := proc.DeclarationText, proc.DeclarationArgument, proc.DeclarationFunction, proc.DeclarationTypeName
	op := func(name string, left, right uint32) proc.DeclarationPart {
		part := proc.DeclarationOp(name, left, right)
		for _, identity := range builtinOperators {
			if identity.name == name && uint32(identity.left) == left && uint32(identity.right) == right {
				part.OID = uint32(identity.oid)
				return part
			}
		}
		panic("stuntgres: unknown released declaration operator")
	}
	ret := func(parts ...proc.DeclarationPart) proc.DeclarationRecipe {
		return proc.DeclarationRecipe{Return: true, Parts: parts}
	}
	text := func(value string) []proc.DeclarationPart {
		return []proc.DeclarationPart{proc.DeclarationString(value), t("::"), typ(25, "text")}
	}
	callText := func(name, signature, value string) proc.DeclarationRecipe {
		parts := []proc.DeclarationPart{f(name, signature), t("("), a(0), t(", ")}
		if name == "lpad" || name == "rpad" {
			parts = append(parts, a(1), t(", "))
		}
		parts = append(parts, text(value)...)
		parts = append(parts, t(")"))
		return ret(parts...)
	}
	plus := func(left, right uint32) proc.DeclarationRecipe {
		return ret(t("("), a(1), t(" "), op("+", left, right), t(" "), a(0), t(")"))
	}
	overlaps := func(base uint32, leftInterval, rightInterval bool) proc.DeclarationRecipe {
		parts := []proc.DeclarationPart{t("(("), a(0), t(", ")}
		if leftInterval {
			parts = append(parts, t("("), a(0), t(" "), op("+", base, 1186), t(" "), a(1), t(")"))
		} else {
			parts = append(parts, a(1))
		}
		parts = append(parts, t(") OVERLAPS ("), a(2), t(", "))
		if rightInterval {
			parts = append(parts, t("("), a(2), t(" "), op("+", base, 1186), t(" "), a(3), t(")"))
		} else {
			parts = append(parts, a(3))
		}
		return ret(append(parts, t("))"))...)
	}
	log := func() proc.DeclarationRecipe {
		return ret(f("log", "1700,1700"), t("((10)::"), typ(1700, "numeric"), t(", "), a(0), t(")"))
	}
	bitLength := func(signature string) proc.DeclarationRecipe {
		return ret(t("("), f("octet_length", signature), t("("), a(0), t(") "), op("*", 23, 23), t(" 8)"))
	}
	description := func(relation uint32, name string, class bool, column bool) proc.DeclarationRecipe {
		col := func(attr int32, name string) proc.DeclarationPart {
			return proc.DeclarationAttribute(relation, func() string {
				if relation == 2396 {
					return "pg_shdescription"
				}
				return "pg_description"
			}(), attr, name)
		}
		descriptionAttr := int32(4)
		if relation == 2396 {
			descriptionAttr = 3
		}
		parts := []proc.DeclarationPart{t("SELECT "), col(descriptionAttr, "description"), t("\n    FROM "), proc.DeclarationTable(relation, name), t("\n   WHERE (("), col(1, "objoid"), t(" "), op("=", 26, 26), t(" "), a(0), t(")")}
		if class || column {
			parts = append(parts, t(" AND ("), col(2, "classoid"), t(" "), op("=", 26, 26), t(" "))
			if class {
				parts = append(parts, t("( SELECT "), proc.DeclarationAttribute(1259, "pg_class", 1, "oid"), t("\n            FROM "), proc.DeclarationTable(1259, "pg_class"), t("\n           WHERE (("), proc.DeclarationAttribute(1259, "pg_class", 2, "relname"), t(" "), op("=", 19, 19), t(" "), a(1), t(") AND ("), proc.DeclarationAttribute(1259, "pg_class", 3, "relnamespace"), t(" "), op("=", 26, 26), t(" ("), proc.DeclarationCatalogAlias(2615, 11, "pg_catalog"), t("::"), typ(4089, "regnamespace"), t(")::"), typ(26, "oid"), t(")))"))
			} else {
				parts = append(parts, t("("), proc.DeclarationCatalogAlias(1259, 1259, "pg_class"), t("::"), typ(2205, "regclass"), t(")::"), typ(26, "oid"))
			}
			parts = append(parts, t(")"))
		}
		if relation != 2396 {
			parts = append(parts, t(" AND ("), col(3, "objsubid"), t(" "), op("=", 23, 23), t(" "))
			if column {
				parts = append(parts, a(1))
			} else {
				parts = append(parts, t("0"))
			}
			parts = append(parts, t(")"))
		}
		return proc.DeclarationRecipe{Parts: append(parts, t(")"))}
	}
	sleep := func(interval bool) proc.DeclarationRecipe {
		parts := []proc.DeclarationPart{f("pg_sleep", "701"), t("(((EXTRACT("), proc.DeclarationIdentifierName("epoch"), t(" FROM ")}
		if interval {
			parts = append(parts, t("("), f("clock_timestamp", ""), t("() "), op("+", 1184, 1186), t(" "), a(0), t(")"))
		} else {
			parts = append(parts, a(0))
		}
		parts = append(parts, t(") "), op("-", 1700, 1700), t(" EXTRACT("), proc.DeclarationIdentifierName("epoch"), t(" FROM "), f("clock_timestamp", ""), t("())))::"), typ(701, "double precision"), t(")"))
		return ret(parts...)
	}
	recipes := map[ids.OID]proc.DeclarationRecipe{
		6183: debugDeclarationRecipe(true),
		6184: debugDeclarationRecipe(false),
		2325: callText("pg_relation_size", "2205,25", "main"),
		5023: plus(3220, 1700), 879: callText("lpad", "25,23,25", " "), 880: callText("rpad", "25,23,25", " "),
		1215: description(2609, "pg_description", true, false), 1216: description(2609, "pg_description", false, true),
		1296: plus(1082, 1083), 1298: plus(1082, 1266),
		1305: overlaps(1184, true, true), 1306: overlaps(1184, false, true), 1307: overlaps(1184, true, false),
		1309: overlaps(1083, true, true), 1310: overlaps(1083, false, true), 1311: overlaps(1083, true, false),
		1348: description(2609, "pg_description", false, false),
		1384: ret(f("date_part", "25,1114"), t("("), a(0), t(", ("), a(1), t(")::"), typ(1114, "timestamp without time zone"), t(")")),
		1426: ret(f("on_ppath", "600,602"), t("("), a(1), t(", "), a(0), t(")")),
		1481: log(), 1544: ret(f("polygon", "23,718"), t("(12, "), a(0), t(")")),
		1708: ret(f("round", "1700,23"), t("("), a(0), t(", 0)")), 1710: ret(f("trunc", "1700,23"), t("("), a(0), t(", 0)")), 1741: log(),
		1810: bitLength("17"), 1811: bitLength("25"), 1812: ret(f("length", "1560"), t("("), a(0), t(")")), 1848: plus(1083, 1186),
		1993: description(2396, "pg_shdescription", true, false),
		2042: overlaps(1114, true, true), 2043: overlaps(1114, false, true), 2044: overlaps(1114, true, false),
		2059: ret(f("age", "1114,1114"), t("((CURRENT_DATE)::"), typ(1114, "timestamp without time zone"), t(", "), a(0), t(")")),
		2074: ret(f("substring", "25,25"), t("("), a(0), t(", "), f("similar_to_escape", "25,25"), t("("), a(1), t(", "), a(2), t("))")),
		2546: plus(1082, 1186), 2547: plus(1266, 1186), 2548: plus(1114, 1186), 2549: plus(1184, 1186), 2550: plus(1082, 23), 2631: plus(869, 20),
		3935: sleep(true), 3936: sleep(false),
	}
	bodies := make(map[ids.OID]sqltype.StructuralTree, len(recipes))
	for oid, recipe := range recipes {
		display, ok := builtinRoutineDisplay[oid]
		if !ok || display.Language != catalog.SQLRoutineLanguage || display.Source != "" {
			panic(fmt.Sprintf("stuntgres: unexpected released routine body %d", oid))
		}
		body, err := proc.NewRoutineDeclarationBody(recipe)
		if err != nil {
			panic(fmt.Sprintf("stuntgres: invalid released routine body %d: %v", oid, err))
		}
		bodies[oid] = body
	}
	for oid, display := range builtinRoutineDisplay {
		if (display.Language == catalog.SQLRoutineLanguage && display.Source == "") != (bodies[oid] != nil) {
			panic(fmt.Sprintf("stuntgres: incomplete released routine body %d", oid))
		}
	}
	return bodies
}()
