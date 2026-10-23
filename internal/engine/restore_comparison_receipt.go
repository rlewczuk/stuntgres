package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// Only checked roots published by this exact parsed statement can satisfy a
// window. An unchanged earlier capture on the same relation is not a receipt.
func (e *Engine) validateComparisonBindingStatement(op *session.Operation, stmt parser.Statement, before catalog.Read, namespace ids.ObjectAddress, position int) error {
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: auth.Owner.Domain.Ref(auth.Catalog.Epoch(), namespace), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
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
			return control.InvalidComparisonContext()
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
		return control.InvalidComparisonContext()
	}
	if len(target.Parts) != 2 {
		return control.InvalidComparisonContext()
	}
	found := false
	for address, d := range auth.Catalog.Descriptors() {
		previous := before.Get(address)
		if previous == d {
			continue
		}
		if d.Name() != target.Parts[1].Text {
			continue
		}
		ns, ok := auth.Catalog.Namespace(d.Namespace())
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
			required, err := stored.ComparisonBindingNamespace(auth.Catalog, node)
			if err != nil {
				failure = err
				return
			}
			if required != (ids.ObjectAddress{}) {
				if required != namespace {
					failure = control.InvalidComparisonContext()
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
		return control.InvalidComparisonContext()
	}
	return nil
}
