package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// ValidateComparisonBinding checks that stmt published, in current, a
// definition whose expressions at or after position bind comparisons only
// through namespace. Only checked roots published by this exact parsed
// statement can satisfy a window: a descriptor unchanged since before is not
// a receipt.
func ValidateComparisonBinding(current, before catalog.Read, stmt parser.Statement, namespace ids.ObjectAddress, position int) error {
	var target parser.ObjectName
	var kind uint8
	switch s := stmt.(type) {
	case *parser.CreateView:
		target = s.Name
		kind = 1
	case *parser.CreateQueryTable:
		target = s.Name
		kind = 1
	case *parser.CreateTable:
		target = s.Name
		kind = 1
	case *parser.CreatePolicy:
		target = s.Table
		kind = 1
	case *parser.CreateTrigger:
		target = s.Table
		kind = 1
	case *parser.CreateRule:
		target = s.Table
		kind = 1
	case *parser.AlterTable:
		target = s.Name
		kind = 1
	case *parser.AlterTableDefault:
		target = s.Name
		kind = 1
	case *parser.ForeignDDL:
		target = s.Name
		kind = 1
	case *parser.CreateIndex:
		target = s.Table
		kind = 1
		target.Parts = append([]parser.Identifier(nil), target.Parts...)
		if len(target.Parts) != 2 {
			return InvalidComparisonContext()
		}
		target.Parts[1] = s.Name
	case *parser.CreateDomain:
		target = s.Name
		kind = 2
	case *parser.AlterDomain:
		target = s.Name
		kind = 2
	case *parser.CreateFunction:
		target = s.Name
		kind = 3
	default:
		return InvalidComparisonContext()
	}
	if len(target.Parts) != 2 {
		return InvalidComparisonContext()
	}
	found := false
	for address, d := range current.Descriptors() {
		previous := before.Get(address)
		if previous == d {
			continue
		}
		if d.Name() != target.Parts[1].Text {
			continue
		}
		ns, ok := current.Namespace(d.Namespace())
		if !ok || ns.Name() != target.Parts[0].Text {
			continue
		}
		switch d.(type) {
		case *catalog.Relation:
			if kind != 1 {
				continue
			}
		case *catalog.Type:
			if kind != 2 {
				continue
			}
		case *catalog.Procedure:
			if kind != 3 {
				continue
			}
		default:
			continue
		}
		var failure error
		err := stored.VisitDefinitionExpressions(d, func(node expr.Expr) {
			if failure != nil || node == nil {
				return
			}
			if node.SourcePosition().Offset < position {
				return
			}
			required, err := stored.ComparisonBindingNamespace(current, node, nil)
			if err != nil {
				failure = err
				return
			}
			if required != (ids.ObjectAddress{}) {
				if required != namespace {
					failure = InvalidComparisonContext()
					return
				}
				found = true
			}
		})
		if err != nil {
			return err
		}
		if failure != nil {
			return failure
		}
	}
	if !found {
		return InvalidComparisonContext()
	}
	return nil
}
