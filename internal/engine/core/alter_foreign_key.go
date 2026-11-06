package core

import (
	"cmp"
	"errors"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) acquireAlterForeignKeys(raw *parser.AlterTable) error {
	count := 0
	for _, action := range raw.Actions {
		if action.Action == parser.AddForeignKey || action.Action == parser.ValidateConstraint || parser.IsAlterConstraint(action.Action) && action.EnforcementSet {
			count++
		}
		if action.Action == parser.AddColumn {
			for _, constraint := range action.Definition.Constraints {
				if constraint.ForeignKey != nil {
					count++
				}
			}
		}
	}
	if count == 0 {
		return nil
	}
	e := s.Engine()
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	childRef, err := e.ResolveRelationReference(auth, raw.Name, true)
	if err != nil {
		e.Mu.Unlock()
		if raw.IfExists && table.RenameMissing(err) {
			return nil
		}
		return err
	}
	child := auth.Catalog.MustRelation(childRef.Address)
	if child.RelationKind == catalog.ForeignRelation {
		e.Mu.Unlock()
		return nil // Foreign declaration admission has no local FK/validation scan.
	}
	if err = auth.Check(access.Requirement{Object: childRef, OwnerOnly: true}); err == nil {
		label := "VALIDATE CONSTRAINT"
		if slices.ContainsFunc(raw.Actions, func(a parser.AlterTableAction) bool {
			return a.Action == parser.AddForeignKey || a.Action == parser.AddColumn
		}) {
			label = "ADD CONSTRAINT"
		} else if slices.ContainsFunc(raw.Actions, func(a parser.AlterTableAction) bool {
			return parser.IsAlterConstraint(a.Action) && a.EnforcementSet
		}) {
			label = "ALTER CONSTRAINT"
		}
		err = analyze.TableKindError(child, label)
	}
	if err == nil {
		err = table.InheritanceAlterActionsGuard(auth.Catalog, child, raw.Actions)
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	declarations := table.AlterForeignKeyDeclarations(auth.Catalog, child, raw.Actions)
	captures := make([]table.AlterForeignKeyAddition, 0, len(declarations))
	var validations []table.AlterForeignKeyAdmission
	// Exact descriptor originals belong to stored-key validation only. ADD
	// captures names/storage/supporting keys, then reopens current REFERENCES
	// authority during ordinary definition cooking after its unlocked waits.
	// An ACL replacement must not preempt that privilege diagnostic.
	originals := make(map[ids.ObjectAddress]catalog.Descriptor)
	modes := map[ids.ObjectAddress]lock.Mode{childRef.Address: table.AlterTableLockMode(raw)}
	// Mode values are independent bits, not strength ranks. Keep the canonical
	// strongest relation grant in this admission's RS/SUE/SRE/AX subset, including
	// a self-reference whose parent is already the ALTER target.
	requireParentMode := func(address ids.ObjectAddress, requested lock.Mode) {
		switch modes[address] {
		case lock.AccessExclusive:
			return
		case lock.ShareRowExclusive:
			if requested != lock.AccessExclusive {
				return
			}
		case lock.ShareUpdateExclusive:
			if requested == lock.RowShare {
				return
			}
		}
		modes[address] = requested
	}
	refs := []ids.ObjectRef{childRef}
	stores := map[ids.ObjectAddress]*storage.Relation{childRef.Address: e.PrivateStorageLocked(s.PendingCatalog(), childRef.Address)}
	enforcement := make(map[ids.OID]bool)
	type pendingEnforcement struct {
		target   ids.ObjectRef
		negative bool
	}
	pending := make(map[string]pendingEnforcement)
	// Every admitted ADD definition exists before MISC, including textually
	// later column REFERENCES. Capture exactly the projected declaration order.
	for _, declaration := range declarations {
		ref, resolveErr := e.ResolveReference(auth, declaration.Definition.Target)
		if resolveErr != nil {
			e.Mu.Unlock()
			return table.AlterCookingError(resolveErr)
		}
		capture := table.AlterForeignKeyAddition{
			AlterForeignKeyDeclaration: declaration,
			AlterForeignKeyAdmission: table.AlterForeignKeyAdmission{
				Target:  ref,
				Storage: e.PrivateStorageLocked(s.PendingCatalog(), ref.Address),
			},
		}
		if ref != childRef {
			capture.Index = table.AlterForeignKeyIndex(auth.Catalog, ref.Address, declaration.Definition)
		}
		captures = append(captures, capture)
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
			stores[ref.Address] = capture.Storage
		}
		if ref != childRef {
			requireParentMode(ref.Address, lock.ShareRowExclusive)
		}
		pending[capture.Name] = pendingEnforcement{target: ref, negative: declaration.Definition.NotEnforced}
	}
	for _, action := range raw.Actions {
		alterEnforcement := parser.IsAlterConstraint(action.Action) && action.EnforcementSet
		if action.Action == parser.ValidateConstraint || alterEnforcement {
			if declared, ok := pending[action.Constraint.Text]; ok {
				if alterEnforcement && declared.negative != action.NotEnforced {
					mode := lock.ShareRowExclusive
					if action.NotEnforced {
						mode = lock.AccessExclusive
					}
					requireParentMode(declared.target.Address, mode)
					declared.negative = action.NotEnforced
					pending[action.Constraint.Text] = declared
				}
				continue
			}
			// Earlier DROP/ADD may replace the name; only an authentic incumbent
			// invalid FK contributes existing-key admission here.
			selected, selectErr := table.SelectAlterConstraintValidation(child, action.Constraint.Text)
			if selectErr != nil || selected.ForeignKey == nil {
				continue
			}
			mode := lock.RowShare
			if alterEnforcement {
				previous, known := enforcement[selected.OID]
				if !known {
					previous = selected.ForeignKey.NotEnforced
				}
				enforcement[selected.OID] = action.NotEnforced
				if previous == action.NotEnforced {
					continue
				}
				mode = lock.ShareRowExclusive
				if action.NotEnforced {
					mode = lock.AccessExclusive
				}
			} else if !selected.Invalid() || selected.ForeignKey.NotEnforced {
				continue
			}
			fk := selected.ForeignKey
			ref := childRef
			ref.Address = fk.Target
			parent, ok := auth.Catalog.Relation(ref.Address)
			if !ok {
				e.Mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			provider, ok := auth.Catalog.Relation(fk.Index)
			if !ok {
				e.Mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			originals[fk.Index] = provider
			originals[childRef.Address] = child
			originals[ref.Address] = parent
			capture := table.AlterForeignKeyAdmission{Target: ref, Storage: e.PrivateStorageLocked(s.PendingCatalog(), ref.Address), Index: fk.Index, Constraint: fk.OID, Parent: parent, Provider: provider}
			validations = append(validations, capture)
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
				stores[ref.Address] = capture.Storage
			}
			requireParentMode(ref.Address, mode)
		}
	}
	e.Mu.Unlock()
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		mode := modes[ref.Address]
		if err := s.ForeignKeyGrant(ref, storage.RowVersion{}, mode, stores[ref.Address]); err != nil {
			return err
		}
	}
	auth, err = OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	sameName := func(name parser.ObjectName, expected ids.ObjectRef) error {
		current, resolveErr := e.ResolveReference(auth, name)
		if resolveErr != nil {
			var diagnostic *sqlstate.Error
			if errors.As(resolveErr, &diagnostic) && diagnostic.Code == "42501" {
				return resolveErr
			}
			return ids.ErrCatalogReplaced
		}
		if current != expected {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	if err := sameName(raw.Name, childRef); err != nil {
		return err
	}
	current := table.AlterForeignKeyDeclarations(auth.Catalog, auth.Catalog.MustRelation(childRef.Address), raw.Actions)
	if len(current) != len(captures) {
		return ids.ErrCatalogReplaced
	}
	for i := range captures {
		if !current[i].Equal(&captures[i].AlterForeignKeyDeclaration) {
			return ids.ErrCatalogReplaced
		}
	}
	for address, original := range originals {
		if auth.Catalog.Get(address) != original {
			return ids.ErrCatalogReplaced
		}
	}
	for _, capture := range validations {
		if _, ok := auth.Catalog.Relation(capture.Index); !ok {
			return ids.ErrCatalogReplaced
		}
	}
	for i := range captures {
		capture := &captures[i]
		if err := sameName(capture.Definition.Target, capture.Target); err != nil {
			return err
		}
		if capture.Target != childRef && table.AlterForeignKeyIndex(auth.Catalog, capture.Target.Address, capture.Definition) != capture.Index {
			return ids.ErrCatalogReplaced
		}
		if capture.Index != (ids.ObjectAddress{}) {
			if _, ok := auth.Catalog.Relation(capture.Index); !ok {
				return ids.ErrCatalogReplaced
			}
		}
	}
	s.foreignKeys().AlterKeys = captures
	s.foreignKeys().ValidationKeys = validations
	return nil
}
