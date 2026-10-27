package schemacmds

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// CaptureRestoredStatisticsOwner accepts only the actual constructor outcome in
// an isolated Restore. Names or an IF NOT EXISTS skip cannot establish a pair.
func CaptureRestoredStatisticsOwner(e *core.Engine, op *session.Operation, created ids.ObjectRef, recipient authorization.PrincipalRef) (ddl.RestoredStatisticsOwner, error) {
	reject := func() (ddl.RestoredStatisticsOwner, error) {
		return ddl.RestoredStatisticsOwner{}, core.SQLError("0A000", ddl.StatisticsOwnerReplayBoundary)
	}
	if e == nil || op == nil {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return ddl.RestoredStatisticsOwner{}, err
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return ddl.RestoredStatisticsOwner{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return captureStatisticsOwnerLocked(e, op, auth, created, recipient)
}

func captureStatisticsOwnerLocked(e *core.Engine, op *session.Operation, auth core.AuthorizationView, created ids.ObjectRef, recipient authorization.PrincipalRef) (ddl.RestoredStatisticsOwner, error) {
	reject := func() (ddl.RestoredStatisticsOwner, error) {
		return ddl.RestoredStatisticsOwner{}, core.SQLError("0A000", ddl.StatisticsOwnerReplayBoundary)
	}
	if !core.PartitionConstraintReplayImport(e, op, auth) || created.Domain() != e.Catalog.Domain || created.Epoch != auth.Catalog.Epoch() || created.Address.ClassID != ids.StatisticExtClassID || created.Address.SubID != 0 {
		return reject()
	}
	d, ok := auth.Catalog.Statistics(created.Address)
	if !ok {
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return ddl.RestoredStatisticsOwner{}, err
	}
	if !actor.Superuser || d.Owner != actor.Ref || recipient == actor.Ref {
		return reject()
	}
	if _, ok := e.ImportTargets[recipient]; !ok {
		return reject()
	}
	if _, err = auth.Roles.Descriptor(recipient); err != nil {
		return reject()
	}
	ns, ok := auth.Catalog.Namespace(d.Namespace())
	if !ok || ns.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if err = (catalog.StatisticsProvider{}).Validate(auth.Catalog, d); err != nil {
		return reject()
	}
	return ddl.RestoredStatisticsOwner{Ref: created, Definition: d, Namespace: ns, Creator: actor.Ref, Recipient: recipient}, nil
}

// RestoreStatisticsOwner changes only the captured descriptor's owner in the
// private import catalog. Capture and replacement share one lock window.
func RestoreStatisticsOwner(e *core.Engine, op *session.Operation, raw *parser.AlterOwner, pair ddl.RestoredStatisticsOwner) error {
	reject := func() error { return core.SQLError("0A000", ddl.StatisticsOwnerReplayBoundary) }
	if e == nil || op == nil || raw == nil || raw.Kind != "statistics" || raw.IfExists || len(raw.Name.Parts) != 2 || raw.Target.Identity != "" || raw.Target.Name == "" || pair.Definition == nil {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current, err := captureStatisticsOwnerLocked(e, op, auth, pair.Ref, pair.Recipient)
	if err != nil {
		return err
	}
	if current != pair || raw.Name.Parts[0].Text != pair.Namespace.Name() || raw.Name.Parts[1].Text != pair.Definition.Name() {
		return reject()
	}
	owner, err := auth.Roles.Descriptor(pair.Recipient)
	if err != nil {
		return err
	}
	if raw.Target.Name != owner.Name {
		return reject()
	}
	next := catalog.CloneState(e.Catalog.State)
	d := *pair.Definition
	d.Owner = pair.Recipient
	d.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err != nil {
		return err
	}
	next.SetDescriptor(d.Address(), &d)
	if err = (catalog.StatisticsProvider{}).Validate(e.Catalog.Read(next), &d); err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	e.Catalog.PublishLocked(next)
	return nil
}
