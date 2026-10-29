package partition

import (
	"context"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func deparseKey(ctx context.Context, r stored.Deparser, relation *catalog.Relation) (string, error) {
	p := relation.Partition
	if p == nil {
		return "", nil
	}
	if len(p.Keys) == 0 || len(relation.Columns) != len(relation.Attributes) {
		return "", invalidDeparse()
	}
	strategy, method := "RANGE", ids.OID(403)
	switch p.Strategy {
	case parser.PartitionRange:
	case parser.PartitionList:
		strategy = "LIST"
	case parser.PartitionHash:
		strategy, method = "HASH", 405
	default:
		return "", invalidDeparse()
	}
	var out strings.Builder
	out.WriteString(strategy + " (")
	for i, key := range p.Keys {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		class, err := deparseClass(r.View, key, method)
		if err != nil {
			return "", err
		}
		if i != 0 {
			out.WriteString(", ")
		}
		usage := key.Usage
		var text string
		if key.Derived != nil {
			if key.Derived.Definition == nil {
				return "", invalidDeparse()
			}
			usage = key.Derived.Definition.ResultUsage()
			text, err = r.SQL(ctx, key.Derived.Definition, stored.DeparseParent{})
			if !index.PGIndexFunctionExpression(key.Derived.Definition) {
				text = "(" + text + ")"
			}
		} else {
			slot := relation.IndexForAttribute(key.Attribute)
			if slot < 0 {
				return "", invalidDeparse()
			}
			usage = relation.Columns[slot].Usage
			text, err = r.Attribute(relation, key.Attribute)
		}
		if err != nil {
			return "", err
		}
		if usage.Type != key.Usage.Type {
			return "", invalidDeparse()
		}
		out.WriteString(text)
		if key.Usage.Collation.ID != usage.Collation.ID {
			name := r.CollationName(key.Usage.Collation.ID)
			if name == "" {
				return "", invalidDeparse()
			}
			out.WriteString(" COLLATE " + name)
		}
		defaultClass, exists := r.View.UniqueDefaultIndexClass(method, key.Usage.Type)
		if !exists || !key.Class.SameIdentity(defaultClass) {
			name, err := deparseClassName(r, class)
			if err != nil {
				return "", err
			}
			out.WriteByte(' ')
			out.WriteString(name)
		}
	}
	out.WriteByte(')')
	return out.String(), nil
}

// Presentation requires catalog class/family identity, not an executable index
// kernel. The latter is demanded separately only when a bound must be compared.
func deparseClass(view catalog.Read, key catalog.PartitionKeyEntry, method ids.OID) (*catalog.OperatorClass, error) {
	if !key.Usage.Valid() {
		return nil, invalidDeparse()
	}
	class, ok := view.OperatorClass(ids.OID(key.Class.OID()))
	if !ok || class.Method != method {
		return nil, invalidDeparse()
	}
	current, ok := view.IndexClass(class.Address().ObjectID)
	if !ok || !current.SameIdentity(key.Class) {
		return nil, invalidDeparse()
	}
	accepted := current.Accepts(key.Usage.Type)
	if class.InputType == 2277 {
		accepted = key.Usage.Type.BaseType().IsArray()
	}
	if !accepted {
		return nil, invalidDeparse()
	}
	return class, nil
}

func deparseClassName(r stored.Deparser, class *catalog.OperatorClass) (string, error) {
	namespace, ok := r.View.Namespace(class.Namespace())
	if !ok {
		return "", invalidDeparse()
	}
	qualified := class.Namespace().ObjectID != ids.PGCatalogNamespaceOID
	if r.Path != nil {
		qualified = true
		for _, ns := range r.Path {
			if visible, ok := r.View.LookupOperatorClass(ns, class.Method, class.Name()); ok {
				qualified = visible.Address() != class.Address()
				break
			}
		}
	}
	name := r.Identifier(class.Name())
	if qualified {
		name = r.Identifier(namespace.Name()) + "." + name
	}
	return name, nil
}
