package core

import (
	"strconv"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Capture the resolution environment before any relation wait. A later path or
// namespace change must not certify old resolution under a newer environment.
func (s *StatementScope) selectPreparedAnalysis(stmt parser.Statement) error {
	if !session.PreparableBody(stmt) {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.statementAuthorizationLocked(stmt)
	if err != nil {
		return err
	}
	s.prepared.path, err = e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return err
	}
	s.prepared.namespace = auth.Catalog.State().NamespaceGeneration()
	s.prepared.private = auth.PendingCatalog()
	s.prepared.epoch = auth.Catalog.Epoch()
	if s.Expected == nil {
		return nil
	}
	if p := s.protocolOrZero().execute; p != nil && s.Expected == &p.Entry.Description && p.Folds != nil {
		a := s.Expected.Analysis
		if a == nil || a.Domain != e.Catalog.Domain || a.Epoch != auth.Catalog.Epoch() || a.Session != auth.Session.SessionToken || p.Transaction != s.TxRef {
			return ids.ErrCatalogReplaced
		}
		// Bind already selected these occurrences. Current lexical observations
		// remain separate admission conflict checks, never a reason to retarget
		// an accepted portal after path changes or DISCARD.
		// The same transaction may lazily create its catalog journal after Bind.
		// Current admission/binding still validates every selected identity; the
		// fold attempt separately fences the journal accepted by that binding.
		s.prepared.reuse = a
		return nil
	}
	valid := s.Expected.Analysis.Valid(auth.Catalog, e.preparedEnvironmentAtPathLocked(auth, s.prepared.path, s.RowSecurity))
	s.prepared.invalid = !valid || s.Expected.PlanOwner == s.Op.Session.SessionToken && s.Expected.PlanGeneration != s.Op.Session.DiscardPlansGeneration
	if !s.prepared.invalid {
		s.prepared.reuse = s.Expected.Analysis
	}
	return nil
}

// PreparedEnvironmentLocked captures one coherent prepared-runtime input. The
// caller holds Engine.Mu and physical-session admission.
func (e *Engine) PreparedEnvironmentLocked(auth AuthorizationView, rowSecurity bool) (PreparedEnvironment, error) {
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return PreparedEnvironment{}, err
	}
	return e.preparedEnvironmentAtPathLocked(auth, path, rowSecurity), nil
}
func (e *Engine) preparedEnvironmentAtPathLocked(auth AuthorizationView, path sequence.Environment, rowSecurity bool) PreparedEnvironment {
	return PreparedEnvironment{Domain: e.Catalog.Domain, Epoch: auth.Catalog.Epoch(), Session: auth.Session.SessionToken, Private: auth.PendingCatalog(), Path: path, Namespace: auth.Catalog.State().NamespaceGeneration(), Discard: auth.Session.DiscardPlansGeneration, RoleStamp: auth.Stamp(), Principal: auth.SessionRoleState.CurrentUser, RowSecurity: rowSecurity, Sequences: &PayloadOf(auth.Session).Sequences}
}

func (e *Engine) preparedInstalledScalarAuthorityRebindLocked(auth AuthorizationView, expected *Description, path sequence.Environment) bool {
	return expected != nil && expected.PlanGeneration == auth.Session.DiscardPlansGeneration && expected.Analysis.DefinitionsValid(auth.Catalog, e.preparedEnvironmentAtPathLocked(auth, path, false), true)
}

// Caller holds Engine.mu. Only the exact prepared source cell can authorize a
// statement-owned installed-scalar datum import into the genuine current member.
// The returned slice belongs to this attempt; neither imports nor values publish
// until the existing successful reanalysis publication path accepts them.
func (e *Engine) preparedInstalledScalarLiteralTemporalsLocked(auth AuthorizationView, expected *Description, path sequence.Environment) []analyze.TemporalConstant {
	if expected == nil {
		return nil
	}
	original := expected.Temporals
	var candidate []analyze.TemporalConstant
	var imports map[*sqltype.Descriptor]*sqltype.Descriptor
	checked := false
	for i, capture := range original {
		source := capture.Value.Type()
		if source == nil || !preparedLtreeArrayShape(source) && !source.IsHstore() && (!source.IsLquery() && !source.IsLtree() && !source.IsLtxtquery()) && !source.IsEan13() && !source.IsIsbn() && !source.IsIsbn13() && !source.IsIsmn() && !source.IsIsmn13() && !source.IsIssn() && !source.IsIssn13() && !source.IsUpc() {
			continue
		}
		if !checked {
			if expected.PlanGeneration != auth.Session.DiscardPlansGeneration || (!expected.Analysis.Valid(auth.Catalog, e.preparedEnvironmentAtPathLocked(auth, path, false)) && !e.preparedInstalledScalarAuthorityRebindLocked(auth, expected, path)) {
				return original
			}
			checked = true
		}
		if preparedLtreeArrayShape(source) {
			if capture.TypeID != source.ID() {
				return original
			}
			array, scalar, ok := expected.Analysis.currentLtreeArray(auth.Catalog, source)
			if !ok {
				return original
			}
			if source == array.SQLType {
				continue
			}
			if imports == nil {
				imports = make(map[*sqltype.Descriptor]*sqltype.Descriptor)
			}
			imports[source.Element()] = scalar.SQLType
			value, err := typedef.ReownDomainDatum(capture.Value, array.SQLType, nil, imports, nil)
			if err != nil {
				return original
			}
			if candidate == nil {
				candidate = append([]analyze.TemporalConstant(nil), original...)
			}
			candidate[i].Value = value
			continue
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(capture.TypeID)}
		old := expected.Analysis.Types[address]
		if capture.TypeID != source.ID() || old == nil || old.SQLType != source || old.Address() != address {
			return original
		}
		captured := false
		for _, dep := range expected.Analysis.Dependencies {
			if dep.Ref.Address == address && dep.Ref.Domain() == e.Catalog.Domain && dep.Ref.Epoch == auth.Catalog.Epoch() {
				captured = true
				break
			}
		}
		current, ok := auth.Catalog.Type(address)
		if !captured || !ok || !samePreparedInstalledScalar(auth.Catalog, old, current) {
			return original
		}
		if source == current.SQLType {
			continue
		}
		if imports == nil {
			imports = make(map[*sqltype.Descriptor]*sqltype.Descriptor)
		}
		imports[source] = current.SQLType
		value, err := typedef.ReownDomainDatum(capture.Value, current.SQLType, nil, imports, nil)
		if err != nil {
			return original
		}
		if candidate == nil {
			candidate = append([]analyze.TemporalConstant(nil), original...)
		}
		candidate[i].Value = value
	}
	if candidate != nil {
		return candidate
	}
	return original
}

// Caller holds Engine.mu and is building a genuinely invalidated analysis
// candidate. An absent original type cannot authorize a datum import. Retire
// only its authentic statement-owned capture, so ordinary binding can parse
// the original SQL literal for its new usage. Identity-only changes never
// enter this path while their original type address still exists. Namespace/path
// evolution and sticky cast invalidation may require fresh literal analysis;
// they cannot authorize importing the retired datum or reusing its analysis.
func (e *Engine) retirePreparedInstalledScalarLiteralTemporalsLocked(auth AuthorizationView, expected *Description, path sequence.Environment, captures []analyze.TemporalConstant) []analyze.TemporalConstant {
	if expected == nil || expected.Analysis == nil || expected.PlanGeneration != auth.Session.DiscardPlansGeneration {
		return captures
	}
	analysis := expected.Analysis
	environment := e.preparedEnvironmentAtPathLocked(auth, path, false)
	sameEnvironment := analysis.EnvironmentValid(environment)
	selectedRetirement := analysis.Domain == environment.Domain && analysis.Epoch == environment.Epoch && analysis.Session == environment.Session && analysis.Private == environment.Private && (analysis.Namespace != environment.Namespace || !sequence.SameEnvironment(analysis.Path, environment.Path))
	if !sameEnvironment && !selectedRetirement {
		return captures
	}
	var candidate []analyze.TemporalConstant
	for i, capture := range captures {
		retired := false
		source := capture.Value.Type()
		if sameEnvironment && preparedLtreeArrayShape(source) && source.ID() == capture.TypeID {
			array, _, ok := expected.Analysis.capturedLtreeArray(auth.Catalog, source)
			retired = ok && auth.Catalog.Get(array.Address()) == nil
		} else if source != nil && (source.IsLquery() || source.IsLtree() || sameEnvironment && (source.IsHstore() || source.IsLtxtquery())) && source.ID() == capture.TypeID {
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(capture.TypeID)}
			old := expected.Analysis.Types[address]
			if old != nil && old.SQLType == source && old.Address() == address && auth.Catalog.Get(address) == nil {
				for _, dep := range expected.Analysis.Dependencies {
					if dep.Ref.Address == address && dep.Ref.Domain() == e.Catalog.Domain && dep.Ref.Epoch == auth.Catalog.Epoch() {
						retired = true
						break
					}
				}
			}
		}
		if retired {
			if candidate == nil {
				candidate = make([]analyze.TemporalConstant, i, len(captures))
				copy(candidate, captures[:i])
			}
		} else if candidate != nil {
			candidate = append(candidate, capture)
		}
	}
	if candidate != nil {
		return candidate
	}
	return captures
}

// Build only on initial or invalidated analysis. Existing binder dependencies
// cover analyzed types/columns; view traversal supplies recursively rewritten
// sources and analysis-only dependencies without a second AST visitor.
func (e *Engine) preparedAnalysisLocked(s *StatementScope, auth AuthorizationView, b analyze.Binding) (*Analysis, error) {
	target := s.LockedRef
	if b.ViewWrite != nil {
		target = view.ViewWriteOf(b.ViewWrite).Logical.Ref
	}
	input := AnalysisInput{Target: target, Binding: b, NeedsReanalysis: s.prepared.changedDuringAdmission}
	if len(s.mutations) != 0 {
		input.Targets = make(map[parser.Statement]ids.ObjectRef, len(s.QueryLockAdmission.MutationTargets))
		input.TargetOrder = make([]ids.ObjectRef, 0, len(s.mutations))
		for i := range s.mutations {
			mutation := s.mutations[i]
			if i == s.ActiveMutation {
				mutation = s.StatementMutation
			}
			ref := mutation.LockedRef
			if mutation.ViewWrite != nil {
				ref = mutation.ViewWrite.Logical.Ref
			}
			if ref.Domain() != nil {
				input.Targets[mutation.Statement] = ref
				input.TargetOrder = append(input.TargetOrder, ref)
			}
		}
	}
	if len(s.QueryLockAdmission.TableQueries) != 0 {
		input.Sources = make(map[*parser.TableReference]analyze.PreparedSource, len(s.QueryLockAdmission.TableQueries))
	}
	for _, leaf := range b.QuerySources {
		if _, admitted := s.QueryLockAdmission.TableQueries[leaf.Table]; admitted {
			input.Sources[leaf.Table] = analyze.PreparedSource{Query: s.QueryLockAdmission.RetainedQuery(leaf.Table), Ref: leaf.Ref}
		}
	}
	input.AllSources = b.QuerySources
	env := e.preparedEnvironmentAtPathLocked(auth, s.prepared.path, s.RowSecurity)
	env.Epoch, env.Private, env.Namespace = s.prepared.epoch, s.prepared.private, s.prepared.namespace
	return CollectAnalysis(auth.Catalog, env, input)
}

// Admission selects an exact identity before waiting. Analysis checks current
// namespace authority on that identity, not whether its spelling now shadows it.
func (e *Engine) admittedReference(auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error) {
	if ref.Domain() == nil {
		return e.ResolveReference(auth, name)
	}
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok || ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() {
		return ids.ObjectRef{}, ids.ErrCatalogReplaced
	}
	ns := e.Catalog.Domain.Ref(ref.Epoch, d.Namespace())
	if err := auth.Check(access.Requirement{Object: ns, Privileges: authorization.UsagePrivilege}); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && len(name.Parts) != 0 && name.Parts[0].Pos.Character > 0 {
			copy := *diagnostic
			copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
			err = &copy
		}
		return ids.ObjectRef{}, err
	}
	if auth.Session != nil {
		if namespace, ok := auth.Catalog.Namespace(d.Namespace()); ok && namespace.Temporary == auth.Session.SessionToken {
			auth.Session.Transactions.AccessedTemporary = true
		}
	}
	return ref, nil
}

// An unchanged prepared identity may outlive its original spelling (for example
// a moved relation). Missing spelling is an observation, not a chosen resource.
func (e *Engine) AdmissionNameReference(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	ref, err := e.ResolveReference(auth, name)
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42P01" {
		return ids.ObjectRef{}, nil
	}
	return ref, err
}

// Recheck names only after an admission wait. Stable preexisting shadows are
// allowed, but changed resolution during that wait preserves the native conflict
// fence for every route. This observation never replaces the granted identity.
func (s *StatementScope) ValidateAdmissionNamesLocked(e *Engine, auth AuthorizationView) error {
	if !s.admissionWaited {
		return nil
	}
	check := func(name parser.ObjectName, before ids.ObjectRef) error {
		after, err := e.AdmissionNameReference(auth, name)
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
			if i == s.ActiveMutation {
				mutation = s.StatementMutation
			}
			if len(mutation.AdmittedTargetName.Parts) != 0 {
				if err := check(mutation.AdmittedTargetName, mutation.AdmittedTargetLexical); err != nil {
					return err
				}
			}
		}
	}
	if len(s.mutations) == 0 && len(s.AdmittedTargetName.Parts) != 0 {
		if err := check(s.AdmittedTargetName, s.AdmittedTargetLexical); err != nil {
			return err
		}
	}
	for table, source := range s.QueryLockAdmission.TableQueries {
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
	reuse                  *Analysis
	invalid                bool
	path                   sequence.Environment
	namespace              uint64
	private                *session.Journal
	epoch                  ids.CatalogEpoch
	changedDuringAdmission bool
}
