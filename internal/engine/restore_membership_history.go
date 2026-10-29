package engine

import (
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

type restoreMembershipHistoryKey struct {
	root, extension ids.ObjectRef
	action          string
}

func clearRestoreMembershipHistory(op *session.Operation) {
	replay := core.OperationReplay(op)
	replay.ExtensionMembershipReplayRoot = ids.ObjectRef{}
	replay.ExtensionMembershipReplayExtension = ids.ObjectRef{}
	replay.ExtensionMembershipReplayAction = ""
}

// Only decoded native installation metadata can identify reconstruction rather
// than an ordinary SQL membership event. The executor still performs all normal
// ownership, membership and reciprocal-closure checks for the exact statement.
func prepareRestoreMembershipHistory(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	clearRestoreMembershipHistory(op)
	s, ok := stmt.(*parser.AlterExtension)
	if !ok || len(r.extensionOwners) == 0 || s.Action != "add" && s.Action != "drop" {
		return nil
	}
	e := r.stage
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	ext, ok := auth.Catalog.Extension(auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}))
	if !ok {
		return nil // Preserve the ordinary executor's missing-object diagnostic.
	}
	host := schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op}}
	root, err := regobj.ResolveExtensionMember(host, auth, s.Member, op.Session.DatabaseName)
	if err != nil || !control.NativeMembershipHistoryReplay(auth.Catalog, r.extensionOwners, root, ext, s.Action) {
		return nil
	}
	key := restoreMembershipHistoryKey{root: auth.Ref(root.Address()), extension: auth.Ref(ext.Address()), action: s.Action}
	if r.membershipHistoryRepairs[key] {
		return nil
	}
	if r.membershipHistoryRepairs == nil {
		r.membershipHistoryRepairs = make(map[restoreMembershipHistoryKey]bool)
	}
	r.membershipHistoryRepairs[key] = true
	replay := core.OperationReplay(op)
	replay.ExtensionMembershipReplayRoot = key.root
	replay.ExtensionMembershipReplayExtension = key.extension
	replay.ExtensionMembershipReplayAction = s.Action
	return nil
}
