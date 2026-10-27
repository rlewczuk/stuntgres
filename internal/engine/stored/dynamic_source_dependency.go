package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// HasDynamicSourceDependency reports installed runtime calls, directly or
// through analyzed stored-view dependencies.
// read is the statement's private catalog.
//
// Stored calls keep their own lifetime/ACL manifests rather than joining the
// outer binder's routineDependencies. Consult those analyzed manifests before
// the first capture, including views opened only later.
func HasDynamicSourceDependency(read catalog.Read, dependencies []catalog.ExpressionDependency, leaves []exec.Leaf) (bool, error) {
	hasCall := func(dependencies []catalog.ExpressionDependency, consumer *catalog.Relation) (bool, error) {
		for _, dependency := range dependencies {
			if dependency.Ref.Address.ClassID != ids.ProcedureClassID {
				continue
			}
			ref := dependency.Ref
			if consumer != nil {
				var err error
				ref, err = ViewUseReference(read, consumer, ref)
				if err != nil {
					return false, err
				}
			}
			procedure, ok := read.Procedure(ref.Address)
			if ok && extension.TablefuncProviderOf(procedure) == extension.StockTablefunc {
				symbol, err := extension.ProcedureSymbol(read, procedure)
				if err != nil {
					return false, err
				}
				if symbol == "connectby" {
					return true, nil
				}
			}
			if ok && (extension.PageinspectProviderOf(procedure) == extension.StockPageinspect || extension.PgrowlocksProviderOf(procedure) == extension.StockPgrowlocks || extension.AmcheckProviderOf(procedure) == extension.StockAmcheck) {
				return true, nil
			}
		}
		return false, nil
	}
	if found, err := hasCall(dependencies, nil); found || err != nil {
		return found, err
	}
	var visited map[*catalog.Relation]bool
	var visit func(*catalog.Relation) (bool, error)
	visit = func(relation *catalog.Relation) (bool, error) {
		if relation == nil || relation.RelationKind != catalog.ViewRelation || visited[relation] {
			return false, nil
		}
		definition := ViewOf(relation)
		if definition == nil {
			return false, nil
		}
		if found, err := hasCall(definition.Dependencies(), relation); found || err != nil {
			return found, err
		}
		if visited == nil {
			visited = make(map[*catalog.Relation]bool)
		}
		visited[relation] = true
		for _, source := range definition.Sources() {
			if source.AnalysisOnly {
				continue
			}
			ref, err := ViewUseReference(read, relation, source.Ref)
			if err != nil {
				return false, err
			}
			child, _ := read.Relation(ref.Address)
			if found, err := visit(child); found || err != nil {
				return found, err
			}
		}
		return false, nil
	}
	for _, leaf := range leaves {
		if !leaf.AnalysisOnly {
			if found, err := visit(leaf.Relation); found || err != nil {
				return found, err
			}
		}
	}
	return false, nil
}
