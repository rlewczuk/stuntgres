package core

import (
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// AnalysisInput carries the actual admitted occurrence identities and binder
// manifest. Core owns dependency closure, effective stamps and purpose union.
type AnalysisInput struct {
 Target ids.ObjectRef
 Targets map[parser.Statement]ids.ObjectRef
 TargetOrder []ids.ObjectRef
 Sources map[*parser.TableReference]analyze.PreparedSource
 AllSources []exec.Leaf
 Binding analyze.Binding
 NeedsReanalysis bool
}
func CollectAnalysis(read catalog.Read, env PreparedEnvironment, input AnalysisInput) (*Analysis,error) {
 cell := &Analysis{Domain:env.Domain, Epoch:env.Epoch, Session:env.Session, Private:env.Private, Path:env.Path, Namespace:env.Namespace, Target:input.Target, Targets:input.Targets, Sources:input.Sources, Casts:input.Binding.Prepared.Casts, NeedsReanalysis:input.NeedsReanalysis}
	type dependencyKey struct {
		ref     ids.ObjectRef
		purpose catalog.DependencyPurpose
	}
	seen := make(map[dependencyKey]int)
	var visitPurpose func(ids.ObjectRef, catalog.DependencyPurpose) error
	visit := func(ref ids.ObjectRef) error { return visitPurpose(ref, catalog.DefinitionDependency) }
	visitPurpose = func(ref ids.ObjectRef, purpose catalog.DependencyPurpose) error {
		if ref.Domain() == nil {
			return nil
		}
		ref.Address.SubID = 0
		key := dependencyKey{ref, purpose}
		if _, present := seen[key]; present {
			return nil
		}
		// Written use subsumes an operand, never an independent support capture.
		operandKey := dependencyKey{ref, catalog.OperandDependency}
		index, present := seen[operandKey]
		if purpose == catalog.OperandDependency {
			if _, executable := seen[dependencyKey{ref, catalog.DefinitionDependency}]; executable {
				return nil
			}
		}
		present = present && purpose == catalog.DefinitionDependency
		if present {
			cell.Dependencies[index].Purpose = purpose
			delete(seen, operandKey)
			seen[key] = index
		}
		d := read.Get(ref.Address)
		if d == nil || ref.Domain() != env.Domain || ref.Epoch != read.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if !present {
			seen[key] = len(cell.Dependencies)
			cell.Dependencies = append(cell.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: DefinitionStamp(d, env), Purpose: purpose})
		}
		if callable, ok := d.(*catalog.Procedure); ok {
			if cell.FoldCallables == nil {
				cell.FoldCallables = make(map[ids.ObjectAddress]*catalog.Procedure)
			}
			cell.FoldCallables[ref.Address] = callable
		}
		if typ, ok := d.(*catalog.Type); ok {
			if cell.FoldTypes == nil {
				cell.FoldTypes = make(map[ids.ObjectAddress]*sqltype.Descriptor)
			}
			cell.FoldTypes[ref.Address] = typ.SQLType
			if !typ.UsesDomainPlanStamp() && (typ.SQLType.IsUserDefined() || typ.SQLType.IsArray()) {
				if cell.Types == nil {
					cell.Types = make(map[ids.ObjectAddress]*catalog.Type)
				}
				cell.Types[ref.Address] = typ
			}
			// Preserve the complete type closure and its demand purpose. Record
			// children in this cell rather than inventing a generic type stamp.
			visitType := func(id sqltype.ID) error {
				return visitPurpose(env.Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}), purpose)
			}
			switch {
			case typ.SQLType.IsDomain():
				if err := visitType(typ.SQLType.DomainBase().Type.ID()); err != nil {
					return err
				}
			case typ.SQLType.IsArray():
				if err := visitType(typ.SQLType.Element().ID()); err != nil {
					return err
				}
			case typ.SQLType.IsComposite():
				for i := range typ.SQLType.CompositeLen() {
					if err := visitType(typ.SQLType.CompositeMember(i).Usage.Type.ID()); err != nil {
						return err
					}
				}
			}
		}
		if relation, ok := d.(*catalog.Relation); ok && relation.TableMethod != 0 {
			if err := catalog.ValidateRelationTableMethod(read, relation); err != nil { return err }
			method, _ := read.AccessMethod(relation.TableMethod)
			for _, a := range [...]ids.ObjectAddress{{ClassID: ids.AccessMethodClassID, ObjectID: relation.TableMethod}, {ClassID: ids.ProcedureClassID, ObjectID: method.Handler}} {
				if err := visitPurpose(env.Domain.Ref(read.Epoch(), a), purpose); err != nil { return err }
			}
		}
		if relation, ok := d.(*catalog.Relation); ok && purpose == catalog.DefinitionDependency && relation.Security.RLSEnabled {
			cell.FoldRoleSensitive = true
		}
		if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.ViewRelation && relation.View != nil {
			for _, leaf := range stored.ViewOf(relation).Sources() {
				child, err := stored.ViewUseReference(read, relation, leaf.Ref)
				if err != nil {
					return err
				}
				if err := visitPurpose(child, purpose); err != nil {
					return err
				}
			}
			for _, dep := range stored.ViewOf(relation).Dependencies() {
				child, err := stored.ViewUseReference(read, relation, dep.Ref)
				if err != nil {
					return err
				}
				if err := visitPurpose(child, dep.Purpose); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(input.Target); err != nil {
		return nil, err
	}
 for _, ref := range input.TargetOrder { if err := visit(ref); err != nil { return nil, err } }
 for _, source := range input.AllSources { if err := visit(source.Ref); err != nil { return nil, err } }
	for _, dep := range input.Binding.Dependencies {
		if err := visitPurpose(dep.Ref, dep.Purpose); err != nil {
			return nil, err
		}
	}
	// A domain conversion can be elided from the executable expression, but
	// its resolved cast occurrence still belongs to this analysis lifetime.
	for key, captured := range input.Binding.Prepared.Types {
		if key.Kind != analyze.DomainCast && key.Kind != analyze.IdentityCast || captured.Base.Type == nil {
			continue
		}
		ref := env.Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(captured.Base.Type.ID())})
		if err := visit(ref); err != nil {
			return nil, err
		}
		if captured.Base.Type.IsArray() && captured.Base.Type.Element().IsDomain() {
			element := env.Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(captured.Base.Type.Element().ID())})
			if err := visit(element); err != nil {
				return nil, err
			}
		}
	}
	return cell, nil
}
