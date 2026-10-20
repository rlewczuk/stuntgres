package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func stageOperatorDescriptor(h routine.DDLHost, d *catalog.Operator) error {
	stage := h.Stage()
	if _, exists := h.StageBase().Find(d.Address()); exists {
		return stage.Replace(d)
	}
	if old := stage.Get(d.Address()); old != nil {
		if catalog.DescriptorNameKey(old) != catalog.DescriptorNameKey(d) {
			return catalog.Conflict{}
		}
		stage.UpdateDescriptor(d)
		return nil
	}
	return stage.Create(d)
}

// resolveOperatorLink captures an exact peer once, including shell/owner checks.
// Only CREATE may create a shell here; ALTER resolution never changes the stage.
func resolveOperatorLink(h routine.DDLHost, auth routine.Authority, d *catalog.Operator, name parser.ObjectName, negator, allowShell bool) (*catalog.Operator, error) {
	if len(name.Parts) == 0 {
		return nil, nil
	}
	symbol := name.Parts[len(name.Parts)-1].Text
	if !routine.ValidOperatorName(symbol) {
		return nil, sqlError("42602", fmt.Sprintf(`"%s" is not a valid operator name`, symbol))
	}
	left, right := d.Right, d.Left
	if negator {
		left, right = d.Left, d.Right
	}
	path, err := h.OperatorNamespaces(auth, name)
	if err != nil {
		// CREATE's catalog peer lookup has no expression error location.
		// Keep ALTER's existing reference diagnostic context unchanged.
		if diagnostic, ok := err.(*sqlstate.Error); ok && allowShell && diagnostic.Position != "" {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return nil, err
	}
	view := h.StageRead()
	var peer *catalog.Operator
	key := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectName: symbol, Left: left, Right: right}
	for _, namespace := range path {
		key.ObjectNamespace = namespace
		if found, ok := view.Operator(view.NameAddress(catalog.DescriptorNameKey(&key))); ok {
			peer = found
			break
		}
	}
	if peer != nil && !allowShell && peer.Function == 0 {
		return nil, sqlError("42883", "operator is only a shell: "+routine.OperatorInputName(h.AccessView(auth).Catalog, left, right, name))
	}
	if peer == nil {
		if !allowShell {
			return nil, sqlError("42883", "operator does not exist: "+routine.OperatorInputName(h.AccessView(auth).Catalog, left, right, name))
		}
		// An implicit namespace recaptures this helper's authority only.
		var ns ids.ObjectRef
		ns, auth, err = h.CreationNamespace(auth, name)
		if err != nil {
			return nil, err
		}
		// CREATE resolves peers before staging the source. A reference to the
		// source itself needs its reserved identity, not a second shell.
		if ns.Address == d.Namespace() && symbol == d.Name() && left == d.Left && right == d.Right {
			return d, nil
		}
		if err = h.AccessView(auth).CheckCreate(auth, ns); err != nil {
			return nil, err
		}
		id, err := h.AllocateOID()
		if err != nil {
			return nil, err
		}
		rev, err := h.Revision()
		if err != nil {
			return nil, err
		}
		peer = &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}, ObjectNamespace: ns.Address, ObjectRevision: rev, ObjectName: symbol, Left: left, Right: right, Owner: h.AccessView(auth).State.CurrentUser}
		if err = h.Stage().Create(peer); err != nil {
			return nil, err
		}
	} else if peer.Address() != d.Address() {
		peerAuth := h.Recapture(auth, view)
		if err = ddlCheck(h, peerAuth, access.Requirement{Object: ddlRef(h, auth, peer.Address()), OwnerOnly: true}); err != nil {
			return nil, err
		}
	}
	return peer, nil
}

// Stage by captured identity, reading the latest private peer so two link kinds
// targeting the same operator retain each other's reciprocal updates.
func stageResolvedOperatorLink(h routine.DDLHost, d, peer *catalog.Operator, negator bool) error {
	if peer == nil {
		return nil
	}
	view := h.StageRead()
	peer = view.MustOperator(peer.Address())
	if peer.Address() == d.Address() {
		if negator {
			return sqlError("42P13", "operator cannot be its own negator")
		}
		d.Commutator = d.Address().ObjectID
		return nil
	}
	next := *peer
	link := peer.Commutator
	attribute := "commutator"
	if negator {
		link = peer.Negator
		attribute = "negator"
	}
	if link != 0 && link != d.Address().ObjectID {
		third, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: link})
		if !ok {
			return fmt.Errorf("stuntgres: missing reciprocal operator")
		}
		return sqlError("42P13", attribute+" operator "+peer.Name()+" is already the "+attribute+" of operator "+third.Name())
	}
	if negator {
		d.Negator = peer.Address().ObjectID
		next.Negator = d.Address().ObjectID
	} else {
		d.Commutator = peer.Address().ObjectID
		next.Commutator = d.Address().ObjectID
	}
	if next == *peer {
		return nil
	}
	var err error
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return stageOperatorDescriptor(h, &next)
}
