package core

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// ControlSepgsqlAuthority is an admission-time immutable identity capture plus
// a command-owned deferred audit sink. It retains no session, lease or mutable
// SQL authority across reader/writer I/O. A later pool user's setcon cannot
// change an already admitted control's subject. Only Capture constructs it.
type ControlSepgsqlAuthority struct {
	owner       *catalog.Owner
	policy      *sepgsql.Runtime
	subject     string
	generation  uint64
	debug       bool
	threshold   session.NoticeLevel
	notice      func(sqlstate.Error)
	events      []sepgsql.AuditEvent
	predecessor *catalog.State
	roles       *access.RoleRecord
}

func (a *ControlSepgsqlAuthority) SepgsqlAudit(policy *sepgsql.Runtime, event sepgsql.AuditEvent) {
	if a.policy != policy {
		panic("stuntgres: foreign control audit policy")
	}
	a.events = append(a.events, event)
}
func (e *Engine) CaptureControlSepgsql(ctx context.Context, s *session.Session, notice func(sqlstate.Error)) (*ControlSepgsqlAuthority, error) {
	if e.Sepgsql == nil {
		return nil, nil
	}
	if s == nil || EngineOf(s) != e || s.Sepgsql == nil {
		return nil, fmt.Errorf("stuntgres: missing control sepgsql subject authority")
	}
	op, err := s.AcquireActivity(ctx)
	if err != nil {
		return nil, err
	}
	defer op.Done()
	t := s.Transactions
	if err = t.AcquireAdmission(ctx, op); err != nil {
		return nil, err
	}
	defer t.Admission.Unlock()
	op.SnapshotNoticeThreshold()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	authority := &ControlSepgsqlAuthority{owner: &e.Catalog.Owner, policy: e.Sepgsql, subject: auth.Sepgsql.Subject, generation: auth.Sepgsql.Generation, debug: auth.Sepgsql.Debug, threshold: op.NoticeThreshold, notice: notice, predecessor: e.Catalog.State, roles: e.RoleStore.Published}
	e.Mu.Unlock()
	return authority, nil
}
func (a *ControlSepgsqlAuthority) Deliver() {
	if a == nil {
		return
	}
	events := a.events
	a.events = nil
	for _, event := range events {
		a.policy.Deliver(event)
		if a.notice != nil && a.threshold <= session.LevelLog {
			a.notice(sqlstate.Error{Severity: "LOG", SeverityUnlocalized: "LOG", Code: "00000", Message: event.Message})
		}
	}
}

// ValidateControlSepgsql admits only this destination's immutable capture.
func (e *Engine) ValidateControlSepgsql(a *ControlSepgsqlAuthority) error {
	if e.Sepgsql == nil {
		if a != nil {
			return fmt.Errorf("stuntgres: foreign control sepgsql authority")
		}
		return nil
	}
	if a == nil || a.owner != &e.Catalog.Owner || a.policy != e.Sepgsql || !a.policy.Valid(a.subject) || a.generation == 0 || a.predecessor == nil || a.roles == nil {
		return fmt.Errorf("stuntgres: missing or foreign control sepgsql authority")
	}
	return nil
}

// Caller holds final replacement locks. Imported identities have already been
// remapped and all old/new label predecessors are the actual target roots.
func (e *Engine) checkControlSepgsqlLocked(ctx context.Context, next *catalog.State, a *ControlSepgsqlAuthority) error {
	if err := e.ValidateControlSepgsql(a); err != nil {
		return err
	}
	if err := catalog.ValidateSecurityLabels(e.Catalog.Read(next)); err != nil {
		return err
	}
	if a == nil {
		return ctx.Err()
	}
	if e.Catalog.State != a.predecessor || e.RoleStore.Published != a.roles {
		return ids.ErrCatalogReplaced
	}
	policy := a.policy
	check := func(target, class, name string, mask sepgsql.Mask) error {
		allowed, event := policy.Decide(a.subject, target, class, name, mask, a.debug)
		if event != nil {
			a.SepgsqlAudit(policy, *event)
		}
		if !allowed {
			return SQLError("42501", "SELinux: security policy violation")
		}
		return nil
	}
	oldView, newView := e.Catalog.Current(), e.Catalog.Read(next)
	oldTargets, newTargets := sepgsqlTargets(oldView, false, false), sepgsqlTargets(newView, false, false)
	compare := func(a, b ids.ObjectAddress) int {
		if a.ClassID < b.ClassID {
			return -1
		}
		if a.ClassID > b.ClassID {
			return 1
		}
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		if a.SubID < b.SubID {
			return -1
		}
		if a.SubID > b.SubID {
			return 1
		}
		return 0
	}
	// A newly labelable tombstone participates only when an actual association
	// on either side requires transfer. Include its genuine opposite identity
	// even without a label, so effective UnlabeledContext still needs consent.
	var retired map[ids.ObjectAddress]struct{}
	for _, view := range [...]catalog.Read{oldView, newView} {
		for key := range view.State().SecurityLabels() {
			if key.Provider != "selinux" || key.Object.SubID <= 0 {
				continue
			}
			object := key.Object
			object.SubID = 0
			relation, ok := view.Relation(object)
			if !ok || relation.IndexForAttribute(key.Object.SubID) >= 0 || catalog.SepgsqlClass(relation, key.Object.SubID) != "db_column" {
				continue
			}
			if retired == nil {
				retired = make(map[ids.ObjectAddress]struct{})
			}
			retired[key.Object] = struct{}{}
		}
	}
	for address := range retired {
		object := address
		object.SubID = 0
		for i, view := range [...]catalog.Read{oldView, newView} {
			relation, ok := view.Relation(object)
			if !ok || relation.IndexForAttribute(address.SubID) >= 0 || catalog.SepgsqlClass(relation, address.SubID) != "db_column" {
				continue
			}
			if i == 0 {
				oldTargets = append(oldTargets, address)
			} else {
				newTargets = append(newTargets, address)
			}
		}
	}
	if len(retired) != 0 {
		slices.SortFunc(oldTargets, compare)
		slices.SortFunc(newTargets, compare)
	}
	// Walk the canonical actual target union, not just stored label rows:
	// absent associations still carry UnlabeledContext and require consent.
	for i, j := 0, 0; i < len(oldTargets) || j < len(newTargets); {
		if err := ctx.Err(); err != nil {
			return err
		}
		var target ids.ObjectAddress
		oldPresent, newPresent := false, false
		switch {
		case j == len(newTargets) || i < len(oldTargets) && compare(oldTargets[i], newTargets[j]) < 0:
			target, oldPresent = oldTargets[i], true
			i++
		case i == len(oldTargets) || compare(oldTargets[i], newTargets[j]) > 0:
			target, newPresent = newTargets[j], true
			j++
		default:
			target, oldPresent, newPresent = oldTargets[i], true, true
			i++
			j++
		}
		key := catalog.SecurityLabelKey{Object: target, Provider: "selinux"}
		old, replacement := e.Catalog.State.SecurityLabel(key), next.SecurityLabel(key)
		object := target
		object.SubID = 0
		oldObject, newObject := oldView.Get(object), newView.Get(object)
		if oldPresent && newPresent && oldObject == newObject && (old == replacement || old != nil && replacement != nil && *old == *replacement) {
			continue
		}
		if oldPresent {
			if oldObject == nil {
				return fmt.Errorf("stuntgres: missing old control label identity")
			}
			previous := policy.Unlabeled()
			if old != nil {
				previous = old.Label
			}
			if err := check(previous, catalog.SepgsqlClass(oldObject, target.SubID), catalog.SepgsqlObjectKey(oldView, oldObject, target.SubID), sepgsql.Setattr|sepgsql.RelabelFrom); err != nil {
				return err
			}
		}
		if newPresent {
			if newObject == nil {
				return fmt.Errorf("stuntgres: missing final control label identity")
			}
			context := policy.Unlabeled()
			if replacement != nil {
				context = replacement.Label
			}
			if err := check(context, catalog.SepgsqlClass(newObject, target.SubID), catalog.SepgsqlObjectKey(newView, newObject, target.SubID), sepgsql.RelabelTo); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// ReplaceCatalogWithControl is the public-control route. Sessionless engine
// callers retain their explicit trusted internal replacement boundary.
func (e *Engine) ReplaceCatalogWithControl(ctx context.Context, source *Engine, replacement catalog.Read, restoreOp *session.Operation, authority *ControlSepgsqlAuthority) (ChangeSet, error) {
	if err := e.ValidateControlSepgsql(authority); err != nil {
		return ChangeSet{}, err
	}
	return e.replaceCatalogContents(ctx, source, replacement, restoreOp, false, authority)
}
