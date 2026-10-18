package engine

import (
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SERIAL is declaration syntax, never a catalog type or identity column.
func serialTypeName(name parser.TypeName) string {
	if name.Schema != nil {
		return ""
	}
	switch name.Name {
	case "smallserial", "serial2":
		return "int2"
	case "serial", "serial4":
		return "int4"
	case "bigserial", "serial8":
		return "int8"
	}
	return ""
}

func (e *Engine) stageCreateSerials(stage *CatalogStage, table *catalog.Relation, declarations []createColumnDeclaration) error {
	for slot, declaration := range declarations {
		if !declaration.serial {
			continue
		}
		_, _, typeName := sequenceTypeLimits(table.Columns[slot].Usage.Type.ID())
		options, _, err := bindSequenceOptions(nil, sequenceGenerator{}, []parser.SequenceOption{{Name: "as", Type: parser.TypeName{Name: typeName}}})
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
		sequence.ACL = stage.creationACL(table.Owner, table.Namespace(), authorization.DefaultSequences)
		sequence.AttributeHighWater = 3
		if err := stage.Create(sequence); err != nil {
			return err
		}
		if stage.generators == nil {
			stage.generators = make(map[ids.ObjectAddress]*sequenceGenerator)
		}
		stage.generators[address] = &sequenceGenerator{last: options.Start, generation: revision}
		// Route synthetic syntax through the same closed default binder as Restore.
		namespace, _ := read.Descriptor(table.Namespace())
		declarations[slot].defaultExpr = parser.CallExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "nextval"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: quoteIdentifier(namespace.Name()) + "." + quoteIdentifier(name)}}}}
	}
	return nil
}

// Only fixed nextval leaves remain volatile inside constructor/field/cast wrappers.
func closedSequenceDefault(node expr.Expr) bool {
	if node == nil || node.Properties().RowDependent || node.Properties().ParameterDependent || node.Properties().StageDependent {
		return false
	}
	switch n := node.(type) {
	case coercionExpression:
		return closedSequenceDefault(n.child)
	case rowExpression:
		for _, field := range n.fields {
			if field.Properties().Volatility > expr.Stable && !closedSequenceDefault(field) {
				return false
			}
		}
		return true
	case fieldExpression:
		return closedSequenceDefault(n.base)
	case sequenceCallExpression:
		if n.name != "nextval" || n.identity || len(n.args) != 1 || n.ref == (ids.ObjectRef{}) || n.ResultUsage().Type.ID() != sqltype.Int8 || n.Properties() != (expr.Properties{Volatility: expr.Volatile}) {
			return false
		}
		literal, ok := n.args[0].(literalExpression)
		if !ok || literal.value.IsNull() || literal.ResultUsage().Type.ID() != sqltype.Regclass || literal.Properties() != (expr.Properties{}) {
			return false
		}
		text, err := literal.value.Text()
		return err == nil && text == strconv.FormatUint(uint64(n.ref.Address.ObjectID), 10)
	}
	return false
}

func closedRegclassDefault(node expr.Expr) bool {
	if node == nil || node.Properties().RowDependent || node.Properties().ParameterDependent || node.Properties().StageDependent {
		return false
	}
	switch n := node.(type) {
	case literalExpression:
		return true
	case coercionExpression:
		return closedRegclassDefault(n.child)
	case rowExpression:
		for _, field := range n.fields {
			if !closedRegclassDefault(field) {
				return false
			}
		}
		return true
	case fieldExpression:
		return closedRegclassDefault(n.base)
	case arrayExpression:
		for _, element := range n.elements {
			if !closedRegclassDefault(element) {
				return false
			}
		}
		return true
	case subscriptExpression:
		if !closedRegclassDefault(n.base) {
			return false
		}
		for _, s := range n.subscripts {
			if s.lower != nil && !closedRegclassDefault(s.lower) || s.upper != nil && !closedRegclassDefault(s.upper) {
				return false
			}
		}
		return true
	}
	return false
}
