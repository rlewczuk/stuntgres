package schemacmds

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func extensionAlterError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

// ExecuteExtensionAlter keeps membership and relocation in the existing
// statement-owned candidate/publication path, including its unlocked validators.
func ExecuteExtensionAlter(e *core.Engine, scope *core.StatementScope, s *parser.AlterExtension, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	stage, err := stageExtensionAlterLocked(e, scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		if stage != nil {
			e.DiscardCatalogStage(stage)
		}
		return core.Result{}, err
	}
	return publishExtensionStage(e, scope, stage)
}

func stageExtensionAlterLocked(e *core.Engine, scope *core.StatementScope, s *parser.AlterExtension) (*core.CatalogStage, error) {
	if err := scope.Op.Context().Err(); err != nil {
		return nil, err
	}
	if s.Action != "schema" && s.Member.Kind == "invalid" {
		return nil, extensionAlterError("42P17", "cannot add an object of this type to an extension")
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	old, ok := auth.Catalog.Extension(auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}))
	if !ok {
		return nil, extensionAlterError("42704", fmt.Sprintf("extension %q does not exist", s.Name.Text))
	}
	h := &extensionHost{Engine: e, Op: scope.Op, Auth: auth}
	h.BeginStage(old.TargetNamespace)
	h.predecessor = old
	if s.Action == "schema" {
		return h.Stage, stageExtensionSchemaLocked(h, old, s.Schema)
	}
	if err := auth.View.Check(auth, access.Requirement{Object: auth.Owner.Domain.Ref(auth.Catalog.Epoch(), old.Address()), OwnerOnly: true, OwnerNoun: "extension"}); err != nil {
		return h.Stage, err
	}
	host := RoutineHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: h.Stage}}
	root, err := regobj.ResolveExtensionMember(host, auth, s.Member, scope.Op.Session.DatabaseName)
	if err != nil {
		return h.Stage, err
	}
	if err := extensionMemberAuthority(host, auth, root, s.Member); err != nil {
		return h.Stage, err
	}
	if s.Action == "add" && root.Address() == old.TargetNamespace {
		return h.Stage, extensionAlterError("55000", fmt.Sprintf("cannot add schema %q to extension %q because the schema contains the extension", root.Name(), old.Name()))
	}
	closure, err := catalog.ExtensionMemberClosure(auth.Catalog, root)
	if err != nil {
		return h.Stage, err
	}
	for _, d := range closure {
		parent := catalog.ExtensionMembership(d)
		if s.Action == "add" && parent == (ids.ObjectAddress{}) || s.Action == "drop" && parent == old.Address() {
			continue
		}
		description, err := host.DependencyObject(auth, d.Address())
		if err != nil {
			return h.Stage, err
		}
		if s.Action == "add" {
			incumbent, ok := auth.Catalog.Extension(parent)
			if !ok {
				return h.Stage, fmt.Errorf("stuntgres: missing incumbent extension")
			}
			return h.Stage, extensionAlterError("55000", fmt.Sprintf("%s is already a member of extension %q", description, incumbent.Name()))
		}
		return h.Stage, extensionAlterError("55000", fmt.Sprintf("%s is not a member of extension %q", description, old.Name()))
	}
	// Native provenance repair replays an already-proven owners-v3 membership,
	// not a new user ADD/DROP event. Only its exact statement-local identities
	// may retain history while the normal authorization/membership edits run.
	replay := core.OperationReplayOrZero(scope.Op)
	preserveHistory := e.ImportTargets != nil && replay.StagingEffects &&
		replay.ExtensionMembershipReplayAction == s.Action &&
		replay.ExtensionMembershipReplayRoot == auth.Owner.Domain.Ref(auth.Catalog.Epoch(), root.Address()) &&
		replay.ExtensionMembershipReplayExtension == auth.Owner.Domain.Ref(auth.Catalog.Epoch(), old.Address())
	next := *old
	next.Members = slices.Clone(old.Members)
	parent := old.Address()
	if s.Action == "drop" {
		parent = ids.ObjectAddress{}
	}
	for _, d := range closure {
		revision, err := h.Revision()
		if err != nil {
			return h.Stage, err
		}
		replacement, err := catalog.WithExtensionMembership(d, parent, revision)
		if err != nil {
			return h.Stage, err
		}
		if err := h.Replace(replacement); err != nil {
			return h.Stage, err
		}
		if s.Action == "add" {
			next.Members = append(next.Members, d.Address())
			if !preserveHistory {
				if err := recordExtensionMemberPrivileges(h.Stage, auth.Catalog, d); err != nil {
					return h.Stage, err
				}
			}
		} else {
			if !preserveHistory {
				h.Stage.RemoveInitialPrivileges(d.Address())
			}
			for i, a := range next.Members {
				if a == d.Address() {
					next.Members = slices.Delete(next.Members, i, i+1)
					break
				}
			}
		}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return h.Stage, err
	}
	if err := ddl.ReplaceCommand(h.Stage, "ALTER EXTENSION", &next); err != nil {
		return h.Stage, err
	}
	return h.Stage, host.RecordDependencyPath(auth)
}

func extensionMemberAuthority(host RoutineHost, auth core.AuthorizationView, d catalog.Descriptor, target parser.ExtensionMemberTarget) error {
	actor, err := auth.View.Roles.Descriptor(auth.View.State.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return nil
	}
	switch d.(type) {
	case *catalog.TSParser, *catalog.TSTemplate:
		return extensionAlterError("42501", "must be superuser")
	case *catalog.AccessMethod:
		return extensionAlterError("42501", "must be superuser to alter access methods")
	}
	if cast, ok := d.(*catalog.Cast); ok {
		for _, id := range [2]ids.OID{ids.OID(cast.Source), ids.OID(cast.Target)} {
			if err := auth.View.Check(auth, access.Requirement{Object: auth.Owner.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}), OwnerOnly: true}); err == nil {
				return nil
			} else if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "42501" {
				return err
			}
		}
		return extensionAlterError("42501", "must be owner of either source or target type")
	}
	err = auth.View.Check(auth, access.Requirement{Object: auth.Owner.Domain.Ref(auth.Catalog.Epoch(), d.Address()), OwnerOnly: true, OwnerNoun: target.Kind})
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
		if _, routine := d.(*catalog.Procedure); routine {
			return extensionAlterError("42501", "must be owner of function "+catalog.ObjectNameText(target.Routine.Name))
		}
		if _, configuration := d.(*catalog.TSConfiguration); configuration {
			return extensionAlterError("42501", "must be owner of text search configuration "+catalog.ObjectNameText(target.Name))
		}
	}
	return err
}
