package view

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// SourceRequirementLeaf resolves a stored occurrence's actual read attributes
// and effective row mark. It borrows definition metadata, never rows or grants.
func SourceRequirementLeaf(a access.View, owner *catalog.Relation, occurrence int, inherited plan.RowMark) (exec.Leaf, error) {
	definition := stored.ViewOf(owner)
	leaf := definition.Sources()[occurrence]
	relation, ok := a.Catalog.Relation(leaf.Ref.Address)
	if !ok || !stored.ViewReferenceValid(a.Catalog, owner, leaf.Ref) {
		return exec.Leaf{}, ids.ErrCatalogReplaced
	}
	use, err := stored.ViewUseReference(a.Catalog, owner, leaf.Ref)
	if err != nil {
		return exec.Leaf{}, err
	}
	source := exec.Leaf{Ref: use, Relation: relation, Only: leaf.Only, AnalysisOnly: leaf.AnalysisOnly, SystemAttributeRead: leaf.SystemAttributeRead, Mark: stored.ViewLeafRowMark(definition, occurrence, inherited), Uses: make([]int, 0, len(leaf.Reads))}
	for _, attribute := range leaf.Reads {
		slot := relation.IndexForAttribute(attribute)
		if slot < 0 {
			return exec.Leaf{}, fmt.Errorf("stuntgres: missing view source attribute")
		}
		source.Uses = append(source.Uses, slot)
	}
	return source, nil
}

// NeedsRowMarkValidation is the existing source gate, independent of whether
// the source is executable. Once admitted, validation visits every stored edge.
func NeedsRowMarkValidation(read catalog.Read, relation *catalog.Relation, mark plan.RowMark) bool {
	return mark.Mode != 0 || stored.ViewHasRowMarks(relation, read)
}

// DefinitionReadRequirement is the column request made by mark validation,
// including analysis-only edges. It is not the executable tableoid read path.
func DefinitionReadRequirement(ref ids.ObjectRef, relation *catalog.Relation, columns []int) analyze.Requirement {
	return analyze.Requirement{Ref: ref, Relation: relation, Privilege: authorization.SelectPrivilege, Columns: columns}
}

// WriteTargetRequirement is shared by view authorization and its detached
// presentation. DELETE is relation-wide; INSERT/UPDATE retain accepted targets.
func WriteTargetRequirement(stmt parser.Statement, layer WriteLayer, source *catalog.Relation) analyze.Requirement {
	request := analyze.Requirement{Ref: layer.SourceRef, Relation: source, Columns: layer.Targets, Privilege: authorization.UpdatePrivilege}
	switch stmt.(type) {
	case *parser.Insert:
		request.Privilege = authorization.InsertPrivilege
	case *parser.Delete:
		request.Privilege, request.WholeRelation = authorization.DeletePrivilege, true
	}
	return request
}

// WriteSourceRequirementLeaf retains independent stored occurrences, including
// another occurrence of the same table. The next write layer owns expansion
// and row marks for the main source; only actual command reads demand SELECT.
func WriteSourceRequirementLeaf(a access.View, b analyze.Binding, owner *catalog.Relation, layer WriteLayer, occurrence int) (exec.Leaf, bool, error) {
	logical := ViewWriteOf(b.ViewWrite).Logical
	reads := b.Returning != nil || len(logical.UsedColumns) != 0 || logical.Insert.Conflict != nil && len(logical.Insert.Conflict.Reads) != 0
	if occurrence == layer.MainOccurrence && !reads {
		return exec.Leaf{}, false, nil
	}
	source, err := SourceRequirementLeaf(a, owner, occurrence, plan.RowMark{})
	if err != nil {
		return exec.Leaf{}, false, err
	}
	// CheckWrite has always checked each included definition occurrence.
	source.AnalysisOnly = false
	if occurrence == layer.MainOccurrence {
		source.Mark = plan.RowMark{}
	}
	return source, true, nil
}
