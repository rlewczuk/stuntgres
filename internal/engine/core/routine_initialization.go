package core

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
)

// Restore owns a separate, serial initialization origin. It proves ordinary SQL
// initialization in the isolated importer, not a client MAC decision or subject.
// Only RestoreStage mints it; StartRestoreSession binds its one physical owner.
type restoreInitializationOrigin struct {
	engine  *Engine
	session *session.Session
	domain  *ids.Domain
	epoch   ids.CatalogEpoch
	policy  *sepgsql.Runtime
}

// Caller owns Engine.Mu and has recaptured this scope's real authorization.
func (s *StatementScope) routineInitializationOriginLocked(auth AuthorizationView) (*restoreInitializationOrigin, error) {
	e := s.Engine()
	if e.restoreInitializationOrigin == nil && e.Sepgsql != nil && auth.Sepgsql.Policy == e.Sepgsql {
		return nil, nil
	}
	origin := e.restoreInitializationOrigin
	op := s.Op
	if origin == nil || origin.engine != e || origin.session != op.Session || origin.domain != e.Catalog.Domain ||
		origin.epoch != e.Catalog.State.Epoch() || origin.policy == nil || origin.policy != e.Sepgsql ||
		e.ImportTargets == nil || !OperationReplayOrZero(op).StagingEffects ||
		auth.Sepgsql != (access.SepgsqlAuthority{}) || auth.Statement != s || auth.Session != op.Session ||
		auth.Owner != &e.Catalog.Owner || auth.Catalog.Owner() != &e.Catalog.Owner ||
		auth.Catalog.Domain() != e.Catalog.Domain || auth.Catalog.Epoch() != origin.epoch ||
		auth.Catalog.State() == nil || ActiveScopeOfLease(op.Lease) != s {
		return nil, fmt.Errorf("stuntgres: missing actual routine initialization policy")
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil {
		return nil, err
	}
	if !bootstrap.Superuser || bootstrap.Ref != e.RoleStore.Domain.Principal(10) {
		return nil, fmt.Errorf("stuntgres: invalid trusted restore routine initialization authority")
	}
	if err := op.Context().Err(); err != nil {
		return nil, err
	}
	return origin, nil
}

// An initialization receipt belongs only to this actual command. It is not an
// ACL cache, a persistent Program field or permission for another callsite.
// Normal immutable predecessor observations stay with the command's existing
// publication/readmission fences; these fields seal the phase which made them.
type routineInitializationReceipt struct {
	completion        uint64
	actor             authorization.PrincipalRef
	policy            *sepgsql.Runtime
	subject           string
	generation, frame uint64
	depth             int
	predecessor       *catalog.State
	label             *catalog.SecurityLabelRecord
	importOrigin      *restoreInitializationOrigin
}

func (s *StatementScope) ownsRoutineInitialization(owner exec.RoutineInitialization) bool {
	if !owner.Matches(ids.CommandIdentity{Transaction: s.TxRef, Command: s.CommandID}) {
		return false
	}
	origin, ok := s.routineInitializationOwners[owner]
	return ok && origin == s.Engine().restoreInitializationOrigin
}

// Caller owns Mu. The sequence is metadata only; the Scope's ephemeral owner set
// and exact completed site receipts are what make an invocation admissible.
func (s *StatementScope) newRoutineInitializationLocked(auth AuthorizationView) (exec.RoutineInitialization, error) {
	e := s.Engine()
	if e.Sepgsql == nil {
		return exec.RoutineInitialization{}, nil
	}
	var origin *restoreInitializationOrigin
	if e.restoreInitializationOrigin != nil {
		var err error
		origin, err = s.routineInitializationOriginLocked(auth)
		if err != nil {
			return exec.RoutineInitialization{}, err
		}
	}
	if e.routineInitializationSequence == math.MaxUint64 {
		return exec.RoutineInitialization{}, fmt.Errorf("stuntgres: routine initialization identity exhausted")
	}
	e.routineInitializationSequence++
	owner := exec.NewRoutineInitialization(ids.CommandIdentity{Transaction: s.TxRef, Command: s.CommandID}, e.routineInitializationSequence)
	if !owner.Active() {
		return exec.RoutineInitialization{}, fmt.Errorf("stuntgres: missing actual routine initialization command")
	}
	if s.routineInitializationOwners == nil {
		s.routineInitializationOwners = make(map[exec.RoutineInitialization]*restoreInitializationOrigin)
	}
	s.routineInitializationOwners[owner] = origin
	return owner, nil
}
func (h *statementHost) NewRoutineInitialization() (exec.RoutineInitialization, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return exec.RoutineInitialization{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return exec.RoutineInitialization{}, err
	}
	return s.newRoutineInitializationLocked(auth)
}

func (h *statementHost) DefaultRoutineInitialization(source exec.DefaultInitializationSource, initialize bool) (exec.RoutineInitialization, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return exec.RoutineInitialization{}, err
	}
	if !source.Valid() || !s.ownsRoutineInitialization(source.Parent) {
		return exec.RoutineInitialization{}, fmt.Errorf("stuntgres: foreign accepted default initialization source")
	}
	if owner, ok := s.defaultInitializations[source]; ok {
		return owner, nil
	}
	if !initialize {
		return exec.RoutineInitialization{}, fmt.Errorf("stuntgres: missing accepted default initialization")
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return exec.RoutineInitialization{}, err
	}
	owner, err := s.newRoutineInitializationLocked(auth)
	if err != nil {
		return owner, err
	}
	if s.defaultInitializations == nil {
		s.defaultInitializations = make(map[exec.DefaultInitializationSource]exec.RoutineInitialization)
	}
	s.defaultInitializations[source] = owner
	return owner, nil
}

// A derived phase is not another original receipt: retain the exact source call
// and distinguish the entered and strict-NULL expanded expression states.
type nativeBodyInitializationSource struct {
	call  exec.RoutineInitializationCall
	phase exec.RoutineInitializationPhase
}

func (h *statementHost) NativeBodyRoutineInitialization(call exec.RoutineInitializationCall, phase exec.RoutineInitializationPhase) (exec.RoutineInitialization, error) {
	if phase != exec.NativeBodyInitialization && phase != exec.ExpandedNativeDeclarationInitialization {
		return exec.RoutineInitialization{}, fmt.Errorf("stuntgres: invalid native body initialization phase")
	}
	source := nativeBodyInitializationSource{call: call, phase: phase}
	if _, err := h.ValidateRoutineInitialization(call.Owner, call.Site); err != nil {
		return exec.RoutineInitialization{}, err
	}
	s := h.scope()
	if owner, ok := s.nativeBodyInitializations[source]; ok {
		return owner, nil
	}
	owner, err := h.NewRoutineInitialization()
	if err != nil {
		return owner, err
	}
	if s.nativeBodyInitializations == nil {
		s.nativeBodyInitializations = make(map[nativeBodyInitializationSource]exec.RoutineInitialization)
	}
	s.nativeBodyInitializations[source] = owner
	return owner, nil
}

func (h *statementHost) InitializeRoutineCall(owner exec.RoutineInitialization, site exec.RoutineInitializationSite, actor authorization.PrincipalRef) (failure error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	if !site.Valid() || !s.ownsRoutineInitialization(owner) {
		return fmt.Errorf("stuntgres: invalid routine initialization owner or callsite")
	}
	call := exec.RoutineInitializationCall{Owner: owner, Site: site}
	if previous, exists := s.routineInitializations[call]; exists {
		if previous.actor != actor {
			return fmt.Errorf("stuntgres: changed initialized routine actor")
		}
		if previous.importOrigin != nil {
			_, err := h.ValidateRoutineInitialization(owner, site)
			return err
		}
		return nil
	}
	mark := s.MarkSepgsqlObservations()
	defer func() {
		if failure != nil {
			s.RestoreSepgsqlObservations(mark)
		}
	}()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	origin, err := s.routineInitializationOriginLocked(auth)
	if err != nil {
		return err
	}
	if site.Reference.Domain() != e.Catalog.Domain || site.Reference.Epoch != auth.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if err = auth.Roles.ValidatePrincipal(actor); err != nil {
		return err
	}
	current, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, site.Routine)
	if err != nil {
		return err
	}
	auth.State.CurrentUser = actor
	// Procedure EXECUTE never reads an access.Environment. The real captured
	// actor/roles and physical subject remain independent, without boxing a copied
	// live AuthorizationView merely to satisfy unrelated TEMP/name interfaces.
	if err = auth.View.Check(nil, access.Requirement{Object: site.Reference, Privileges: authorization.ExecutePrivilege}); err != nil {
		return err
	}
	if err = s.Op.Context().Err(); err != nil {
		return err
	}
	completion, err := s.completeRoutineInitialization()
	if err != nil {
		return err
	}
	if s.routineInitializations == nil {
		s.routineInitializations = make(map[exec.RoutineInitializationCall]routineInitializationReceipt)
	}
	receipt := routineInitializationReceipt{completion: completion, actor: actor, importOrigin: origin}
	if origin == nil {
		receipt.policy, receipt.subject = auth.Sepgsql.Policy, auth.Sepgsql.Subject
		receipt.generation, receipt.frame, receipt.depth = auth.Sepgsql.Generation, auth.Sepgsql.Frame, auth.Sepgsql.Depth
		receipt.predecessor = auth.Sepgsql.Predecessor
		receipt.label = auth.Catalog.State().SecurityLabel(catalog.SecurityLabelKey{Object: current.Address(), Provider: "selinux"})
	}
	s.routineInitializations[call] = receipt
	return nil
}

func (h *statementHost) ValidateRoutineInitialization(owner exec.RoutineInitialization, site exec.RoutineInitializationSite) (authorization.PrincipalRef, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return authorization.PrincipalRef{}, err
	}
	if !site.Valid() || !s.ownsRoutineInitialization(owner) {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: missing actual routine initialization owner")
	}
	receipt, ok := s.routineInitializations[exec.RoutineInitializationCall{Owner: owner, Site: site}]
	if !ok {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: missing initialized routine callsite")
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return authorization.PrincipalRef{}, err
	}
	if receipt.importOrigin != nil {
		origin, err := s.routineInitializationOriginLocked(auth)
		if err != nil {
			return authorization.PrincipalRef{}, err
		}
		if receipt.importOrigin != origin {
			return authorization.PrincipalRef{}, ids.ErrCatalogReplaced
		}
	} else if receipt.policy != e.Sepgsql || receipt.policy == nil || auth.Sepgsql.Policy != receipt.policy {
		return authorization.PrincipalRef{}, ids.ErrCatalogReplaced
	}
	if site.Reference.Domain() != e.Catalog.Domain || site.Reference.Epoch != auth.Catalog.Epoch() {
		return authorization.PrincipalRef{}, ids.ErrCatalogReplaced
	}
	if err = auth.Roles.ValidatePrincipal(receipt.actor); err != nil {
		return authorization.PrincipalRef{}, err
	}
	if _, err = exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, site.Routine); err != nil {
		return authorization.PrincipalRef{}, err
	}
	if _, err = auth.View.Object(site.Reference); err != nil {
		return authorization.PrincipalRef{}, err
	}
	if err = s.Op.Context().Err(); err != nil {
		return authorization.PrincipalRef{}, err
	}
	// Deliberately no current-subject/current-actor equality and no EXECUTE replay:
	// arguments can lawfully change either after this initializer completed.
	return receipt.actor, nil
}

// ValidateRoutineInitializationOwner borrows an already admitted command owner.
// Unlike sealing, this live entry fence neither copies receipts nor forbids a
// genuine serial trusted transition. It never initializes a call or replays ACLs.
func (h *statementHost) ValidateRoutineInitializationOwner(owner exec.RoutineInitialization, read catalog.Read) error {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	if !s.ownsRoutineInitialization(owner) {
		return fmt.Errorf("stuntgres: missing actual routine initialization owner")
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	if e.restoreInitializationOrigin != nil {
		if _, err = s.routineInitializationOriginLocked(auth); err != nil {
			return err
		}
	} else if e.Sepgsql == nil || auth.Sepgsql.Policy != e.Sepgsql {
		return ids.ErrCatalogReplaced
	}
	if read.Owner() != auth.Catalog.Owner() || read.Domain() != auth.Catalog.Domain() || read.Epoch() != auth.Catalog.Epoch() || read.State() == nil {
		return ids.ErrCatalogReplaced
	}
	return s.Op.Context().Err()
}

func (h *statementHost) SealRoutineInitialization(owner exec.RoutineInitialization, read catalog.Read) (exec.ClosedRoutineInitialization, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return exec.ClosedRoutineInitialization{}, err
	}
	if !s.ownsRoutineInitialization(owner) {
		return exec.ClosedRoutineInitialization{}, fmt.Errorf("stuntgres: missing closed initialization owner")
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return exec.ClosedRoutineInitialization{}, err
	}
	// Import completion is serial SQL work, never a sealed worker MAC proof.
	if e.restoreInitializationOrigin != nil || read.Owner() != auth.Catalog.Owner() || read.Domain() != auth.Catalog.Domain() || read.Epoch() != auth.Catalog.Epoch() || e.Sepgsql == nil || e.Sepgsql.AnyTrustedTransitions() {
		return exec.ClosedRoutineInitialization{}, ids.ErrCatalogReplaced
	}
	receipts := make([]exec.RoutineInitializationReceipt, 0, len(s.routineInitializations))
	for call, receipt := range s.routineInitializations {
		if call.Owner == owner {
			receipts = append(receipts, exec.RoutineInitializationReceipt{Site: call.Site, Actor: receipt.actor})
		}
	}
	direct := make([]exec.DirectRoutineInitializationSite, 0, len(s.directRoutineInitializations))
	for call := range s.directRoutineInitializations {
		if call.Owner == owner {
			direct = append(direct, call.Site)
		}
	}
	return exec.SealClosedRoutineInitialization(owner, read, auth.RoleRecord, receipts, direct)
}

func (s *StatementScope) clearRoutineInitializations() {
	s.routineInitializations = nil
	s.routineInitializationOwners = nil
	s.defaultInitializations = nil
	s.nativeBodyInitializations = nil
	s.directRoutineInitializations = nil
}

// Direct factories and index support select genuine ownerless catalog rows,
// not ACL-bearing SQL objects. Admit their actual provider associations and
// owning family where applicable; unrelated sources retain Object admission.
// The caller holds Engine.Mu and captured the actual operation authority.
func validateDirectRoutineSource(auth AuthorizationView, site exec.DirectRoutineInitializationSite) error {
	if site.Purpose == exec.DirectAccessMethod {
		if _, err := auth.Roles.Descriptor(auth.State.CurrentUser); err != nil {
			return err
		}
		source := site.Source
		if source.Domain() != auth.Catalog.Domain() || source.Epoch != auth.Catalog.Epoch() || source.Address.ClassID != ids.AccessMethodClassID || source.Address.ObjectID == 0 || source.Address.SubID != 0 {
			return ids.ErrCatalogReplaced
		}
		method, ok := auth.Catalog.AccessMethod(source.Address.ObjectID)
		if !ok || method.MethodKind() != "i" || method.Handler != site.Reference.Address.ObjectID {
			return ids.ErrCatalogReplaced
		}
		return (catalog.AccessMethodProvider{}).Validate(auth.Catalog, method)
	}
	if site.Purpose == exec.DirectIndexSupport {
		if _, err := auth.Roles.Descriptor(auth.State.CurrentUser); err != nil {
			return err
		}
		source := site.Source
		if source.Domain() != auth.Catalog.Domain() || source.Epoch != auth.Catalog.Epoch() ||
			source.Address.ClassID != ids.AccessMethodProcedureClassID || source.Address.ObjectID == 0 || source.Address.SubID != 0 {
			return ids.ErrCatalogReplaced
		}
		member, ok := auth.Catalog.Get(source.Address).(*catalog.OperatorFamilySupport)
		if !ok || member.Function != site.Reference.Address.ObjectID || int32(member.Number) != int32(site.Ordinal) {
			return ids.ErrCatalogReplaced
		}
		selected, ok := auth.Catalog.FamilySupport(member.Family, member.Left, member.Right, member.Number)
		if !ok || selected != member {
			return ids.ErrCatalogReplaced
		}
		family, ok := auth.Catalog.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: member.Family})
		if !ok {
			return ids.ErrCatalogReplaced
		}
		for _, descriptor := range [2]catalog.Descriptor{member, family} {
			provider := auth.Catalog.Owner().Provider(descriptor.Address().ClassID)
			if provider == nil {
				return ids.ErrCatalogReplaced
			}
			if err := provider.Validate(auth.Catalog, descriptor); err != nil {
				return err
			}
		}
		_, err := auth.Object(auth.Ref(family.Address()))
		return err
	}
	if site.Source.Address.ObjectID != 0 {
		_, err := auth.Object(site.Source)
		return err
	}
	return nil
}

func (h *statementHost) InitializeDirectRoutine(owner exec.RoutineInitialization, site exec.DirectRoutineInitializationSite) error {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	if !site.Valid() || !s.ownsRoutineInitialization(owner) {
		return fmt.Errorf("stuntgres: foreign direct routine initialization source")
	}
	call := exec.DirectRoutineInitializationCall{Owner: owner, Site: site}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	if e.restoreInitializationOrigin != nil {
		if _, err = s.routineInitializationOriginLocked(auth); err != nil {
			return err
		}
	}
	if site.Reference.Domain() != auth.Catalog.Domain() || site.Reference.Epoch != auth.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if _, err = exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, site.Routine); err != nil {
		return err
	}
	if err = validateDirectRoutineSource(auth, site); err != nil {
		return err
	}
	if _, ok := s.directRoutineInitializations[call]; ok {
		return nil
	}
	if err = s.Op.Context().Err(); err != nil {
		return err
	}
	completion, err := s.completeRoutineInitialization()
	if err != nil {
		return err
	}
	if s.directRoutineInitializations == nil {
		s.directRoutineInitializations = make(map[exec.DirectRoutineInitializationCall]uint64)
	}
	s.directRoutineInitializations[call] = completion
	return nil
}

func (h *statementHost) ValidateDirectRoutineInitialization(owner exec.RoutineInitialization, site exec.DirectRoutineInitializationSite) error {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	if !site.Valid() || !s.ownsRoutineInitialization(owner) {
		return fmt.Errorf("stuntgres: foreign direct routine invocation source")
	}
	if _, ok := s.directRoutineInitializations[exec.DirectRoutineInitializationCall{Owner: owner, Site: site}]; !ok {
		return fmt.Errorf("stuntgres: missing initialized direct routine source")
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	if e.restoreInitializationOrigin != nil {
		if _, err = s.routineInitializationOriginLocked(auth); err != nil {
			return err
		}
	}
	if site.Reference.Domain() != auth.Catalog.Domain() || site.Reference.Epoch != auth.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if _, err = exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, site.Routine); err != nil {
		return err
	}
	if err = validateDirectRoutineSource(auth, site); err != nil {
		return err
	}
	return s.Op.Context().Err()
}
