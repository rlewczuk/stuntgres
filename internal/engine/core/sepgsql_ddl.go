package core

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
)

// Stage producers run both with and without Engine.Mu. Refresh only the actual
// physical MAC phase under its own subject lock; immutable selected SQL roles
// and committed predecessor remain the stage owner's original fenced inputs.
func (s *CatalogStage) sepgsqlView() (access.View, error) {
	auth := s.sepgsqlAuth
	if auth.Sepgsql.Policy == nil {
		return auth, nil
	}
	scope := s.statement
	if s.Owner == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil || scope == nil || scope.Op == nil || scope.Finished || scope.Engine() != s.Owner || s.AuthSession != scope.Op.Session || ActiveScopeOfLease(scope.Op.Lease) != scope || auth.Owner != s.Base.Owner() || auth.Catalog.Domain() != s.Base.Domain() || auth.Catalog.Epoch() != s.Base.Epoch() {
		return access.View{}, fmt.Errorf("stuntgres: invalid semantic catalog stage authority")
	}
	if err := scope.Op.Context().Err(); err != nil {
		return access.View{}, err
	}
	subject, generation, frame, depth := scope.Op.Session.Sepgsql.PhaseSnapshot()
	auth.Sepgsql.Subject, auth.Sepgsql.Generation, auth.Sepgsql.Frame, auth.Sepgsql.Depth = subject, generation, frame, depth
	debug, _ := scope.Op.Setting("sepgsql.debug_audit")
	auth.Sepgsql.Debug, auth.Sepgsql.Audit, auth.Sepgsql.Observer = debug == "on", scope.Op, scope
	auth.Recapture(s.Owner.StageRead(s))
	return auth, nil
}
func (s *CatalogStage) sepgsqlCheck(auth access.View, address ids.ObjectAddress, mask sepgsql.Mask) error {
	if auth.Sepgsql.Policy == nil {
		return nil
	}
	s.ObserveSecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"})
	return auth.SepgsqlCheck(address, mask)
}

// sepgsqlCreateSequence is shared by genuine stage and journal producers. The
// new descriptor supplies only the address and audit name, never authority over
// its parent. Sequences have one whole-object label, not column create hooks.
func sepgsqlCreateSequence(auth access.View, d *catalog.Relation) (*catalog.SecurityLabelRecord, error) {
	if err := auth.SepgsqlCheck(d.Namespace(), sepgsql.Permission("db_schema", "add_name")); err != nil {
		return nil, err
	}
	policy := auth.Sepgsql.Policy
	context := policy.CreateContext(auth.Sepgsql.Subject, auth.SepgsqlContext(d.Namespace()), "db_sequence", d.Name())
	if _, err := auth.SepgsqlDecision(context, "db_sequence", catalog.SepgsqlObjectKey(auth.Catalog, d, 0), sepgsql.Create, true); err != nil {
		return nil, err
	}
	return &catalog.SecurityLabelRecord{Label: context, PolicyID: policy.ID(), PolicyVersion: policy.Version()}, nil
}

// SepgsqlPostCreateSequenceLocked is the explicit CREATE SEQUENCE producer's
// postcreate boundary, after real name admission and before OWNED BY. Caller
// holds Engine.Mu and owns the attached prospective journal; no object has been
// installed in that journal yet. Import authority comes only from real auth.
func (e *Engine) SepgsqlPostCreateSequenceLocked(scope *StatementScope, auth AuthorizationView, changes *session.Journal, d *catalog.Relation) error {
	if auth.Sepgsql.Policy == nil {
		return nil
	}
	if scope == nil || scope.Op == nil || scope.Op.Session == nil || scope.Finished || scope.Engine() != e ||
		scope.Owner != scope.Op.Session.Transactions || ActiveScopeOfLease(scope.Op.Lease) != scope ||
		auth.inputs != &e.Authority || auth.Statement != scope || auth.Session != scope.Op.Session ||
		auth.Owner != &e.Catalog.Owner || auth.Sepgsql.Policy != e.Sepgsql ||
		changes == nil || scope.Catalog != changes || scope.CatalogAuthority == nil ||
		scope.CatalogAuthority.Session != auth.Session {
		return fmt.Errorf("stuntgres: invalid sequence postcreate authority")
	}
	if err := scope.Op.Context().Err(); err != nil {
		return err
	}
	if auth.Catalog.Domain() != e.Catalog.Domain || auth.Catalog.Epoch() != e.Catalog.State.Epoch() ||
		auth.Catalog.Epoch() != scope.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d == nil || d.RelationKind != catalog.SequenceRelation || d.Sequence == nil ||
		d.Address().ClassID != ids.RelationClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 ||
		auth.Catalog.State() != e.PrivateCatalogLocked(changes).State() {
		return fmt.Errorf("stuntgres: invalid sequence postcreate journal")
	}
	if err := e.CatalogPreflightLocked(changes); err != nil {
		return err
	}
	auth.Session.Mu.Lock()
	err := PublicationOperationLocked(e, scope.Op)
	if err == nil {
		err = e.catalogAuthorityPreflightLocked(scope.CatalogAuthority, changes)
	}
	auth.Session.Mu.Unlock()
	if err != nil {
		return err
	}
	if _, observed := changes.Originals[d.Namespace()]; !observed || auth.Catalog.Get(d.Namespace()) == nil {
		return ids.ErrCatalogReplaced
	}
	if auth.Catalog.Get(d.Address()) != nil || auth.Catalog.NameAddress(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return catalog.Conflict{}
	}
	subject, generation, frame, depth := auth.Session.Sepgsql.PhaseSnapshot()
	auth.Sepgsql.Subject, auth.Sepgsql.Generation, auth.Sepgsql.Frame, auth.Sepgsql.Depth = subject, generation, frame, depth
	debug, _ := scope.Op.Setting("sepgsql.debug_audit")
	auth.Sepgsql.Debug, auth.Sepgsql.Audit, auth.Sepgsql.Observer = debug == "on", scope.Op, scope
	record, err := sepgsqlCreateSequence(auth.View, d)
	if err != nil {
		return err
	}
	changes.RecordSecurityLabel(e.Catalog.State, catalog.SecurityLabelKey{Object: d.Address(), Provider: "selinux"}, record)
	return nil
}

func (s *CatalogStage) sepgsqlCreate(d catalog.Descriptor) error {
	if s.sepgsqlAuth.Sepgsql.Policy == nil || s.terminalCleanup {
		return nil
	}
	auth, err := s.sepgsqlView()
	if err != nil {
		return err
	}
	policy := auth.Sepgsql.Policy
	if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.SequenceRelation {
		s.ObserveSecurityLabel(catalog.SecurityLabelKey{Object: relation.Namespace(), Provider: "selinux"})
		record, err := sepgsqlCreateSequence(auth, relation)
		if err != nil {
			return err
		}
		s.RecordSecurityLabel(catalog.SecurityLabelKey{Object: relation.Address(), Provider: "selinux"}, record)
		return nil
	}
	if index, ok := d.(*catalog.Relation); ok && index.RelationKind == catalog.IndexRelation {
		if index.Namespace().ObjectID == ids.PGToastNamespaceOID {
			return nil
		}
		if err = s.sepgsqlCheck(auth, index.Namespace(), sepgsql.Permission("db_schema", "add_name")); err != nil {
			return err
		}
		if index.SQLIndex == nil {
			return ids.ErrCatalogReplaced
		}
		parent, ok := auth.Catalog.Relation(index.SQLIndex.Parent)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			return nil
		}
		return s.sepgsqlCheck(auth, parent.Address(), sepgsql.Setattr)
	}
	class := catalog.SepgsqlClass(d, 0)
	if class == "" {
		if _, relation := d.(*catalog.Relation); relation && d.Namespace() != (ids.ObjectAddress{}) {
			return s.sepgsqlCheck(auth, d.Namespace(), sepgsql.Permission("db_schema", "add_name"))
		}
		return nil
	}
	parent := d.Namespace()
	switch d.(type) {
	case *catalog.Namespace:
		parent = auth.Owner.DatabaseAddress()
	case *catalog.Database:
		parent = auth.Owner.DatabaseAddress()
	}
	parentContext := policy.Unlabeled()
	if parent != (ids.ObjectAddress{}) {
		parentContext = auth.SepgsqlContext(parent)
		s.ObserveSecurityLabel(catalog.SecurityLabelKey{Object: parent, Provider: "selinux"})
		if d.Namespace() != (ids.ObjectAddress{}) {
			if err := s.sepgsqlCheck(auth, parent, sepgsql.Permission("db_schema", "add_name")); err != nil {
				return err
			}
		}
	}
	name := d.Name()
	auditName := catalog.SepgsqlObjectKey(auth.Catalog, d, 0)
	if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary.ID() != 0 && !ns.Temporary.IsDetached() &&
		ns.Temporary.Private() == nil && ns.Temporary.Domain() == s.Base.Domain() &&
		s.AuthSession != nil && ns.Temporary == s.AuthSession.SessionToken {
		name = "pg_temp"
		if ns.TemporaryToast {
			name = "pg_toast_temp"
		}
		auditName = catalog.DependencyIdentifier(name)
	}
	context := policy.CreateContext(auth.Sepgsql.Subject, parentContext, class, name)
	mask := sepgsql.Create
	if procedure, ok := d.(*catalog.Procedure); ok && procedure.Display.Leakproof {
		mask |= sepgsql.Permission("db_procedure", "install")
	}
	if _, err := auth.SepgsqlDecision(context, class, auditName, mask, true); err != nil {
		return err
	}
	s.RecordSecurityLabel(catalog.SecurityLabelKey{Object: d.Address(), Provider: "selinux"}, &catalog.SecurityLabelRecord{Label: context, PolicyID: policy.ID(), PolicyVersion: policy.Version()})
	if relation, ok := d.(*catalog.Relation); ok && (relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation) {
		for sub := range catalog.SepgsqlAttributes(relation) {
			if err := s.sepgsqlCreateColumn(auth, relation, sub, context); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *CatalogStage) sepgsqlCreateColumn(auth access.View, relation *catalog.Relation, sub int32, parent string) error {
	policy := auth.Sepgsql.Policy
	context := policy.CreateContext(auth.Sepgsql.Subject, parent, "db_column", catalog.SepgsqlAttributeName(relation, sub))
	if _, err := auth.SepgsqlDecision(context, "db_column", catalog.SepgsqlObjectKey(auth.Catalog, relation, sub), sepgsql.Create, true); err != nil {
		return err
	}
	address := relation.Address()
	address.SubID = sub
	s.RecordSecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"}, &catalog.SecurityLabelRecord{Label: context, PolicyID: policy.ID(), PolicyVersion: policy.Version()})
	return nil
}
func (s *CatalogStage) sepgsqlReplace(previous, next catalog.Descriptor) error {
	if s.sepgsqlAuth.Sepgsql.Policy == nil || s.terminalCleanup || s.statement == nil || previous == nil {
		return nil
	}
	if _, relation := previous.(*catalog.Relation); relation {
		return nil
	} // Actual semantic relation/column owners emit their source hooks.
	tag, _, _, _ := session.ClassifyCommandTag(s.statement.Statement, 0)
	// DCL, COMMENT, shared projections and internal COW bookkeeping are not
	// post-alter hooks. This is semantic utility admission, not descriptor churn.
	if !strings.HasPrefix(tag, "ALTER ") && tag != "CREATE FUNCTION" && tag != "CREATE VIEW" {
		return nil
	}
	auth, err := s.sepgsqlView()
	if err != nil {
		return err
	}
	if catalog.SepgsqlClass(previous, 0) == "" {
		return nil
	}
	mask := sepgsql.Setattr
	if old, ok := previous.(*catalog.Procedure); ok {
		if procedure, ok := next.(*catalog.Procedure); ok && !old.Display.Leakproof && procedure.Display.Leakproof {
			mask |= sepgsql.Permission("db_procedure", "install")
		}
	}
	if _, procedure := previous.(*catalog.Procedure); procedure {
		if previous.Namespace() != next.Namespace() {
			if err = s.sepgsqlCheck(auth, previous.Namespace(), sepgsql.Permission("db_schema", "remove_name")); err != nil {
				return err
			}
			if err = s.sepgsqlCheck(auth, previous.Namespace(), sepgsql.Permission("db_schema", "add_name")); err != nil {
				return err
			} // released proc.c old-schema caller
		}
		if previous.Name() != next.Name() {
			if err = s.sepgsqlCheck(auth, previous.Namespace(), sepgsql.Permission("db_schema", "add_name")|sepgsql.Permission("db_schema", "remove_name")); err != nil {
				return err
			}
		}
	}
	if err := s.sepgsqlCheck(auth, previous.Address(), mask); err != nil {
		return err
	}
	return nil
}

// SepgsqlDropDescriptor consumes an execution-owned semantic decision before
// its physical retirement. Pure immutable preparation carries no MAC grants.
func (p *DropProvider) SepgsqlDropDescriptor(d catalog.Descriptor, internal bool) error {
	if internal || p.Stage == nil || p.Owner.Sepgsql == nil || p.Stage.terminalCleanup {
		return nil
	}
	if d == nil {
		return ids.ErrCatalogReplaced
	}
	if consumed, err := p.consumeSepgsqlRetirement(d.Address()); err != nil || consumed {
		return err
	}
	if err := p.PrepareSepgsqlRetirements([]ids.ObjectAddress{d.Address()}, nil); err != nil {
		return err
	}
	_, err := p.consumeSepgsqlRetirement(d.Address())
	return err
}
func (p *DropProvider) sepgsqlDropConsent(d catalog.Descriptor, internal bool) error {
	if internal || p.Stage == nil || p.Owner.Sepgsql == nil || p.Stage.terminalCleanup {
		return nil
	}
	auth, err := p.Stage.sepgsqlView()
	if err != nil {
		return err
	}
	if d == nil {
		return ids.ErrCatalogReplaced
	}
	check := func(address ids.ObjectAddress, mask sepgsql.Mask) error {
		p.Stage.ObserveSecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"})
		return auth.SepgsqlCheck(address, mask)
	}
	if index, ok := d.(*catalog.Relation); ok && index.RelationKind == catalog.IndexRelation {
		if index.Namespace().ObjectID == ids.PGToastNamespaceOID {
			return nil
		}
		if index.SQLIndex == nil {
			return ids.ErrCatalogReplaced
		}
		parent, ok := auth.Catalog.Relation(index.SQLIndex.Parent)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			return nil
		}
		if err = check(index.Namespace(), sepgsql.Permission("db_schema", "remove_name")); err != nil {
			return err
		}
		return check(parent.Address(), sepgsql.Setattr)
	}
	if catalog.SepgsqlClass(d, 0) == "" {
		return nil
	}
	if d.Namespace() != (ids.ObjectAddress{}) {
		if err = check(d.Namespace(), sepgsql.Permission("db_schema", "remove_name")); err != nil {
			return err
		}
	}
	if err = check(d.Address(), sepgsql.Drop); err != nil {
		return err
	}
	if relation, ok := d.(*catalog.Relation); ok && (relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.CatalogRelation && !relation.CatalogView) {
		for sub := range catalog.SepgsqlAttributes(relation) {
			address := relation.Address()
			address.SubID = sub
			if err = check(address, sepgsql.Drop); err != nil {
				return err
			}
		}
	}
	return nil
}
