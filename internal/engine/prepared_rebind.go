package engine

import (
	"strconv"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Only immutable analysis identities and definition stamps survive a statement.
// Engine.mu and physical-session admission own this cell; no data or grants do.
type preparedAnalysis struct {
	domain          *ids.Domain
	epoch           ids.CatalogEpoch
	session         ids.SessionToken
	private         *session.Journal
	path            sequence.Environment
	namespace       uint64
	target          ids.ObjectRef
	targets         map[parser.Statement]ids.ObjectRef
	sources         map[*parser.TableReference]analyze.PreparedSource
	dependencies    []catalog.ExpressionDependency
	needsReanalysis bool // A granted definition evolved after this attempt resolved names.
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
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return err
	}
	s.prepared.path, err = e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return err
	}
	s.prepared.namespace = auth.Catalog.State().NamespaceGeneration()
	s.prepared.private = auth.session.Transactions.Catalog
	s.prepared.epoch = auth.Catalog.Epoch()
	if s.Expected == nil {
		return nil
	}
	valid := e.preparedAnalysisValidLocked(auth, s.Expected.analysis, s.prepared.path)
	s.prepared.invalid = !valid || s.Expected.planOwner == s.op.Session.SessionToken && s.Expected.planGeneration != s.op.Session.DiscardPlansGeneration
	if !s.prepared.invalid {
		s.prepared.reuse = s.Expected.analysis
	}
	return nil
}

func (e *Engine) preparedAnalysisValidLocked(auth AuthorizationView, analysis *preparedAnalysis, path sequence.Environment) bool {
	if analysis == nil || analysis.needsReanalysis || analysis.domain != e.catalog.Domain || analysis.epoch != auth.Catalog.Epoch() || analysis.session != auth.session.SessionToken || analysis.private != auth.session.Transactions.Catalog || analysis.namespace != auth.Catalog.State().NamespaceGeneration() || !sequence.SameEnvironment(analysis.path, path) {
		return false
	}
	for _, dep := range analysis.dependencies {
		d := auth.Catalog.Get(dep.Ref.Address)
		if d == nil || dep.Ref.Domain() != e.catalog.Domain || dep.Ref.Epoch != auth.Catalog.Epoch() || preparedDefinitionStamp(d, auth.session) != dep.Revision {
			return false
		}
	}
	return true
}

// Caller holds Engine.mu. Sequence source events are independent of generator
// state and descriptor revisions. Commit exposes the descriptor's source stamp
// to peers; a reverted reset retains its event only in the initiating session.
func preparedDefinitionStamp(d catalog.Descriptor, session *session.Session) ids.DefinitionRevision {
	var memory *sequence.SessionMemory
	if session != nil {
		memory = &payloadOf(session).sequences
	}
	return sequence.DefinitionStamp(d, memory)
}

// Build only on initial or invalidated analysis. Existing binder dependencies
// cover analyzed types/columns; view traversal supplies recursively rewritten
// sources and analysis-only dependencies without a second AST visitor.
func (e *Engine) preparedAnalysisLocked(s *StatementScope, auth AuthorizationView, b analyze.Binding) (*preparedAnalysis, error) {
	target := s.lockedRef
	if b.ViewWrite != nil {
		target = viewWriteOf(b.ViewWrite).logical.Ref
	}
	cell := &preparedAnalysis{domain: e.catalog.Domain, epoch: s.prepared.epoch, session: s.op.Session.SessionToken, private: s.prepared.private, path: s.prepared.path, namespace: s.prepared.namespace, target: target, needsReanalysis: s.prepared.changedDuringAdmission}
	seen := make(map[ids.ObjectRef]bool)
	var visit func(ids.ObjectRef) error
	visit = func(ref ids.ObjectRef) error {
		if ref.Domain() == nil {
			return nil
		}
		ref.Address.SubID = 0
		if seen[ref] {
			return nil
		}
		seen[ref] = true
		d := auth.Catalog.Get(ref.Address)
		if d == nil || ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		cell.dependencies = append(cell.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: preparedDefinitionStamp(d, auth.session)})
		if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.ViewRelation && relation.View != nil {
			for _, leaf := range stored.ViewOf(relation).Sources() {
				child, err := stored.ViewUseReference(auth.Catalog, relation, leaf.Ref)
				if err != nil {
					return err
				}
				if err := visit(child); err != nil {
					return err
				}
			}
			for _, dep := range stored.ViewOf(relation).Dependencies() {
				child, err := stored.ViewUseReference(auth.Catalog, relation, dep.Ref)
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
	if len(s.mutations) != 0 {
		cell.targets = make(map[parser.Statement]ids.ObjectRef, len(s.queryLockAdmission.MutationTargets))
		for i := range s.mutations {
			mutation := s.mutations[i]
			if i == s.activeMutation {
				mutation = s.statementMutation
			}
			ref := mutation.lockedRef
			if mutation.viewWrite != nil {
				ref = mutation.viewWrite.logical.Ref
			}
			if ref.Domain() == nil {
				continue
			}
			cell.targets[mutation.statement] = ref
			if err := visit(ref); err != nil {
				return nil, err
			}
		}
	}
	if len(s.queryLockAdmission.TableQueries) != 0 {
		cell.sources = make(map[*parser.TableReference]analyze.PreparedSource, len(s.queryLockAdmission.TableQueries))
	}
	for _, leaf := range b.QuerySources {
		if _, admitted := s.queryLockAdmission.TableQueries[leaf.Table]; admitted {
			cell.sources[leaf.Table] = analyze.PreparedSource{Query: s.queryLockAdmission.RetainedQuery(leaf.Table), Ref: leaf.Ref}
		}
		if err := visit(leaf.Ref); err != nil {
			return nil, err
		}
	}
	for _, dep := range b.Dependencies {
		if err := visit(dep.Ref); err != nil {
			return nil, err
		}
	}
	return cell, nil
}

// Admission selects an exact identity before waiting. Analysis checks current
// namespace authority on that identity, not whether its spelling now shadows it.
func (e *Engine) admittedReference(auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error) {
	if ref.Domain() == nil {
		return e.resolveReference(auth, name)
	}
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok || ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() {
		return ids.ObjectRef{}, ids.ErrCatalogReplaced
	}
	ns := e.catalog.Domain.Ref(ref.Epoch, d.Namespace())
	if err := auth.Check(access.Requirement{Object: ns, Privileges: authorization.UsagePrivilege}); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && len(name.Parts) != 0 && name.Parts[0].Pos.Character > 0 {
			copy := *diagnostic
			copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
			err = &copy
		}
		return ids.ObjectRef{}, err
	}
	if auth.session != nil {
		if namespace, ok := auth.Catalog.Namespace(d.Namespace()); ok && namespace.Temporary == auth.session.SessionToken {
			auth.session.Transactions.AccessedTemporary = true
		}
	}
	return ref, nil
}

// An unchanged prepared identity may outlive its original spelling (for example
// a moved relation). Missing spelling is an observation, not a chosen resource.
func (e *Engine) admissionNameReference(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	ref, err := e.resolveReference(auth, name)
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42P01" {
		return ids.ObjectRef{}, nil
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
	check := func(name parser.ObjectName, before ids.ObjectRef) error {
		after, err := e.admissionNameReference(auth, name)
		if err != nil {
			return err
		}
		if after != before {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	if len(s.mutations) != 0 {
		for i := range s.mutations {
			mutation := s.mutations[i]
			if i == s.activeMutation {
				mutation = s.statementMutation
			}
			if len(mutation.admittedTargetName.Parts) != 0 {
				if err := check(mutation.admittedTargetName, mutation.admittedTargetLexical); err != nil {
					return err
				}
			}
		}
	}
	if len(s.mutations) == 0 && len(s.admittedTargetName.Parts) != 0 {
		if err := check(s.admittedTargetName, s.admittedTargetLexical); err != nil {
			return err
		}
	}
	for table, source := range s.queryLockAdmission.TableQueries {
		if source.Ref.Domain() != nil {
			if err := check(table.Name, source.Lexical); err != nil {
				return err
			}
		}
	}
	return nil
}

// preparedRebind is the statement's prepared-analysis rebind state: the reused
// analysis and the resolution environment captured before any relation wait.
// Every SELECT/DML statement and SQL routine body writes it, so it is a value
// group rather than a pointer slot.
type preparedRebind struct {
	reuse                  *preparedAnalysis
	invalid                bool
	path                   sequence.Environment
	namespace              uint64
	private                *session.Journal
	epoch                  ids.CatalogEpoch
	changedDuringAdmission bool
}
