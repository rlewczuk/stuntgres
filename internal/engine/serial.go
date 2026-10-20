package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
)

func (e *Engine) stageCreateSerials(stage *CatalogStage, table *catalog.Relation, declarations []analyze.ColumnDeclaration) error {
	for slot, declaration := range declarations {
		if !declaration.Serial {
			continue
		}
		options, err := sequence.SerialOptions(table.Columns[slot].Usage.Type.ID())
		if err != nil {
			return err
		}
		read := e.stageRead(stage)
		if read.MustNamespace(table.Namespace()).Temporary != (ids.SessionToken{}) {
			options.Persistence = "temporary"
		}
		name := ownedSequenceName(table.Name(), table.Columns[slot].Name, func(name string) bool {
			return name == table.Name() || len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: name})) != 0
		})
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
		options.Owned = table.Address()
		options.Owned.SubID = table.Attributes[slot]
		d := sequence.NewRelation(sequence.RelationSpec{Address: address, Namespace: table.Namespace(), Name: name, Revision: revision, Owner: table.Owner, Columns: sequence.Columns(e.catalog.read(e.catalog.state)), Options: options})
		d.Security.ACL = stage.creationACL(table.Owner, table.Namespace(), authorization.DefaultSequences)
		if err := stage.Create(d); err != nil {
			return err
		}
		stage.sequenceState().SetGenerator(address, sequence.InitialGenerator(options, nil, revision))
		// Route synthetic syntax through the same closed default binder as Restore.
		namespace, _ := read.Descriptor(table.Namespace())
		declarations[slot].DefaultExpr = sequence.SerialDefault(namespace.Name(), name)
	}
	return nil
}

func closedRegclassDefault(node expr.Expr) bool {
	if node == nil || node.Properties().RowDependent || node.Properties().ParameterDependent || node.Properties().StageDependent {
		return false
	}
	switch n := node.(type) {
	case plan.LiteralExpr:
		return true
	case plan.CoercionExpr:
		return closedRegclassDefault(n.Child)
	case plan.RowExpr:
		for _, field := range n.Fields {
			if !closedRegclassDefault(field) {
				return false
			}
		}
		return true
	case plan.FieldExpr:
		return closedRegclassDefault(n.Base)
	case plan.ArrayExpr:
		for _, element := range n.Elements {
			if !closedRegclassDefault(element) {
				return false
			}
		}
		return true
	case plan.SubscriptExpr:
		if !closedRegclassDefault(n.Base) {
			return false
		}
		for _, s := range n.Subscripts {
			if s.Lower != nil && !closedRegclassDefault(s.Lower) || s.Upper != nil && !closedRegclassDefault(s.Upper) {
				return false
			}
		}
		return true
	}
	return false
}
