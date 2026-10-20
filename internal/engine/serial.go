package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func (e *Engine) stageCreateSerials(stage *CatalogStage, table *catalog.Relation, declarations []analyze.ColumnDeclaration) error {
	for slot, declaration := range declarations {
		if !declaration.Serial {
			continue
		}
		_, _, typeName := sequence.TypeLimits(table.Columns[slot].Usage.Type.ID())
		options, _, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, []parser.SequenceOption{{Name: "as", Type: parser.TypeName{Name: typeName}}})
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
		sequence := &catalog.Relation{ObjectAddress: address, ObjectNamespace: table.Namespace(), ObjectName: name, ObjectRevision: revision, SequencePlanStamp: revision, Owner: table.Owner, Columns: e.sequenceColumnsLocked(), Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, RelationKind: catalog.SequenceRelation, Sequence: &options}
		sequence.Security.ACL = stage.creationACL(table.Owner, table.Namespace(), authorization.DefaultSequences)
		sequence.ColumnMeta.HighWater = 3
		if err := stage.Create(sequence); err != nil {
			return err
		}
		stage.sequenceState().setGenerator(address, &storage.SequenceGenerator{Last: options.Start, Generation: revision})
		// Route synthetic syntax through the same closed default binder as Restore.
		namespace, _ := read.Descriptor(table.Namespace())
		declarations[slot].DefaultExpr = parser.CallExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "nextval"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(name)}}}}
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
