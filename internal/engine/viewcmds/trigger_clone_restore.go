package viewcmds

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// RestoredTriggerClone captures the actual child identity, not a source OID.
type RestoredTriggerClone struct {
	Record stored.TriggerCloneReplay
	Child ids.ObjectRef
	OID ids.OID
	Owner authorization.PrincipalRef
}

func cloneReplayRelation(read catalog.Read, schema, name string) (*catalog.Relation, bool) {
	namespace := read.State().NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema})
	address := read.State().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	return read.Relation(address)
}

func CaptureRestoredTriggerClone(e *core.Engine, op *session.Operation, record stored.TriggerCloneReplay) (RestoredTriggerClone, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { return RestoredTriggerClone{}, err }
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || e.ImportTargets == nil { return RestoredTriggerClone{}, fmt.Errorf("stuntgres: invalid trigger clone import authority") }
	child, ok := cloneReplayRelation(auth.Catalog, record.ChildSchema, record.ChildTable)
	if !ok || child.Owner != auth.State.CurrentUser { return RestoredTriggerClone{}, fmt.Errorf("stuntgres: invalid retained trigger clone owner") }
	for _, definition := range stored.TriggersOf(child) {
		p := definition.Parts()
		if p.Name == record.ChildTrigger && p.Level == stored.TriggerRow && p.Parent == (ids.ObjectAddress{}) {
			return RestoredTriggerClone{Record: record, Child: auth.Ref(child.Address()), OID: p.OID, Owner: child.Owner}, nil
		}
	}
	return RestoredTriggerClone{}, fmt.Errorf("stuntgres: missing local retained trigger clone")
}

// CheckRestoredTriggerCloneCreate fences the annotated CREATE's lifetime.
// OIDs survive OR REPLACE, whereas later dependency-only ALTERs are legitimate.
func CheckRestoredTriggerCloneCreate(e *core.Engine, op *session.Operation, trigger *parser.CreateTrigger, captures []RestoredTriggerClone) error {
	if !trigger.Replace || len(captures) == 0 { return nil }
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { return err }
	ref, err := e.ResolveRelationReference(auth, trigger.Table, true)
	if err != nil { return err }
	for _, capture := range captures {
		if ref == capture.Child && trigger.Name.Text == capture.Record.ChildTrigger {
			return fmt.Errorf("stuntgres: replaced retained trigger clone")
		}
	}
	return nil
}

// ImportTriggerCloneLinks runs only against Restore's isolated engine, after
// all real CREATEs. One checked publication reinstates native dependency links.
func ImportTriggerCloneLinks(e *core.Engine, ctx context.Context, op *session.Operation, records []RestoredTriggerClone) error {
	if len(records) == 0 { return nil }
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { e.Mu.Unlock(); return err }
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || e.ImportTargets == nil { e.Mu.Unlock(); return fmt.Errorf("stuntgres: invalid trigger clone import authority") }
	auth.State.CurrentUser = bootstrap.Ref
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	changes := make(map[ids.ObjectAddress]*catalog.Relation)
	for _, capture := range records {
		if err = ctx.Err(); err != nil { break }
		r := capture.Record
		child, ok := cloneReplayRelation(auth.Catalog, r.ChildSchema, r.ChildTable)
		parent, parentOK := cloneReplayRelation(auth.Catalog, r.ParentSchema, r.ParentTable)
		if !ok || !parentOK || auth.Ref(child.Address()) != capture.Child || child.Owner != capture.Owner || child.Membership == nil || child.Membership.Parent != parent.Address() {
			err = fmt.Errorf("stuntgres: invalid retained trigger clone membership"); break
		}
		if _, ok := e.ImportTargets[child.Owner]; !ok { err = fmt.Errorf("stuntgres: uncaptured trigger clone owner"); break }
		if _, ok := e.ImportTargets[parent.Owner]; !ok { err = fmt.Errorf("stuntgres: uncaptured trigger parent owner"); break }
		var source stored.Trigger
		for _, definition := range stored.TriggersOf(parent) { if definition.Name() == r.ParentTrigger { source = definition; break } }
		if source.OID() == 0 || source.Parts().Level != stored.TriggerStatement { err = fmt.Errorf("stuntgres: invalid retained trigger clone parent"); break }
		next := changes[child.Address()]
		if next == nil { copy := *child; next = &copy; changes[child.Address()] = next }
		list := slices.Clone(stored.TriggersOf(next))
		index := slices.IndexFunc(list, func(definition stored.Trigger) bool { return definition.OID() == capture.OID && definition.Name() == r.ChildTrigger })
		if index < 0 { err = fmt.Errorf("stuntgres: replaced retained trigger clone"); break }
		parts := list[index].Parts()
		if parts.Parent != (ids.ObjectAddress{}) || parts.Level != stored.TriggerRow { err = fmt.Errorf("stuntgres: invalid retained trigger clone kind"); break }
		parts.Parent, parts.ParentRevision = ids.ObjectAddress{ClassID: 2620, ObjectID: source.OID()}, parent.Revision()
		list[index], err = stored.NewTrigger(parts)
		if err != nil { break }
		next.Triggers = stored.TriggersPayload(list)
	}
	if err == nil {
		for _, next := range changes {
			next.ObjectRevision, err = e.Catalog.RevisionLocked()
			if err == nil { err = stage.Replace(next) }
			if err != nil { break }
		}
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil { return err }
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil { return err }
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: auth.Catalog.Epoch()})
	return err
}
