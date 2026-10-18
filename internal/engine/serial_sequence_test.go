package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type serialSequenceUnit struct {
	engine   *Engine
	view     CatalogRead
	context  *EvalContext
	table    *relationDescriptor
	sequence *relationDescriptor
	node     deparseExpression
}

// Construct descriptors directly. Public SQL witnesses belong in reference/.
func serialSequenceUnitFixture(t *testing.T) serialSequenceUnit {
	t.Helper()
	e := New()
	view := e.CatalogRead()
	view.state = cloneCatalog(view.state)
	text, _ := view.LookupType(sqltype.Text)
	integer, _ := view.LookupType(sqltype.Int4)
	ns := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90001}, name: "Mixed.Schema", revision: 1, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}
	table := &relationDescriptor{
		address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, namespace: ns.address, name: "Owned", revision: 1, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"),
		columns:    []ColumnDescriptor{{Name: "ID", Usage: integer.Usage()}, {Name: "plain", Usage: integer.Usage()}},
		attributes: []int32{7, 9}, index: map[string]int{"ID": 0, "plain": 1}, attributeHighWater: 9,
	}
	owner := table.address
	owner.SubID = 7
	sequence := &relationDescriptor{
		address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90003}, namespace: ns.address, name: "Owned.Seq", revision: 1, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"),
		relationKind: sequenceRelation, sequence: &sequenceOptions{owned: owner},
	}
	for _, descriptor := range []Descriptor{ns, table, sequence} {
		view.state.descriptors[descriptor.Address()] = descriptor
		view.state.names[descriptorNameKey(descriptor)] = descriptor.Address()
	}
	literal := func(s string) BoundExpr {
		return literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, s)}
	}
	node := deparseExpression{
		expressionInfo: expressionInfo{Usage: text.Usage(), Property: ExprProperties{Volatility: Stable}},
		signature:      deparseSerialSequence, args: []BoundExpr{literal(`"Mixed.Schema"."Owned"`), literal("ID")},
	}
	scope := &StatementScope{commentCatalog: view, deparseAuth: catalogAuthorization(e, view)}
	return serialSequenceUnit{e, view, &EvalContext{Context: context.Background(), statementScope: scope}, table, sequence, node}
}

func TestSerialSequenceOwnership(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(serialSequenceUnit)
		want any
	}{
		{"AUTO/stable-attribute", func(serialSequenceUnit) {}, `"Mixed.Schema"."Owned.Seq"`},
		{"INTERNAL/no-default", func(f serialSequenceUnit) { f.sequence.sequence.identity = true }, `"Mixed.Schema"."Owned.Seq"`},
		{"NONE", func(f serialSequenceUnit) { f.sequence.sequence.owned = ids.ObjectAddress{} }, nil},
		{"different-column", func(f serialSequenceUnit) { f.sequence.sequence.owned.SubID = 9 }, nil},
		{"compacted-slot-is-not-attribute", func(f serialSequenceUnit) { f.sequence.sequence.owned.SubID = 1 }, nil},
		{"different-table", func(f serialSequenceUnit) { f.sequence.sequence.owned.ObjectID++ }, nil},
		{"different-class", func(f serialSequenceUnit) { f.sequence.sequence.owned.ClassID = ids.TypeClassID }, nil},
		{"whole-table-is-not-column", func(f serialSequenceUnit) { f.sequence.sequence.owned.SubID = 0 }, nil},
		{"index-is-not-sequence", func(f serialSequenceUnit) { f.sequence.relationKind = indexRelation }, nil},
		{"table-is-not-sequence", func(f serialSequenceUnit) { f.sequence.relationKind = tableRelation }, nil},
		{"no-sequence-options", func(f serialSequenceUnit) { f.sequence.sequence = nil }, nil},
		{"name-is-not-ownership", func(f serialSequenceUnit) {
			f.sequence.name = "Owned_ID_seq"
			f.sequence.sequence.owned = ids.ObjectAddress{}
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := serialSequenceUnitFixture(t)
			tc.edit(f)
			value, err := f.node.Eval(f.context)
			if err != nil {
				t.Fatal(err)
			}
			if value.Type() != f.node.ResultUsage().Type {
				t.Fatal("lost TEXT usage")
			}
			if tc.want == nil {
				if !value.IsNull() {
					t.Fatal("false association", value)
				}
			} else if got, err := value.Text(); err != nil || got != tc.want {
				t.Fatal(got, err, tc.want)
			}
		})
	}
	// Replacement of the engine's live map must not change a captured inquiry.
	f := serialSequenceUnitFixture(t)
	f.engine.state = cloneCatalog(f.view.state)
	changed := *f.sequence
	options := *changed.sequence
	options.owned = ids.ObjectAddress{}
	changed.sequence = &options
	f.engine.state.descriptors[changed.address] = &changed
	value, err := f.node.Eval(f.context)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := value.Text(); err != nil || text != `"Mixed.Schema"."Owned.Seq"` {
		t.Fatal(text, err)
	}
	fresh := *f.context.statementScope
	fresh.commentCatalog = CatalogRead{owner: f.engine, state: f.engine.state}
	fresh.deparseAuth.catalog = fresh.commentCatalog
	fresh.deparseAuth.pairedCatalog = fresh.deparseAuth.catalog
	value, err = f.node.Eval(&EvalContext{Context: context.Background(), statementScope: &fresh})
	if err != nil || !value.IsNull() {
		t.Fatal("fresh snapshot ignored NONE", value, err)
	}
}

func TestSerialSequenceNameInput(t *testing.T) {
	f := serialSequenceUnitFixture(t)
	text := f.node.ResultUsage().Type
	for _, tc := range []struct{ table, column, code, message string }{
		{`"Mixed.Schema"."Owned"`, "id", "42703", `column "id" of relation "Owned" does not exist`},
		{`"Mixed.Schema"."Owned"`, `"ID"`, "42703", `column ""ID"" of relation "Owned" does not exist`},
		{`"Mixed.Schema"."Owned"`, "ID ", "42703", `column "ID " of relation "Owned" does not exist`},
		{"", "ID", "42602", "invalid name syntax"},
		{" \t\n", "ID", "42602", "invalid name syntax"},
		{"owned.", "ID", "42602", "invalid name syntax"},
		{`"owned`, "ID", "42602", "invalid name syntax"},
		{`public.""`, "ID", "42P01", `relation "public." does not exist`},
		{`"".owned`, "ID", "3F000", `schema "" does not exist`},
		{"a.b.c.d", "ID", "42601", "improper relation name (too many dotted names): a.b.c.d"},
		{"other.public.owned", "ID", "0A000", `cross-database references are not implemented: "other.public.owned"`},
	} {
		t.Run(tc.table+"/"+tc.column, func(t *testing.T) {
			node := f.node
			node.args = []BoundExpr{
				literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, tc.table)},
				literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, tc.column)},
			}
			_, err := node.Eval(f.context)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != "" || diagnostic.Context != "" {
				t.Fatal(err)
			}
		})
	}
	for _, input := range []string{` PUBLIC . "Owned" `, `""`, `public.""`, `"".owned`, "123", "si--literal", strings.Repeat("界", 22)} {
		name, err := parser.ParseRegclassName(input)
		if err != nil {
			t.Fatal(input, err)
		}
		if input == "123" && name.Parts[0].Text != "123" {
			t.Fatal("numeric name became OID", name)
		}
		if strings.HasPrefix(input, "界") && name.Parts[0].Text != strings.Repeat("界", 21) {
			t.Fatal("partial UTF8 identifier", name)
		}
	}
	name, err := parser.ParseRegclassName(`current."Mixed.Schema"."Owned"`)
	if err != nil {
		t.Fatal(err)
	}
	name, err = regclassQualifiedName(name, `current."Mixed.Schema"."Owned"`, "current")
	if err != nil || len(name.Parts) != 2 || name.Parts[0].Text != "Mixed.Schema" || name.Parts[1].Text != "Owned" {
		t.Fatal(name, err)
	}
}

func TestSerialSequenceExpression(t *testing.T) {
	f := serialSequenceUnitFixture(t)
	if err := validateDeparseExpression(f.node); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*deparseExpression)
	}{
		{"signature", func(n *deparseExpression) { n.signature = deparseSignature(len(deparseSignatures)) }},
		{"arity", func(n *deparseExpression) { n.args = n.args[:1] }},
		{"nil-child", func(n *deparseExpression) { n.args[1] = nil }},
		{"result-type", func(n *deparseExpression) { n.Usage.Type, _ = f.view.LookupType(sqltype.Int4) }},
		{"child-type", func(n *deparseExpression) {
			integer, _ := f.view.LookupType(sqltype.Int4)
			v, _ := sqltype.Integer(integer, 1)
			n.args[1] = literalExpression{expressionInfo: expressionInfo{Usage: integer.Usage()}, value: v}
		}},
		{"properties", func(n *deparseExpression) { n.Property.Volatility = Immutable }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := f.node
			node.args = append([]BoundExpr(nil), node.args...)
			tc.edit(&node)
			if err := validateDeparseExpression(node); err == nil {
				t.Fatal("damaged stored inquiry accepted")
			}
		})
	}
	// Real parameter nodes prove strict child demand and property merging.
	text := f.node.ResultUsage().Type
	node := f.node
	node.args = []BoundExpr{literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.Null(text)}, parameterExpression{expressionInfo: expressionInfo{Usage: text.Usage(), Property: ExprProperties{ParameterDependent: true}}, number: 1}}
	node.Property.ParameterDependent = true
	if _, err := node.Eval(&EvalContext{Context: context.Background()}); err == nil {
		t.Fatal("NULL suppressed demanded child")
	}
	value, err := node.Eval(&EvalContext{Context: context.Background(), Parameters: []sqltype.Value{sqltype.String(text, "missing")}})
	if err != nil || !value.IsNull() || value.Type() != text {
		t.Fatal("strict NULL required catalog", value, err)
	}
	for _, rawSQL := range []string{`SELECT pg_get_serial_sequence($1,$2)`, `SELECT pg_get_serial_sequence('t','id')`} {
		raw := statement(t, rawSQL).(*parser.Select).Targets[0].(parser.FunctionExpr)
		b := newTestExpressionBinder(t, context.Background(), f.engine, sqltype.InputEnvironment{}, nil)
		bound, err := b.bindDeparse(raw, true)
		if err != nil {
			t.Fatal(err)
		}
		n := bound.(deparseExpression)
		if n.signature != deparseSerialSequence || n.ResultUsage().Type.ID() != sqltype.Text || n.Properties().Volatility != Stable || n.Properties().ParameterDependent != strings.Contains(rawSQL, "$1") {
			t.Fatal(n)
		}
	}
	// Rendering/remapping must retain the inquiry, never an evaluated name.
	before, err := defaultSQL(context.Background(), f.view, f.node)
	if err != nil || !strings.Contains(before, "pg_catalog.pg_get_serial_sequence(") {
		t.Fatal(before, err)
	}
	remapped, err := remapBoundExpression(f.node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := defaultSQL(context.Background(), f.view, remapped)
	if err != nil || before != after {
		t.Fatal(before, after, err)
	}
}
