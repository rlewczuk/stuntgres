package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type serialSequenceUnit struct {
	engine   *Engine
	view     catalog.Read
	context  *exec.Context
	table    *catalog.Relation
	sequence *catalog.Relation
	node     functions.Deparse
}

// Construct descriptors directly. Public SQL witnesses belong in reference/.
func serialSequenceUnitFixture(t *testing.T) serialSequenceUnit {
	t.Helper()
	e := New()
	view := e.CatalogRead()
	view = view.WithState(catalog.CloneState(view.State()))
	text, _ := view.LookupType(sqltype.Text)
	integer, _ := view.LookupType(sqltype.Int4)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90001}, ObjectName: "Mixed.Schema", ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}
	table := &catalog.Relation{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, ObjectNamespace: ns.Address(), ObjectName: "Owned", ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"),
		Columns:    []catalog.Column{{Name: "ID", Usage: integer.Usage()}, {Name: "plain", Usage: integer.Usage()}},
		Attributes: []int32{7, 9}, Index: map[string]int{"ID": 0, "plain": 1}, ColumnMeta: catalog.ColumnMetadata{
			HighWater: 9,
		},
	}
	owner := table.Address()
	owner.SubID = 7
	sequence := &catalog.Relation{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90003}, ObjectNamespace: ns.Address(), ObjectName: "Owned.Seq", ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"),
		RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{Owned: owner},
	}
	for _, descriptor := range []catalog.Descriptor{ns, table, sequence} {
		view.State().SetDescriptor(descriptor.Address(), descriptor)
		view.State().BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	}
	literal := func(s string) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, s)}
	}
	node := functions.Deparse{
		Info:      expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Stable}},
		Signature: functions.DeparseSerialSequence, Args: []expr.Expr{literal(`"Mixed.Schema"."Owned"`), literal("ID")},
	}
	scope := &StatementScope{inquiry: inquirySnapshot{commentCatalog: view, deparseAuth: catalogAuthorization(e, view)}}
	return serialSequenceUnit{e, view, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: hostOf(scope)}}, table, sequence, node}
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
		{"index-is-not-sequence", func(f serialSequenceUnit) { f.sequence.RelationKind = catalog.IndexRelation }, nil},
		{"table-is-not-sequence", func(f serialSequenceUnit) { f.sequence.RelationKind = catalog.TableRelation }, nil},
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
	f.engine.catalog.state = catalog.CloneState(f.view.State())
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
	fresh := *scopeOf(f.context.Host)
	fresh.inquiry.commentCatalog = f.engine.catalog.read(f.engine.catalog.state)
	fresh.inquiry.deparseAuth.Catalog = fresh.inquiry.commentCatalog
	fresh.inquiry.deparseAuth.PairedCatalog = fresh.inquiry.deparseAuth.Catalog
	value, err = f.node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: hostOf(&fresh)}})
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
			node.Args = []expr.Expr{
				plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, tc.table)},
				plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, tc.column)},
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
	if err := f.node.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*functions.Deparse)
	}{
		{"signature", func(n *functions.Deparse) { n.Signature = functions.DeparseSignature(functions.DeparseSignatureCount) }},
		{"arity", func(n *functions.Deparse) { n.Args = n.Args[:1] }},
		{"nil-child", func(n *functions.Deparse) { n.Args[1] = nil }},
		{"result-type", func(n *functions.Deparse) { n.Usage.Type, _ = f.view.LookupType(sqltype.Int4) }},
		{"child-type", func(n *functions.Deparse) {
			integer, _ := f.view.LookupType(sqltype.Int4)
			v, _ := sqltype.Integer(integer, 1)
			n.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: v}
		}},
		{"properties", func(n *functions.Deparse) { n.Property.Volatility = expr.Immutable }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := f.node
			node.Args = append([]expr.Expr(nil), node.Args...)
			tc.edit(&node)
			if err := node.Validate(); err == nil {
				t.Fatal("damaged stored inquiry accepted")
			}
		})
	}
	// Real parameter nodes prove strict child demand and property merging.
	text := f.node.ResultUsage().Type
	node := f.node
	node.Args = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}, plan.ParameterExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}}
	node.Property.ParameterDependent = true
	if _, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err == nil {
		t.Fatal("NULL suppressed demanded child")
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{sqltype.String(text, "missing")}}})
	if err != nil || !value.IsNull() || value.Type() != text {
		t.Fatal("strict NULL required catalog", value, err)
	}
	for _, rawSQL := range []string{`SELECT pg_get_serial_sequence($1,$2)`, `SELECT pg_get_serial_sequence('t','id')`} {
		raw := statement(t, rawSQL).(*parser.Select).Targets[0].(parser.FunctionExpr)
		b := newTestExpressionBinder(t, context.Background(), f.engine, sqltype.InputEnvironment{}, nil)
		bound, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := bound.(functions.Deparse)
		if n.Signature != functions.DeparseSerialSequence || n.ResultUsage().Type.ID() != sqltype.Text || n.Properties().Volatility != expr.Stable || n.Properties().ParameterDependent != strings.Contains(rawSQL, "$1") {
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
