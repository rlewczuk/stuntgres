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
// dblink_get_pkey reads detached catalog metadata, not a tuple source. Its text
// argument deliberately creates neither a source capture nor a relation edge.
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
			if ok && procedure.SQL != nil {
				return true, nil
			}
			if ok && extension.TablefuncProviderOf(procedure) == extension.StockTablefunc {
				symbol, err := extension.ProcedureSymbol(read, procedure)
				if err != nil {
					return false, err
				}
				if symbol == "connectby" || symbol == "crosstab" || symbol == "crosstab2" || symbol == "crosstab3" || symbol == "crosstab4" {
					return true, nil
				}
			}
			if ok && extension.XML2ProviderOf(procedure) == extension.StockXML2 {
				symbol, err := extension.ProcedureSymbol(read, procedure)
				if err != nil {
					return false, err
				}
				if symbol == "xpath_table" {
					return true, nil
				}
			}
			if ok && extension.PgsurgeryProviderOf(procedure) == extension.StockPgsurgery {
				return true, nil
			}
			if ok && extension.PgBuffercacheProviderOf(procedure) == extension.StockPgBuffercache && procedure.Origin != nil && procedure.Origin.Declaration >= 1 && int(procedure.Origin.Declaration) <= len(extension.PgBuffercacheEvictionMembers) {
				return true, nil
			}
			if ok && (extension.PgFreespaceProviderOf(procedure) == extension.StockPgFreespace || extension.PgvisibilityProviderOf(procedure) == extension.StockPgvisibility || extension.PgstattupleProviderOf(procedure) == extension.StockPgstattuple || extension.PageinspectProviderOf(procedure) == extension.StockPageinspect || extension.PgrowlocksProviderOf(procedure) == extension.StockPgrowlocks || extension.AmcheckProviderOf(procedure) == extension.StockAmcheck) {
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

// HasDynamicMutationDependency includes the destination-owned DEFAULT/CHECK
// manifests. They deliberately do not join the outer query's routine manifest,
// but their lazy relation reads still need this command's original roots.
func HasDynamicMutationDependency(read catalog.Read, relation *catalog.Relation) (bool, error) {
	if relation == nil {
		return false, nil
	}
	for _, definition := range relation.ColumnMeta.Defaults {
		for _, ref := range definition.Dependencies {
			dependency := [1]catalog.ExpressionDependency{{Ref: ref}}
			if found, err := HasDynamicSourceDependency(read, dependency[:], nil); found || err != nil {
				return found, err
			}
		}
	}
	for _, check := range relation.Constraints.Check {
		if found, err := HasDynamicSourceDependency(read, check.Dependencies, nil); found || err != nil {
			return found, err
		}
	}
	return false, nil
}
