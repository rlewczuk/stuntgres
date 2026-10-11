package engine

import (
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Only immutable analysis identities and definition stamps survive a statement.
// Engine.mu and physical-session admission own this cell; no data or grants do.
type preparedAnalysis struct {
	owner           *catalogOwner
	epoch           CatalogEpoch
	session         SessionToken
	private         *catalogChanges
	path            sequenceEnvironment
	namespace       uint64
	target          ObjectRef
	sources         map[*parser.TableReference]preparedSource
	dependencies    []expressionDependency
	needsReanalysis bool // A granted definition evolved after this attempt resolved names.
}
type preparedSource struct {
	query   *parser.Select
	ref     ObjectRef
	lexical ObjectRef // Statement-local name observation, never a retained lock choice.
}

func preparableBody(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}

// Capture the resolution environment before any relation wait. A later path or
// namespace change must not certify old resolution under a newer environment.
func (s *StatementScope) selectPreparedAnalysis(stmt parser.Statement) error {
	if !preparableBody(stmt) {
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return err
	}
	s.preparedPath, err = e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return err
	}
	s.preparedNamespace = auth.catalog.state.namespaceGeneration
	s.preparedPrivate = auth.session.transactions.catalog
	s.preparedEpoch = auth.catalog.Epoch()
	if s.Expected == nil {
		return nil
	}
	valid := e.preparedAnalysisValidLocked(auth, s.Expected.analysis, s.preparedPath)
	s.preparedInvalid = !valid || s.Expected.planOwner == s.op.session.token && s.Expected.planGeneration != s.op.session.discardPlansGeneration
	if !s.preparedInvalid {
		s.preparedReuse = s.Expected.analysis
	}
	return nil
}

func (e *Engine) preparedAnalysisValidLocked(auth AuthorizationView, analysis *preparedAnalysis, path sequenceEnvironment) bool {
	if analysis == nil || analysis.needsReanalysis || analysis.owner != e.owner || analysis.epoch != auth.catalog.Epoch() || analysis.session != auth.session.token || analysis.private != auth.session.transactions.catalog || analysis.namespace != auth.catalog.state.namespaceGeneration || !sameSequenceEnvironment(analysis.path, path) {
		return false
	}
	for _, dep := range analysis.dependencies {
		d := auth.catalog.state.descriptors[dep.ref.Address]
		if d == nil || dep.ref.owner != e.owner || dep.ref.Epoch != auth.catalog.Epoch() || preparedDefinitionStamp(d) != dep.revision {
			return false
		}
	}
	return true
}

func preparedDefinitionStamp(d Descriptor) DefinitionRevision {
	if relation, ok := d.(*relationDescriptor); ok && relation.relationKind == sequenceRelation {
		return relation.sequencePlanStamp
	}
	return d.Revision()
}

// Build only on initial or invalidated analysis. Existing binder dependencies
// cover analyzed types/columns; view traversal supplies recursively rewritten
// sources and analysis-only dependencies without a second AST visitor.
func (e *Engine) preparedAnalysisLocked(s *StatementScope, auth AuthorizationView, b binding) (*preparedAnalysis, error) {
	target := s.lockedRef
	if b.viewWrite != nil {
		target = b.viewWrite.logical.ref
	}
	cell := &preparedAnalysis{owner: e.owner, epoch: s.preparedEpoch, session: s.op.session.token, private: s.preparedPrivate, path: s.preparedPath, namespace: s.preparedNamespace, target: target, needsReanalysis: s.preparedChangedDuringAdmission}
	seen := make(map[ObjectRef]bool)
	var visit func(ObjectRef) error
	visit = func(ref ObjectRef) error {
		if ref.owner == nil {
			return nil
		}
		ref.Address.SubID = 0
		if seen[ref] {
			return nil
		}
		seen[ref] = true
		d := auth.catalog.state.descriptors[ref.Address]
		if d == nil || ref.owner != e.owner || ref.Epoch != auth.catalog.Epoch() {
			return ErrCatalogReplaced
		}
		cell.dependencies = append(cell.dependencies, expressionDependency{ref: ref, revision: preparedDefinitionStamp(d)})
		if relation, ok := d.(*relationDescriptor); ok && relation.relationKind == viewRelation && relation.view != nil {
			for _, leaf := range relation.view.sources {
				child, err := storedViewUseReference(auth.catalog, relation, leaf.ref)
				if err != nil {
					return err
				}
				if err := visit(child); err != nil {
					return err
				}
			}
			for _, dep := range relation.view.dependencies {
				child, err := storedViewUseReference(auth.catalog, relation, dep.ref)
				if err != nil {
					return err
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(target); err != nil {
		return nil, err
	}
	if len(s.queryLockAdmission.tableQueries) != 0 {
		cell.sources = make(map[*parser.TableReference]preparedSource, len(s.queryLockAdmission.tableQueries))
	}
	for _, leaf := range b.querySources {
		if _, admitted := s.queryLockAdmission.tableQueries[leaf.table]; admitted {
			cell.sources[leaf.table] = preparedSource{query: s.queryLockAdmission.retainedQuery(leaf.table), ref: leaf.ref}
		}
		if err := visit(leaf.ref); err != nil {
			return nil, err
		}
	}
	for _, dep := range b.dependencies {
		if err := visit(dep.ref); err != nil {
			return nil, err
		}
	}
	return cell, nil
}

// Admission selects an exact identity before waiting. Analysis checks current
// namespace authority on that identity, not whether its spelling now shadows it.
func (e *Engine) admittedReference(auth AuthorizationView, name parser.ObjectName, ref ObjectRef) (ObjectRef, error) {
	if ref.owner == nil {
		return e.resolveReference(auth, name)
	}
	d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.owner != e.owner || ref.Epoch != auth.catalog.Epoch() {
		return ObjectRef{}, ErrCatalogReplaced
	}
	ns := ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: d.namespace}
	if err := auth.Check(AccessRequirement{Object: ns, Privileges: UsagePrivilege}); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && len(name.Parts) != 0 && name.Parts[0].Pos.Character > 0 {
			copy := *diagnostic
			copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
			err = &copy
		}
		return ObjectRef{}, err
	}
	if auth.session != nil {
		if namespace, ok := auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor); ok && namespace.temporary == auth.session.token {
			auth.session.transactions.accessedTemporary = true
		}
	}
	return ref, nil
}

// An unchanged prepared identity may outlive its original spelling (for example
// a moved relation). Missing spelling is an observation, not a chosen resource.
func (e *Engine) admissionNameReference(auth AuthorizationView, name parser.ObjectName) (ObjectRef, error) {
	ref, err := e.resolveReference(auth, name)
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42P01" {
		return ObjectRef{}, nil
	}
	return ref, err
}

// Recheck names only after an admission wait. Stable preexisting shadows are
// allowed, but changed resolution during that wait preserves the native conflict
// fence for every route. This observation never replaces the granted identity.
func (s *StatementScope) validateAdmissionNamesLocked(e *Engine, auth AuthorizationView) error {
	if !s.admissionWaited {
		return nil
	}
	check := func(name parser.ObjectName, before ObjectRef) error {
		after, err := e.admissionNameReference(auth, name)
		if err != nil {
			return err
		}
		if after != before {
			return ErrCatalogReplaced
		}
		return nil
	}
	if len(s.admittedTargetName.Parts) != 0 {
		if err := check(s.admittedTargetName, s.admittedTargetLexical); err != nil {
			return err
		}
	}
	for table, source := range s.queryLockAdmission.tableQueries {
		if source.ref.owner != nil {
			if err := check(table.Name, source.lexical); err != nil {
				return err
			}
		}
	}
	return nil
}
