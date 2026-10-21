package table

import (
	"fmt"
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
			options.Persistence = split.Persistence
			read := h.StageRead()
			tableNamespace := read.MustNamespace(relation.Namespace())
			if tableNamespace.Temporary != (ids.SessionToken{}) {
				auth = h.StagedTemporary(auth, tableNamespace)
				if err := split.CheckTemporary(); err != nil {
					return err
				}
				options.Persistence = "temporary"
			}
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

// StageIdentitySequence stages the sequence of an identity column of
// relation's slot: its OID, then its revision, its creation ACL and its
// initial generator.
func StageIdentitySequence(h Host, relation *catalog.Relation, slot int, name string, mode parser.IdentityMode, options catalog.SequenceOptions, restart *int64) error {
	namespace := h.StageBase().Get(relation.Namespace())
	if staged := h.Stage().Get(relation.Namespace()); staged != nil {
		namespace = staged
	}
	if namespace.(*catalog.Namespace).Temporary != (ids.SessionToken{}) {
		options.Persistence = "temporary"
	}
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
