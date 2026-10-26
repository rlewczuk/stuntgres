package core

import (
	"strconv"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
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
	types           map[ids.ObjectAddress]*catalog.Type
	casts           map[catalog.CastPair]*catalog.Cast
	needsReanalysis bool // A granted definition evolved after this attempt resolved names.
}

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
	valid := e.preparedAnalysisValidLocked(auth, s.Expected.analysis, s.prepared.path)
	s.prepared.invalid = !valid || s.Expected.planOwner == s.Op.Session.SessionToken && s.Expected.planGeneration != s.Op.Session.DiscardPlansGeneration
	if !s.prepared.invalid {
		s.prepared.reuse = s.Expected.analysis
	}
	return nil
}

func (e *Engine) preparedAnalysisEnvironmentValidLocked(auth AuthorizationView, analysis *preparedAnalysis, path sequence.Environment) bool {
	return analysis != nil && !analysis.needsReanalysis && analysis.domain == e.Catalog.Domain && analysis.epoch == auth.Catalog.Epoch() && analysis.session == auth.Session.SessionToken && analysis.private == auth.PendingCatalog() && analysis.namespace == auth.Catalog.State().NamespaceGeneration() && sequence.SameEnvironment(analysis.path, path)
}

func (e *Engine) preparedAnalysisValidLocked(auth AuthorizationView, analysis *preparedAnalysis, path sequence.Environment) bool {
	return e.preparedAnalysisDefinitionsValidLocked(auth, analysis, path, false)
}

func (e *Engine) preparedInstalledScalarAuthorityRebindLocked(auth AuthorizationView, expected *StatementDescription, path sequence.Environment) bool {
	return expected != nil && expected.planGeneration == auth.Session.DiscardPlansGeneration && e.preparedAnalysisDefinitionsValidLocked(auth, expected.analysis, path, true)
}

// Caller holds Engine.mu. Only the exact prepared source cell can authorize a
// statement-owned installed-scalar datum import into the genuine current member.
// The returned slice belongs to this attempt; neither imports nor values publish
// until the existing successful reanalysis publication path accepts them.
func (e *Engine) preparedInstalledScalarLiteralTemporalsLocked(auth AuthorizationView, expected *StatementDescription, path sequence.Environment) []analyze.TemporalConstant {
	if expected == nil {
		return nil
	}
	original := expected.Temporals
	var candidate []analyze.TemporalConstant
	var imports map[*sqltype.Descriptor]*sqltype.Descriptor
	checked := false
	for i, capture := range original {
		source := capture.Value.Type()
		if source == nil || !source.IsHstore() && (!source.IsLquery() && !source.IsLtree()) && !source.IsEan13() && !source.IsIsbn() && !source.IsIsbn13() && !source.IsIsmn() && !source.IsIsmn13() && !source.IsIssn() && !source.IsIssn13() && !source.IsUpc() {
			continue
		}
		if !checked {
			if expected.planGeneration != auth.Session.DiscardPlansGeneration || (!e.preparedAnalysisValidLocked(auth, expected.analysis, path) && !e.preparedInstalledScalarAuthorityRebindLocked(auth, expected, path)) {
				return original
			}
			checked = true
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(capture.TypeID)}
		old := expected.analysis.types[address]
		if capture.TypeID != source.ID() || old == nil || old.SQLType != source || old.Address() != address {
			return original
		}
		captured := false
		for _, dep := range expected.analysis.dependencies {
			if dep.Ref.Address == address && dep.Ref.Domain() == e.Catalog.Domain && dep.Ref.Epoch == auth.Catalog.Epoch() {
				captured = true
				break
			}
		}
		current, ok := auth.Catalog.Type(address)
		if !captured || !ok || source.IsHstore() != current.SQLType.IsHstore() || source.IsLquery() != current.SQLType.IsLquery() || source.IsLtree() != current.SQLType.IsLtree() || source.IsEan13() != current.SQLType.IsEan13() || source.IsIsbn() != current.SQLType.IsIsbn() || source.IsIsbn13() != current.SQLType.IsIsbn13() || source.IsIsmn() != current.SQLType.IsIsmn() || source.IsIsmn13() != current.SQLType.IsIsmn13() || source.IsIssn() != current.SQLType.IsIssn() || source.IsIssn13() != current.SQLType.IsIssn13() || source.IsUpc() != current.SQLType.IsUpc() || old.Extension != current.Extension || old.Array != current.Array {
			return original
		}
		nominal := *old
		nominal.Owner, nominal.ACL = current.Owner, current.ACL
		if !catalog.SamePreparedTypeDefinition(&nominal, current) {
			return original
		}
		ext, ok := auth.Catalog.Extension(current.Extension)
		if !ok {
			return original
		}
		if _, err := extension.MemberTypeRole(auth.Catalog, ext, current); err != nil {
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
// enter this path while their original type address still exists.
func (e *Engine) retirePreparedInstalledScalarLiteralTemporalsLocked(auth AuthorizationView, expected *StatementDescription, path sequence.Environment, captures []analyze.TemporalConstant) []analyze.TemporalConstant {
	if expected == nil || expected.planGeneration != auth.Session.DiscardPlansGeneration || !e.preparedAnalysisEnvironmentValidLocked(auth, expected.analysis, path) {
		return captures
	}
	var candidate []analyze.TemporalConstant
	for i, capture := range captures {
		retired := false
		source := capture.Value.Type()
		if (source.IsHstore() || (source.IsLquery() || source.IsLtree())) && source.ID() == capture.TypeID {
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(capture.TypeID)}
			old := expected.analysis.types[address]
			if old != nil && old.SQLType == source && old.Address() == address && auth.Catalog.Get(address) == nil {
				for _, dep := range expected.analysis.dependencies {
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

// The rebind mode validates nominal installed scalar occurrences only, never
// executable reuse. Owner/ACL changes still require fresh analysis and checks.
func (e *Engine) preparedAnalysisDefinitionsValidLocked(auth AuthorizationView, analysis *preparedAnalysis, path sequence.Environment, installedScalarAuthorityRebind bool) bool {
	if !e.preparedAnalysisEnvironmentValidLocked(auth, analysis, path) {
		return false
	}
	for pair, observed := range analysis.casts {
		current, _ := auth.Catalog.Cast(pair.Source, pair.Target)
		if current != observed {
			return false
		}
	}
	for _, dep := range analysis.dependencies {
		d := auth.Catalog.Get(dep.Ref.Address)
		if d == nil || dep.Ref.Domain() != e.Catalog.Domain || dep.Ref.Epoch != auth.Catalog.Epoch() {
			return false
		}
		if PreparedDefinitionStamp(d, auth.Session) != dep.Revision {
			current, ok := d.(*catalog.Type)
			if !ok {
				return false
			}
			if installedScalarAuthorityRebind && !current.SQLType.IsHstore() && (!current.SQLType.IsLquery() && !current.SQLType.IsLtree()) && !current.SQLType.IsEan13() && !current.SQLType.IsIsbn() && !current.SQLType.IsIsbn13() && !current.SQLType.IsIsmn() && !current.SQLType.IsIsmn13() && !current.SQLType.IsIssn() && !current.SQLType.IsIssn13() && !current.SQLType.IsUpc() {
				return false
			}
			old := analysis.types[dep.Ref.Address]
			if catalog.SamePreparedTypeDefinition(old, current) {
				continue
			}
			if !installedScalarAuthorityRebind || old == nil || old.SQLType.IsHstore() != current.SQLType.IsHstore() || old.SQLType.IsLquery() != current.SQLType.IsLquery() || old.SQLType.IsLtree() != current.SQLType.IsLtree() || old.SQLType.IsEan13() != current.SQLType.IsEan13() || old.SQLType.IsIsbn() != current.SQLType.IsIsbn() || old.SQLType.IsIsbn13() != current.SQLType.IsIsbn13() || old.SQLType.IsIsmn() != current.SQLType.IsIsmn() || old.SQLType.IsIsmn13() != current.SQLType.IsIsmn13() || old.SQLType.IsIssn() != current.SQLType.IsIssn() || old.SQLType.IsIssn13() != current.SQLType.IsIssn13() || old.SQLType.IsUpc() != current.SQLType.IsUpc() || !old.SQLType.IsHstore() && !old.SQLType.IsLquery() && !old.SQLType.IsLtree() && !old.SQLType.IsEan13() && !old.SQLType.IsIsbn() && !old.SQLType.IsIsbn13() && !old.SQLType.IsIsmn() && !old.SQLType.IsIsmn13() && !old.SQLType.IsIssn() && !old.SQLType.IsIssn13() && !old.SQLType.IsUpc() {
				return false
			}
			// Compare the same immutable definition while deliberately leaving
			// authority to the fresh binder, not to retained executable analysis.
			nominal := *old
			nominal.Owner, nominal.ACL = current.Owner, current.ACL
			if !catalog.SamePreparedTypeDefinition(&nominal, current) {
				return false
			}
		}
	}
	return true
}

// Caller holds Engine.mu. Sequence source events are independent of generator
// state and descriptor revisions. Commit exposes the descriptor's source stamp
// to peers; a reverted reset retains its event only in the initiating session.
func PreparedDefinitionStamp(d catalog.Descriptor, session *session.Session) ids.DefinitionRevision {
	if domain, ok := d.(*catalog.Type); ok && domain.UsesDomainPlanStamp() {
		return domain.EffectiveDomainPlanStamp()
	}
	var memory *sequence.SessionMemory
	if session != nil {
		memory = &PayloadOf(session).Sequences
	}
	return sequence.DefinitionStamp(d, memory)
}

// Build only on initial or invalidated analysis. Existing binder dependencies
// cover analyzed types/columns; view traversal supplies recursively rewritten
// sources and analysis-only dependencies without a second AST visitor.
func (e *Engine) preparedAnalysisLocked(s *StatementScope, auth AuthorizationView, b analyze.Binding) (*preparedAnalysis, error) {
	target := s.LockedRef
	if b.ViewWrite != nil {
		target = view.ViewWriteOf(b.ViewWrite).Logical.Ref
	}
	cell := &preparedAnalysis{domain: e.Catalog.Domain, epoch: s.prepared.epoch, session: s.Op.Session.SessionToken, private: s.prepared.private, path: s.prepared.path, namespace: s.prepared.namespace, target: target, needsReanalysis: s.prepared.changedDuringAdmission}
	cell.casts = b.Prepared.Casts
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
		if d == nil || ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		cell.dependencies = append(cell.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: PreparedDefinitionStamp(d, auth.Session)})
		if typ, ok := d.(*catalog.Type); ok {
			if !typ.UsesDomainPlanStamp() && (typ.SQLType.IsUserDefined() || typ.SQLType.IsArray()) {
				if cell.types == nil {
					cell.types = make(map[ids.ObjectAddress]*catalog.Type)
				}
				cell.types[ref.Address] = typ
			}
			// Type usages form the precise executable provider closure. Record
			// children in this cell rather than inventing a generic type stamp.
			visitType := func(id sqltype.ID) error {
				return visit(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}))
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
		cell.targets = make(map[parser.Statement]ids.ObjectRef, len(s.QueryLockAdmission.MutationTargets))
		for i := range s.mutations {
			mutation := s.mutations[i]
			if i == s.ActiveMutation {
				mutation = s.StatementMutation
			}
			ref := mutation.LockedRef
			if mutation.ViewWrite != nil {
				ref = mutation.ViewWrite.Logical.Ref
			}
			if ref.Domain() == nil {
				continue
			}
			cell.targets[mutation.Statement] = ref
			if err := visit(ref); err != nil {
				return nil, err
			}
		}
	}
	if len(s.QueryLockAdmission.TableQueries) != 0 {
		cell.sources = make(map[*parser.TableReference]analyze.PreparedSource, len(s.QueryLockAdmission.TableQueries))
	}
	for _, leaf := range b.QuerySources {
		if _, admitted := s.QueryLockAdmission.TableQueries[leaf.Table]; admitted {
			cell.sources[leaf.Table] = analyze.PreparedSource{Query: s.QueryLockAdmission.RetainedQuery(leaf.Table), Ref: leaf.Ref}
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
	// A domain conversion can be elided from the executable expression, but
	// its resolved cast occurrence still belongs to this analysis lifetime.
	for key, captured := range b.Prepared.Types {
		if key.Kind != analyze.DomainCast && key.Kind != analyze.IdentityCast || captured.Base.Type == nil {
			continue
		}
		ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(captured.Base.Type.ID())})
		if err := visit(ref); err != nil {
			return nil, err
		}
		if captured.Base.Type.IsArray() && captured.Base.Type.Element().IsDomain() {
			element := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(captured.Base.Type.Element().ID())})
			if err := visit(element); err != nil {
				return nil, err
			}
		}
	}
	return cell, nil
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
	reuse                  *preparedAnalysis
	invalid                bool
	path                   sequence.Environment
	namespace              uint64
	private                *session.Journal
	epoch                  ids.CatalogEpoch
	changedDuringAdmission bool
}
