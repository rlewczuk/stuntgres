package table

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func ownedSequenceName(relation, column string, used func(string) bool) string {
	return ddl.GeneratedRelationName(relation, column, "seq", used)
}

// StageCreateIdentities stages the sequence of every identity column of
// CREATE TABLE syntax. Both reciprocal descriptors and the initial generator
// belong to one CREATE stage.
func StageCreateIdentities(h Host, auth ddl.Authority, relation *catalog.Relation, syntax *parser.CreateTable) error {
	for _, element := range syntax.Elements {
		if element.Column == nil {
			continue
		}
		slot := relation.Index[element.Column.Name]
		for _, constraint := range element.Column.Constraints {
			if constraint.Identity == nil {
				continue
			}
			definition := constraint.Identity
			as, err := sequence.IdentityTypeOption(relation.Columns[slot].Usage.Type.ID())
			if err != nil {
				return err
			}
			split, err := sequence.SplitIdentityOptions(as, definition.Options)
			if err != nil {
				return err
			}
			options, restart, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, split.Raw)
			if err != nil {
				return err
			}
			read := h.StageRead()
			tableNamespace := read.MustNamespace(relation.Namespace())
			if tableNamespace.Temporary != (ids.SessionToken{}) {
				auth = h.StagedTemporary(auth, tableNamespace)
				if err := split.CheckTemporary(); err != nil {
					return err
				}
			}
			options.Persistence = split.ResolvePersistence(EffectivePersistence(read, relation))
			namespace := relation.Namespace()
			used := func(name string) bool {
				return len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})) != 0 || name == relation.Name()
			}
			name := ownedSequenceName(relation.Name(), relation.Columns[slot].Name, used)
			if explicit := split.Name; len(explicit.Parts) != 0 {
				auth = h.Recapture(auth, read)
				ns, err := h.ExistingCreationNamespace(auth, explicit)
				if err != nil {
					return err
				}
				if ns.Address != namespace {
					return sqlError("55000", "sequence must be in same schema as table it is linked to")
				}
				name = explicit.Parts[len(explicit.Parts)-1].Text
				if used(name) {
					return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
				}
			}
			if err := StageIdentitySequence(h, relation, slot, name, definition.Mode, options, restart); err != nil {
				return err
			}
		}
	}
	return nil
}

// StageIdentitySequence stages an identity sequence with already resolved
// persistence: its OID, revision, creation ACL and initial generator.
func StageIdentitySequence(h Host, relation *catalog.Relation, slot int, name string, mode parser.IdentityMode, options catalog.SequenceOptions, restart *int64) error {
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	rev, err := h.Revision()
	if err != nil {
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	options.Owned = relation.Address()
	options.Owned.SubID = relation.Attributes[slot]
	options.Identity = true
	d := sequence.NewRelation(sequence.RelationSpec{Address: address, Namespace: relation.Namespace(), Name: name, Revision: rev, Owner: relation.Owner, Columns: h.SequenceColumns(), Options: options})
	d.Security.ACL = h.CreationACL(relation.Owner, relation.Namespace(), authorization.DefaultSequences)
	if err := h.Stage().Create(d); err != nil {
		return err
	}
	if relation.ColumnMeta.Identities == nil {
		relation.ColumnMeta.Identities = make(map[int32]catalog.ColumnIdentity)
	}
	relation.ColumnMeta.Identities[relation.Attributes[slot]] = catalog.ColumnIdentity{Mode: mode, Sequence: address}
	h.SequenceStage().SetGenerator(address, sequence.InitialGenerator(options, restart, rev))
	return nil
}

// identityColumn is the slot of the column an ADD or SET identity action
// names, and the start of its diagnostics.
func identityColumn(old *catalog.Relation, action parser.AlterTableAction) (int, string, error) {
	slot, ok := old.Index[action.Column.Text]
	if catalog.IsSystemColumn(action.Column.Text) {
		return 0, "", sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, action.Column.Text))
	}
	if !ok {
		return 0, "", sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, old.Name()))
	}
	return slot, fmt.Sprintf(`column "%s" of relation "%s"`, action.Column.Text, old.Name()), nil
}

// AddIdentityCandidate is old with ADD GENERATED ... AS IDENTITY of action.
// PostgreSQL executes the sequence before-command before table guards, so the
// sequence is staged first; a failed guard does not change the old link.
func AddIdentityCandidate(h Host, auth ddl.Authority, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	slot, message, err := identityColumn(old, action)
	if err != nil {
		return old, err
	}
	attribute := old.Attributes[slot]
	_, exists := old.ColumnMeta.Identities[attribute]
	next := *old
	next.ColumnMeta.Identities = maps.Clone(old.ColumnMeta.Identities)
	column := &parser.Column{Name: action.Column.Text, Constraints: []parser.ColumnConstraint{{Identity: action.Identity}}}
	if err := StageCreateIdentities(h, auth, &next, &parser.CreateTable{Elements: []parser.TableElement{{Column: column}}}); err != nil {
		return old, err
	}
	if exists {
		return old, sqlError("55000", message+" is already an identity column")
	}
	if old.Columns[slot].Nullable {
		return old, sqlError("55000", message+" must be declared NOT NULL before identity can be added")
	}
	if _, ok := old.ColumnMeta.Defaults[attribute]; ok || old.ColumnMeta.Generations[attribute].Expression != nil {
		return old, sqlError("55000", message+" already has a default value")
	}
	return &next, nil
}

// PrepareIdentitySet checks ALTER COLUMN SET identity options and returns the
// column's attribute and identity. The engine then acquires the identity
// sequence and stages the options before FinishIdentitySet.
func PrepareIdentitySet(old *catalog.Relation, action parser.AlterTableAction) (int32, catalog.ColumnIdentity, error) {
	slot, message, err := identityColumn(old, action)
	if err != nil {
		return 0, catalog.ColumnIdentity{}, err
	}
	attribute := old.Attributes[slot]
	identity, exists := old.ColumnMeta.Identities[attribute]
	if !exists {
		return 0, catalog.ColumnIdentity{}, sqlError("55000", message+" is not an identity column")
	}
	if len(action.Identity.Options) != 0 {
		if err := sequence.CheckIdentitySetOptions(action.Identity.Options); err != nil {
			return 0, catalog.ColumnIdentity{}, err
		}
	}
	return attribute, identity, nil
}

// FinishIdentitySet is old with the SET identity mode of action. Numeric
// before-command errors take precedence over a utility mode conflict.
func FinishIdentitySet(old *catalog.Relation, attribute int32, identity catalog.ColumnIdentity, action parser.AlterTableAction) (*catalog.Relation, error) {
	definition := action.Identity
	if definition.ConflictingModes {
		return old, sqlError("42601", "conflicting or redundant options")
	}
	next := *old
	next.ColumnMeta.Identities = maps.Clone(old.ColumnMeta.Identities)
	if definition.Mode != parser.IdentityNone {
		identity.Mode = definition.Mode
		next.ColumnMeta.Identities[attribute] = identity
	}
	return &next, nil
}
