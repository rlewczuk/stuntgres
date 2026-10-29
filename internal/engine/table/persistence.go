package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// EffectivePersistence normalizes legacy descriptors and namespace-owned TEMP.
func EffectivePersistence(view catalog.Read, relation *catalog.Relation) string {
	if ns, ok := view.Namespace(relation.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
		return "temporary"
	}
	if relation.Sequence != nil && relation.Sequence.Persistence != "" {
		return relation.Sequence.Persistence
	}
	if relation.Persistence == "unlogged" {
		return "unlogged"
	}
	return "logged"
}

type PersistencePreparation struct {
	Changed     bool
	Destination string
}

// PreparePersistence deliberately consults the original catalog, before DROP or
// ADD foreign keys. Same-state requests precede FK checks, but not the repeated
// genuine-change guard. Execution installs the intention only after definitions.
func PreparePersistence(view catalog.Read, original *catalog.Relation, action parser.AlterTableActionKind, prior PersistencePreparation) (PersistencePreparation, error) {
	if prior.Changed {
		return prior, sqlError("0A000", "cannot change persistence setting twice")
	}
	current := EffectivePersistence(view, original)
	if current == "temporary" {
		err := sqlError("42P16", fmt.Sprintf(`cannot change logged status of table "%s" because it is temporary`, original.Name())).(*sqlstate.Error)
		err.Schema = view.MustNamespace(original.Namespace()).Name()
		err.Table = original.Name()
		return prior, err
	}
	destination := "logged"
	if action == parser.SetUnlogged {
		destination = "unlogged"
	}
	if current == destination {
		return prior, nil
	}
	if destination == "unlogged" {
		for _, mapping := range view.PublicationRelations() {
			if mapping.Relation == original.Address() {
				return prior, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`cannot change table "%s" to unlogged because it is part of a publication`, original.Name()), Detail: "Unlogged relations cannot be replicated."}
			}
		}
	}
	if original.Sequence != nil {
		return PersistencePreparation{Changed: true, Destination: destination}, nil
	}
	for _, child := range view.Relations() {
		for _, fk := range child.Constraints.ForeignKeys {
			if child.Address() == fk.Target {
				continue
			}
			parent, ok := view.Relation(fk.Target)
			if !ok {
				continue
			}
			other := parent
			blocked := destination == "logged" && child.Address() == original.Address() && EffectivePersistence(view, parent) == "unlogged"
			if destination == "unlogged" && parent.Address() == original.Address() && EffectivePersistence(view, child) == "logged" {
				blocked = true
				other = child
			}
			if !blocked {
				continue
			}
			otherIdentity := "unlogged"
			if destination == "unlogged" {
				otherIdentity = "logged"
			}
			err := sqlError("42P16", fmt.Sprintf(`could not change table "%s" to %s because it references %s table "%s"`, original.Name(), destination, otherIdentity, other.Name())).(*sqlstate.Error)
			err.Schema = view.MustNamespace(original.Namespace()).Name()
			err.Table = original.Name()
			err.Constraint = fk.Name
			return prior, err
		}
	}
	return PersistencePreparation{Changed: true, Destination: destination}, nil
}

func persistenceKindError(relation *catalog.Relation, action, kind string) error {
	err := sqlError("42809", fmt.Sprintf(`ALTER action %s cannot be performed on relation "%s"`, action, relation.Name())).(*sqlstate.Error)
	err.Detail = "This operation is not supported for " + kind + "."
	return err
}

func persistenceCompositeError(relation *catalog.Relation) error {
	err := sqlError("42809", fmt.Sprintf(`"%s" is a composite type`, relation.Name())).(*sqlstate.Error)
	err.Hint = "Use ALTER TYPE instead."
	return err
}
