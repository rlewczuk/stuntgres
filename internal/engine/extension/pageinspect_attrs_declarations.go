package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

var pageinspectSplitInputNames = [2]*catalog.RoutineInputNames{
	catalog.NewRoutineInputNames("rel_oid", "t_data", "t_infomask", "t_infomask2", "t_bits"),
	catalog.NewRoutineInputNames("rel_oid", "t_data", "t_infomask", "t_infomask2", "t_bits", "do_detoast"),
}

func pageinspectSplitNames(slot int) *catalog.RoutineInputNames {
	if slot == 17 || slot == 18 {
		return pageinspectSplitInputNames[slot-17]
	}
	return nil
}

// These parsed release SQL bodies are declaration ownership only. Runtime
// composition uses the same admitted heap reader and tuple splitter, not SQL
// source lookup or an additional SQL evaluator.
var pageinspectAttrsBodies = func() [2]sqltype.StructuralTree {
	member := func(slot int, name string, arguments ...uint32) proc.DeclarationPart {
		return proc.DeclarationMember("", slot, uint32(ids.ProcedureClassID), name, arguments...)
	}
	identifier := func(name string) declarationParts { return declarationParts{proc.DeclarationIdentifierName(name)} }
	field := func(alias, name string) declarationParts {
		return declarationJoin(identifier(alias), declarationText("."), identifier(name))
	}
	formal := func(slot int) declarationParts {
		return declarationJoin(declarationParts{proc.DeclarationSelfRoutineName("heap_page_item_attrs")}, declarationText("."), declarationArg(slot))
	}
	columns := []string{"lp", "lp_off", "lp_flags", "lp_len", "t_xmin", "t_xmax", "t_field3", "t_ctid", "t_infomask2", "t_infomask", "t_hoff", "t_bits", "t_oid"}
	projection := func(alias string, names []string) declarationParts {
		parts := declarationParts{}
		for i, name := range names {
			if i != 0 {
				parts = declarationJoin(parts, declarationText(",\n     "))
			}
			parts = declarationJoin(parts, field(alias, name))
		}
		return parts
	}
	// Function-RTE aliases are release-analyzed names, not helper identities.
	// Outer formal qualifiers instead follow this wrapper's current name.
	sourceAlias := func(alias string, names []string) declarationParts {
		parts := declarationJoin(declarationText(" "), identifier(alias), declarationText("("))
		for i, name := range names {
			if i != 0 {
				parts = declarationJoin(parts, declarationText(", "))
			}
			parts = declarationJoin(parts, identifier(name))
		}
		return declarationJoin(parts, declarationText(")"))
	}
	heapColumns := append(append([]string(nil), columns...), "t_data")
	all := append(append([]string(nil), columns...), "t_attrs")
	split := declarationCall(member(18, "tuple_data_split", 26, 17, 23, 23, 25, 16), declarationCast(formal(1), proc.DeclarationTypeName(26, "oid")), field("heap_page_items", "t_data"), field("heap_page_items", "t_infomask"), field("heap_page_items", "t_infomask2"), field("heap_page_items", "t_bits"), formal(2))
	three := declarationJoin(declarationText("SELECT "), projection("heap_page_items", columns), declarationText(",\n     "), split, declarationText(" AS t_attrs\n    FROM "), declarationCall(member(15, "heap_page_items", 17), formal(0)), sourceAlias("heap_page_items", heapColumns))
	two := declarationJoin(declarationText("SELECT "), projection("heap_page_item_attrs", all), declarationText("\n    FROM "), declarationCall(member(19, "heap_page_item_attrs", 17, 2205, 16), formal(0), formal(1), declarationText("false")), sourceAlias("heap_page_item_attrs", all))
	parts := [2]declarationParts{three, two}
	sources := [2]string{
		"SELECT lp,lp_off,lp_flags,lp_len,t_xmin,t_xmax,t_field3,t_ctid,t_infomask2,t_infomask,t_hoff,t_bits,t_oid,tuple_data_split(rel_oid::oid,t_data,t_infomask,t_infomask2,t_bits,do_detoast) AS t_attrs FROM heap_page_items(page)",
		"SELECT * FROM heap_page_item_attrs(page,rel_oid,false)",
	}
	var bodies [2]sqltype.StructuralTree
	for i := range bodies {
		body, err := proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Parts: parts[i], Source: sources[i]})
		if err != nil {
			panic(err)
		}
		bodies[i] = body
	}
	return bodies
}()

// Genuine SQL-body edges share the catalog's dependency visitor with ordinary
// checked routines, so pg_depend, retirement and replay see the same identities.
func (provider *PageinspectProvider) VisitProcedureDependencies(_ catalog.Read, p *catalog.Procedure, yield func(ids.ObjectAddress) bool) bool {
	if provider != StockPageinspect || p == nil || p.Implementation != provider || !p.Origin.Valid(p.Address()) || p.Origin.Installation.Implementation != provider || len(p.Origin.Installation.Members) != len(PageinspectMembers) {
		return true
	}
	slot := p.Origin.Declaration
	if (slot != 19 && slot != 20) || p.DeclarationBody != pageinspectAttrsBodies[slot-19] {
		return true
	}
	members := p.Origin.Installation.Members
	if slot == 20 {
		return yield(members[19])
	}
	return yield(members[15]) && yield(members[18])
}
