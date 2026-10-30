package table

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
)

// StageCreateSerials stages the owned sequence of every serial column and
// gives the column its nextval DEFAULT.
func StageCreateSerials(h Host, auth ddl.Authority, relation *catalog.Relation, declarations []analyze.ColumnDeclaration) error {
	for slot, declaration := range declarations {
		if !declaration.Serial {
			continue
		}
		options, err := sequence.SerialOptions(relation.Columns[slot].Usage.Type.ID())
		if err != nil {
			return err
		}
		read := h.StageRead()
		options.Persistence = EffectivePersistence(read, relation)
		if read.MustNamespace(relation.Namespace()).Temporary != (ids.SessionToken{}) {
			options.Persistence = "temporary"
		}
		if err := h.CheckCreationTablespace(auth, options.Persistence, false); err != nil {
			return err
		}
		name := ownedSequenceName(relation.Name(), relation.Columns[slot].Name, func(name string) bool {
			return name == relation.Name() || len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: relation.Namespace(), Name: name})) != 0
		})
		oid, err := h.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := h.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
		options.Owned = relation.Address()
		options.Owned.SubID = relation.Attributes[slot]
		d := sequence.NewRelation(sequence.RelationSpec{Address: address, Namespace: relation.Namespace(), Name: name, Revision: revision, Owner: relation.Owner, Columns: h.SequenceColumns(), Options: options})
		d.Security.ACL = h.CreationACL(relation.Owner, relation.Namespace(), authorization.DefaultSequences)
		if err := ddl.CreateCommand(h.Stage(), "CREATE SEQUENCE", d); err != nil {
			return err
		}
		h.SequenceStage().SetGenerator(address, sequence.InitialGenerator(options, nil, revision))
		// Route synthetic syntax through the same closed default binder as Restore.
		namespace, _ := read.Descriptor(relation.Namespace())
		declarations[slot].DefaultExpr = sequence.SerialDefault(namespace.Name(), name)
		ddl.RecordCommand(h.Stage(), "ALTER SEQUENCE", address)
	}
	return nil
}
