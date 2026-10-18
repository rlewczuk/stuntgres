package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
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
	view = view.WithState(cloneCatalog(view.State()))
	text, _ := view.LookupType(sqltype.Text)
	integer, _ := view.LookupType(sqltype.Int4)
	ns := &namespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90001}, ObjectName: "Mixed.Schema", ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}
	table := &relationDescriptor{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, ObjectNamespace: ns.Address(), ObjectName: "Owned", ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"),
		Columns:    []ColumnDescriptor{{Name: "ID", Usage: integer.Usage()}, {Name: "plain", Usage: integer.Usage()}},
		Attributes: []int32{7, 9}, Index: map[string]int{"ID": 0, "plain": 1}, AttributeHighWater: 9,
	}
	owner := table.Address()
	owner.SubID = 7
	sequence := &relationDescriptor{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90003}, ObjectNamespace: ns.Address(), ObjectName: "Owned.Seq", ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"),
		RelationKind: sequenceRelation, Sequence: &sequenceOptions{Owned: owner},
	}
	for _, descriptor := range []Descriptor{ns, table, sequence} {
		view.State().SetDescriptor(descriptor.Address(), descriptor)
		view.State().BindName(descriptorNameKey(descriptor), descriptor.Address())
	}
	literal := func(s string) expr.Expr {
		return literalExpression{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, s)}
	}
	node := deparseExpression{
		Info:      expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Stable}},
		signature: deparseSerialSequence, args: []expr.Expr{literal(`"Mixed.Schema"."Owned"`), literal("ID")},
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
		{"INTERNAL/no-default", func(f serialSequenceUnit) { f.sequence.Sequence.Identity = true }, `"Mixed.Schema"."Owned.Seq"`},
		{"NONE", func(f serialSequenceUnit) { f.sequence.Sequence.Owned = ids.ObjectAddress{} }, nil},
		{"different-column", func(f serialSequenceUnit) { f.sequence.Sequence.Owned.SubID = 9 }, nil},
		{"compacted-slot-is-not-attribute", func(f serialSequenceUnit) { f.sequence.Sequence.Owned.SubID = 1 }, nil},
		{"different-table", func(f serialSequenceUnit) { f.sequence.Sequence.Owned.ObjectID++ }, nil},
		{"different-class", func(f serialSequenceUnit) { f.sequence.Sequence.Owned.ClassID = ids.TypeClassID }, nil},
		{"whole-table-is-not-column", func(f serialSequenceUnit) { f.sequence.Sequence.Owned.SubID = 0 }, nil},
		{"index-is-not-sequence", func(f serialSequenceUnit) { f.sequence.RelationKind = indexRelation }, nil},
		{"table-is-not-sequence", func(f serialSequenceUnit) { f.sequence.RelationKind = tableRelation }, nil},
		{"no-sequence-options", func(f serialSequenceUnit) { f.sequence.Sequence = nil }, nil},
		{"name-is-not-ownership", func(f serialSequenceUnit) {
			f.sequence.ObjectName = "Owned_ID_seq"
			f.sequence.Sequence.Owned = ids.ObjectAddress{}
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
	f.engine.catalog.state = cloneCatalog(f.view.State())
	changed := *f.sequence
	options := *changed.Sequence
	options.Owned = ids.ObjectAddress{}
	changed.Sequence = &options
	f.engine.catalog.state.SetDescriptor(changed.Address(), &changed)
	value, err := f.node.Eval(f.context)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := value.Text(); err != nil || text != `"Mixed.Schema"."Owned.Seq"` {
		t.Fatal(text, err)
	}
	fresh := *f.context.statementScope
	fresh.commentCatalog = f.engine.catalog.read(f.engine.catalog.state)
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
			node.args = []expr.Expr{
				literalExpression{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, tc.table)},
				literalExpression{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, tc.column)},
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
			n.args[1] = literalExpression{Info: expr.Info{Usage: integer.Usage()}, value: v}
		}},
		{"properties", func(n *deparseExpression) { n.Property.Volatility = expr.Immutable }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := f.node
			node.args = append([]expr.Expr(nil), node.args...)
			tc.edit(&node)
			if err := validateDeparseExpression(node); err == nil {
				t.Fatal("damaged stored inquiry accepted")
			}
		})
	}
	// Real parameter nodes prove strict child demand and property merging.
	text := f.node.ResultUsage().Type
	node := f.node
	node.args = []expr.Expr{literalExpression{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text)}, parameterExpression{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{ParameterDependent: true}}, number: 1}}
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
		if n.signature != deparseSerialSequence || n.ResultUsage().Type.ID() != sqltype.Text || n.Properties().Volatility != expr.Stable || n.Properties().ParameterDependent != strings.Contains(rawSQL, "$1") {
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
