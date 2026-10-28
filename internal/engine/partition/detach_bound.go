package partition

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DetachBoundCheck reconstructs syntax only from immutable, already bound key
// recipes and typed datums. The ordinary CHECK binder owns executable cooking,
// dependency capture, naming and inherited row-identity transfer.
func DetachBoundCheck(ctx context.Context, view catalog.Read, parent, child *catalog.Relation) (*parser.CheckConstraint, error) {
	keys := make([]string, len(parent.Partition.Keys))
	operators := make([][5]string, len(keys))
	casts := make([]string, len(keys))
	for i, key := range parent.Partition.Keys {
		if key.Derived != nil {
			text, err := (stored.ScalarRenderer{Owner: parent}).SQL(ctx, view, key.Derived.Definition)
			if err != nil {
				return nil, err
			}
			keys[i] = "(" + text + ")"
		} else {
			keys[i] = catalog.QuoteIdentifier(parent.Columns[parent.IndexForAttribute(key.Attribute)].Name)
		}
		ordering, err := routine.BindIndexOrdering(view, key.Usage, key.Class)
		if err != nil {
			return nil, err
		}
		if !ordering.SameOperation(key.Comparison) {
			return nil, fmt.Errorf("stuntgres: changed partition comparison authority")
		}
		class, ok := view.OperatorClass(ids.OID(key.Class.OID()))
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing partition operator class")
		}
		input, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: class.InputType})
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing partition operator input type")
		}
		if input.SQLType.Concrete() && uint32(class.InputType) != uint32(key.Usage.Type.ID()) {
			if _, ok := catalog.ImplicitBinaryCast(view, key.Usage.Type.ID(), input.SQLType.ID()); !ok {
				return nil, fmt.Errorf("stuntgres: partition operator input is not an authorized binary adapter")
			}
			if _, err := sqltype.PlanBinaryCoercion(key.Usage.Type, input.SQLType); err != nil {
				return nil, err
			}
			casts[i] = catalog.QuoteIdentifier(view.Get(input.Namespace()).Name()) + "." + catalog.QuoteIdentifier(input.Name())
			keys[i] = "CAST(" + keys[i] + " AS " + casts[i] + ")"
		}
		if key.Usage.Collation.ID != 0 {
			keys[i] = "(" + keys[i] + " COLLATE " + typedef.CollationSQLName(view, key.Usage.Collation.ID) + ")"
		}
	}
	literal := func(i int, d catalog.PartitionDatum) (string, error) {
		text, err := regobj.Literal(ctx, view, plan.LiteralExpr{Info: expr.Info{Usage: parent.Partition.Keys[i].Usage}, Value: d.Value})
		if err == nil && casts[i] != "" {
			text = "CAST(" + text + " AS " + casts[i] + ")"
		}
		return text, err
	}
	compare := func(i int, strategy int16, value string) (string, error) {
		op := operators[i][strategy-1]
		if op == "" {
			selected, err := BoundOperator(view, parent.Partition.Keys[i], strategy)
			if err != nil {
				return "", err
			}
			op = "OPERATOR(" + catalog.QuoteIdentifier(view.Get(selected.Namespace()).Name()) + "." + selected.Name() + ")"
			operators[i][strategy-1] = op
		}
		return keys[i] + " " + op + " " + value, nil
	}
	var predicate string
	bound := child.Membership
	switch bound.Strategy {
	case parser.PartitionRange:
		var tuple func([]catalog.PartitionDatum, int, bool) (string, error)
		tuple = func(values []catalog.PartitionDatum, at int, lower bool) (string, error) {
			if at == len(values) {
				if lower {
					return "TRUE", nil
				}
				return "FALSE", nil
			}
			d := values[at]
			if d.Kind != 0 {
				if lower && d.Kind < 0 || !lower && d.Kind > 0 {
					return "TRUE", nil
				}
				return "FALSE", nil
			}
			value, err := literal(at, d)
			if err != nil {
				return "", err
			}
			strategy := int16(1)
			if lower {
				strategy = 5
			}
			if at == len(values)-1 {
				if lower {
					strategy = 4
				}
				return compare(at, strategy, value)
			}
			decisive, err := compare(at, strategy, value)
			if err != nil {
				return "", err
			}
			equal, err := compare(at, 3, value)
			if err != nil {
				return "", err
			}
			rest, err := tuple(values, at+1, lower)
			if err != nil {
				return "", err
			}
			return "(" + decisive + " OR (" + equal + " AND " + rest + "))", nil
		}
		lower, err := tuple(bound.Lower, 0, true)
		if err != nil {
			return nil, err
		}
		upper, err := tuple(bound.Upper, 0, false)
		if err != nil {
			return nil, err
		}
		parts := make([]string, 0, len(keys)+2)
		for _, key := range keys {
			parts = append(parts, key+" IS NOT NULL")
		}
		parts = append(parts, lower, upper)
		predicate = strings.Join(parts, " AND ")
	case parser.PartitionList:
		var terms []string
		null := false
		for _, d := range bound.Values {
			if d.Value.IsNull() {
				null = true
				continue
			}
			value, err := literal(0, d)
			if err != nil {
				return nil, err
			}
			term, err := compare(0, 3, value)
			if err != nil {
				return nil, err
			}
			terms = append(terms, term)
		}
		predicate = "FALSE"
		if len(terms) != 0 {
			predicate = "(" + keys[0] + " IS NOT NULL AND (" + strings.Join(terms, " OR ") + "))"
		}
		if null {
			predicate = "(" + keys[0] + " IS NULL OR " + predicate + ")"
		}
	default:
		return nil, fmt.Errorf("stuntgres: HASH has no concurrent detach bound CHECK")
	}
	statements, err := parser.Parse("CREATE TABLE detach_bound (CHECK (" + predicate + "))")
	if err != nil {
		return nil, err
	}
	return statements[0].(*parser.CreateTable).Elements[0].Check, nil
}
