package partition_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDeparsePartitionPresenterArrayEquality(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionList, sqltype.Int4)
	array, ok := f.view.LookupType(1007)
	if !ok {
		t.Fatal("missing real integer[] type")
	}
	equality, ok := f.view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 1070})
	if !ok || equality.Name() != "=" || equality.Left != 2277 || equality.Right != 2277 || equality.Function != 744 {
		t.Fatal("missing released array equality authority")
	}
	// Presentation-only catalog: no executable array ordering provider or new
	// production array_ops admission is manufactured by this fixture.
	family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 190010}, ObjectNamespace: f.parent.Namespace(), ObjectName: "deparse_array_family", ObjectRevision: 1, Method: 403}
	class := &catalog.OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: 190011}, ObjectNamespace: f.parent.Namespace(), ObjectName: "deparse_array_class", ObjectRevision: 1, Method: 403, Family: family.Address().ObjectID, InputType: 2277}
	member := &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: 190012}, ObjectRevision: 1, Family: family.Address().ObjectID, Left: 2277, Right: 2277, Operator: equality.Address().ObjectID, Method: 403, Number: 3, Purpose: 's'}
	f.put(family, class, member)
	identity, ok := f.view.IndexClass(class.Address().ObjectID)
	if !ok {
		t.Fatal("array presentation class identity")
	}
	f.parent.Columns[0].Usage = array.Usage()
	f.child.Columns[0].Usage = array.Usage()
	f.parent.Partition.Keys[0] = catalog.PartitionKeyEntry{Attribute: f.parent.Attributes[0], Usage: array.Usage(), Class: identity}
	f.child.Membership.Values = []catalog.PartitionDatum{f.datum(t, 0, "{3}"), f.datum(t, 0, "{1}")}
	before := append([]catalog.PartitionDatum(nil), f.child.Membership.Values...)
	// Exact text independently passed the public PG18.6 discovery case before
	// migration here; the unavailable native CREATE opclass is not this feature.
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, "((k IS NOT NULL) AND ((k = '{3}'::integer[]) OR (k = '{1}'::integer[])))")
	if !reflect.DeepEqual(before, f.child.Membership.Values) {
		t.Fatal("array datum ownership changed")
	}
	broken := *member
	broken.Operator = 4294967295
	f.put(&broken)
	if _, err := f.call(context.Background(), functions.DeparsePartitionConstraint, f.child.Address().ObjectID); err == nil {
		t.Fatal("array comparison guessed missing equality authority")
	}
}

func TestDeparsePartitionPresenterExpressionOwnership(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Int4, sqltype.Int4)
	usage := f.parent.Columns[0].Usage
	ref := f.view.Owner().Domain.Ref(f.view.Epoch(), f.parent.Address())
	ref.Address.SubID = f.parent.Attributes[0]
	column := plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: 0}
	one := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: f.datum(t, 0, "1").Value}
	arithmetic, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, usage.Type, usage.Type, usage.Type)
	if err != nil {
		t.Fatal(err)
	}
	definition := plan.ArithmeticExpr{Info: expr.Info{Usage: usage, Property: column.Properties()}, Left: column, Right: one, Operator: sqltype.ArithmeticAdd, Provider: arithmetic}
	execution := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: f.datum(t, 0, "99").Value}
	derived := &catalog.DerivedIndexKey{Definition: definition, Execution: execution, Dependencies: []ids.ObjectRef{ref}, Attributes: append([]int32(nil), f.parent.Attributes...)}
	key := f.parent.Partition.Keys[0]
	key.Attribute, key.Derived = 0, derived
	f.parent.Partition.Keys = []catalog.PartitionKeyEntry{key}
	// The attached child's physical order and attribute identities differ.
	f.child.Columns = []catalog.Column{{Name: "b", Usage: usage}, {Name: "a", Usage: usage}}
	f.child.Attributes = []int32{31, 41}
	f.child.Index = map[string]int{"a": 1, "b": 0}
	f.child.Membership.Lower = []catalog.PartitionDatum{f.datum(t, 0, "0")}
	f.child.Membership.Upper = []catalog.PartitionDatum{f.datum(t, 0, "10")}
	beforeDependencies := append([]ids.ObjectRef(nil), derived.Dependencies...)
	beforeAttributes := append([]int32(nil), derived.Attributes...)
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, "RANGE (((a + 1)))")
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, "(((a + 1) IS NOT NULL) AND ((a + 1) >= 0) AND ((a + 1) < 10))")
	if !reflect.DeepEqual(derived.Definition, definition) || !reflect.DeepEqual(derived.Execution, execution) || !reflect.DeepEqual(derived.Dependencies, beforeDependencies) || !reflect.DeepEqual(derived.Attributes, beforeAttributes) {
		t.Fatal("presentation remapped source recipes in place")
	}
	if derived.Definition.(plan.ArithmeticExpr).Left.(plan.ColumnExpr).Origin != ref || derived.Definition.(plan.ArithmeticExpr).Left.(plan.ColumnExpr).Slot != 0 {
		t.Fatal("source dependency/frame was replaced")
	}
	// Rename by stable identity; both the ordinary and expression paths read
	// the captured current names, not original labels or the folded execution.
	f.parent.Columns[0].Name = "A renamed"
	f.parent.Index = map[string]int{"A renamed": 0, "b": 1}
	f.child.Columns[1].Name = "A renamed"
	f.child.Index = map[string]int{"A renamed": 1, "b": 0}
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, `RANGE ((("A renamed" + 1)))`)
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, `((("A renamed" + 1) IS NOT NULL) AND (("A renamed" + 1) >= 0) AND (("A renamed" + 1) < 10))`)
}

func TestDeparsePartitionPresenterClassAndCollation(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Text)
	f.parent.Partition.Keys[0].ExplicitClass = true
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, "RANGE (k)")
	f.parent.Partition.Keys[0].Usage.Collation = sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}
	collatedKey := &f.parent.Partition.Keys[0]
	if comparison, err := routine.BindIndexOrdering(f.view, collatedKey.Usage, collatedKey.Class); err != nil {
		t.Fatal(err)
	} else {
		collatedKey.Comparison = comparison
	}
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, `RANGE (k COLLATE "C")`)
	f.child.Membership.Lower = []catalog.PartitionDatum{f.datum(t, 0, "b")}
	f.child.Membership.Upper = []catalog.PartitionDatum{f.datum(t, 0, "d")}
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, `((k IS NOT NULL) AND ((k)::text >= 'b'::text COLLATE "C") AND ((k)::text < 'd'::text COLLATE "C"))`)
	f.input.Output.QuoteAllIdentifiers = true
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, `(("k" IS NOT NULL) AND (("k")::"text" >= 'b'::"text" COLLATE "C") AND (("k")::"text" < 'd'::"text" COLLATE "C"))`)
	f.input.Output.QuoteAllIdentifiers = false
	f.parent.Partition.Keys[0].Usage = f.parent.Columns[0].Usage
	pg := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	pattern, ok := f.view.LookupOperatorClass(pg, 403, "text_pattern_ops")
	if !ok {
		t.Fatal("missing pattern authority")
	}
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 190020}, ObjectName: "Key Schema", ObjectRevision: 1}
	custom := *pattern
	custom.ObjectAddress.ObjectID = 190021
	custom.ObjectNamespace, custom.ObjectName, custom.Default = namespace.Address(), "Key Class", false
	f.put(namespace, &custom)
	class, ok := f.view.IndexClass(custom.Address().ObjectID)
	if !ok {
		t.Fatal("custom presentation class")
	}
	key := &f.parent.Partition.Keys[0]
	key.Class = class
	var err error
	key.Comparison, err = routine.BindIndexOrdering(f.view, key.Usage, class)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Path = []ids.ObjectAddress{pg, namespace.Address()}
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, `RANGE (k "Key Class")`)
	f.input.Path = []ids.ObjectAddress{pg}
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, `RANGE (k "Key Schema"."Key Class")`)
	f.input.Output.QuoteAllIdentifiers = true
	f.check(t, functions.DeparsePartitionKey, f.parent.Address().ObjectID, `RANGE ("k" "Key Schema"."Key Class")`)
	f.child.Membership.Lower = []catalog.PartitionDatum{f.datum(t, 0, "b")}
	f.child.Membership.Upper = []catalog.PartitionDatum{f.datum(t, 0, "d")}
	f.input.Output.QuoteAllIdentifiers = false
	f.check(t, functions.DeparsePartitionConstraint, f.child.Address().ObjectID, "((k IS NOT NULL) AND (k ~>=~ 'b'::text) AND (k ~<~ 'd'::text))")
}

type deparsePartitionCancelContext struct {
	context.Context
	remaining int
}

func (c *deparsePartitionCancelContext) Err() error {
	c.remaining--
	if c.remaining < 0 {
		return context.Canceled
	}
	return nil
}

func TestDeparsePartitionPresenterCancellationBetweenSteps(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionList, sqltype.Int4)
	f.child.Membership.Values = []catalog.PartitionDatum{f.datum(t, 0, "3"), f.datum(t, 0, "1")}
	def := *f.child
	def.ObjectAddress.ObjectID++
	def.ObjectName = "cancel_default"
	def.Membership = &catalog.PartitionMembership{Parent: f.parent.Address(), Strategy: parser.PartitionList, DefaultBound: true}
	f.parent.Partition.Children = append(f.parent.Partition.Children, def.Address())
	f.put(&def)
	for _, allowance := range []int{1, 4, 8} {
		ctx := &deparsePartitionCancelContext{Context: context.Background(), remaining: allowance}
		value, err := f.call(ctx, functions.DeparsePartitionConstraint, def.Address().ObjectID)
		if !errors.Is(err, context.Canceled) || value.Type() != nil {
			t.Fatal("partial/NULL definition published after cancellation", value, err)
		}
	}
}

func TestDeparsePartitionRecipeRetagOwnership(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionRange, sqltype.Int4)
	oid, _ := f.view.LookupType(sqltype.Oid)
	text, _ := f.view.LookupType(sqltype.Text)
	for _, tc := range []struct {
		signature functions.DeparseSignature
		oid       ids.OID
	}{
		{functions.DeparsePartitionConstraint, 3408},
		{functions.DeparsePartitionKey, 3352},
	} {
		procedure, ok := f.view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid})
		if !ok {
			t.Fatal("missing released inquiry declaration")
		}
		if procedure.Name() != tc.signature.Name() || procedure.Signature != "26" || procedure.Result != 25 || !procedure.Strict || procedure.Volatility != expr.Stable || procedure.ReturnsSet() || procedure.InputNames != nil || procedure.Builtin == nil || procedure.Builtin.Parallel != 's' || procedure.Builtin.Providers != functions.CatalogRoutineProvider {
			t.Fatal("released inquiry declaration metadata", procedure)
		}
		arg := plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: sqltype.Null(oid)}
		node := functions.Deparse{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Stable}, RoutineReference: procedure}, Signature: tc.signature, Args: []expr.Expr{arg}}
		identity, changed, err := stored.RetagTypes(f.view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed {
			t.Fatal("identity retag", changed, err)
		}
		retained := identity.(functions.Deparse)
		if &retained.Args[0] != &node.Args[0] || retained.CallRoutineReference() != procedure {
			t.Fatal("identity retag replaced immutable backing/provenance")
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		copy := remapped.(functions.Deparse)
		if copy.Signature != node.Signature || copy.Info != node.Info || !reflect.DeepEqual(copy.Args, node.Args) || &copy.Args[0] == &node.Args[0] {
			t.Fatal("remap lost recipe or borrowed source operand slice")
		}
		fresh := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			fresh[d.ID()] = d
		}
		mapped, _, err := stored.RetagTypes(f.view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(functions.Deparse)
		if err := next.Validate(); err != nil {
			t.Fatal(err)
		}
		if next.Usage.Type != fresh[sqltype.Text] || next.Args[0].ResultUsage().Type != fresh[sqltype.Oid] || next.CallRoutineReference() != procedure {
			t.Fatal("retag lost typed recipe or routine provenance")
		}
		if node.Usage.Type != text || node.Args[0].ResultUsage().Type != oid || !reflect.DeepEqual(node.Args[0], arg) {
			t.Fatal("retag modified source recipe")
		}
	}
}

func TestDeparsePartitionCollationLeafRendering(t *testing.T) {
	f := newDeparsePartitionFixture(t, parser.PartitionList, sqltype.Text)
	original, ok := f.view.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950})
	if !ok {
		t.Fatal("missing real C collation")
	}
	custom := *original
	custom.OID, custom.ObjectName = 190030, "dp_coll"
	custom.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	f.put(&custom)
	pg := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	plain := f.parent.Columns[0].Usage
	collated := plain
	collation, err := custom.Usage()
	if err != nil {
		t.Fatal(err)
	}
	collated.Collation = collation
	value := sqltype.String(plain.Type, "a")
	constant := plan.LiteralExpr{Info: expr.Info{Usage: collated}, Value: value}
	explicit := plan.CollationExpr{Info: expr.Info{Usage: collated}, Child: plan.LiteralExpr{Info: expr.Info{Usage: plain}, Value: value}}
	for _, tc := range []struct {
		name     string
		path     []ids.ObjectAddress
		quoteAll bool
		want     string
	}{
		{"visible", []ids.ObjectAddress{custom.Namespace(), pg}, false, `'a'::text COLLATE dp_coll`},
		{"quoted_visible", []ids.ObjectAddress{custom.Namespace(), pg}, true, `'a'::"text" COLLATE "dp_coll"`},
		{"hidden", []ids.ObjectAddress{pg}, false, `'a'::text COLLATE public.dp_coll`},
		{"quoted_hidden", []ids.ObjectAddress{pg}, true, `'a'::"text" COLLATE "public"."dp_coll"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			renderer := stored.Deparser{View: f.view, Path: tc.path}
			renderer.Output.QuoteAllIdentifiers = tc.quoteAll
			got, err := renderer.SQL(context.Background(), constant, stored.DeparseParent{})
			if err != nil || got != tc.want {
				t.Fatalf("constant = %q, %v; want %q", got, err, tc.want)
			}
			got, err = renderer.SQL(context.Background(), explicit, stored.DeparseParent{})
			if err != nil || got != "("+tc.want+")" {
				t.Fatalf("explicit COLLATE = %q, %v; want %q", got, err, "("+tc.want+")")
			}
		})
	}
}
