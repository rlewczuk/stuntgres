package analyze

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQuerySystemAttributeBinding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		subID  int32
		typeID sqltype.ID
	}{{"ctid", -1, sqltype.Tid}, {"xmin", -2, sqltype.Xid}, {"xmax", -4, sqltype.Xid}, {"tableoid", -6, sqltype.Oid}} {
		t.Run(tc.name, func(t *testing.T) {
			b, ns := tableOIDBinder(t)
			pos := parser.Position{Character: 8}
			node, err := b.querySystemAttribute(tc.name, ns, &ns.Qualifiers[1], nil, 4, pos)
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.SystemAttributeExpr)
			if n.SubID != tc.subID || n.ResultUsage().Type.ID() != tc.typeID || n.QueryOccurrence != 2 || n.Frame != 4 || !n.Properties().RowDependent || !b.Query.Sources[1].SystemAttributeRead || len(b.Query.Sources[1].Uses) != 0 || len(ns.Frame) != 0 {
				t.Fatal("typed source identity changed dense schema", n)
			}
			if !slices.Equal(b.Query.Sources[1].SystemColumns, []int32{tc.subID}) || len(b.Query.Sources[0].SystemColumns) != 0 || b.Query.Sources[1].WholeRowRead {
				t.Fatal("system demand lost exact source or expanded whole row", b.Query.Sources)
			}
			if _, err := b.querySystemAttribute(tc.name, ns, &ns.Qualifiers[1], nil, 4, pos); err != nil || len(b.Query.Sources[1].SystemColumns) != 1 {
				t.Fatal("repeated system read duplicated demand", err)
			}
			whole, columns := false, false
			if err := SourceReadRequirements(&b.Query.Sources[1], func(r Requirement) error {
				if r.WholeRelation {
					whole = true
				} else {
					columns = true
					if !slices.Equal(r.SystemColumns, []int32{tc.subID}) || r.WholeRow || len(r.Columns) != 0 {
						t.Fatal("system MAC demand became another attribute or whole row", r)
					}
				}
				return nil
			}); err != nil || !whole || !columns {
				t.Fatal("missing independent system ACL/MAC requirements", err)
			}
			found := false
			for _, dep := range b.Dependencies {
				found = found || dep.Ref.Address.ObjectID == n.Owner.Address.ObjectID && dep.Ref.Address.SubID == tc.subID
			}
			if !found {
				t.Fatal("missing exact negative dependency", b.Dependencies)
			}
			if !ExpressionUsesFrame(n, 4) || ExpressionUsesFrame(n, 1) || FirstQueryBoundFrameVariable(n, 4) != pos {
				t.Fatal("lexical frame lost", n)
			}
			other := n
			other.SubID = -6
			if tc.subID != -6 && b.SameGroupExpression(n, other) {
				t.Fatal("distinct system attributes grouped together")
			}
			if _, err := b.querySystemAttribute(tc.name, ns, nil, nil, 4, pos); err == nil || err.(*sqlstate.Error).Code != "42702" {
				t.Fatal("comma ambiguity", err)
			}
			for i := range ns.Qualifiers {
				ns.Qualifiers[i].SystemColumnsHidden = true
			}
			if got, err := b.querySystemAttribute(tc.name, ns, nil, nil, 4, pos); got != nil || err != nil {
				t.Fatal("JOIN operands leaked into unqualified output", got, err)
			}
			detail, hint, err := QueryColumnDiagnostic(ns.Qualifiers, "", tc.name, pos, false)
			if err != nil || detail != `There are columns named "`+tc.name+`", but they are in tables that cannot be referenced from this part of the query.` || hint != "Try using a table-qualified name." {
				t.Fatal("qualified-only diagnostic", detail, hint, err)
			}
			ns.Qualifiers[1].Columns = []QueryColumn{{Descriptor: catalog.Column{Name: tc.name}}}
			ordinary := ns.Qualifiers[1].Columns[0]
			if got, err := b.querySystemAttribute(tc.name, ns, &ns.Qualifiers[1], &ordinary, 4, pos); got != nil || err != nil {
				t.Fatal("ordinary member lost precedence", got, err)
			}
			b.Consumer.CheckConsumer = true
			if got, err := b.querySystemAttribute(tc.name, ns, &ns.Qualifiers[0], nil, 4, pos); got != nil || err != nil {
				t.Fatal("CHECK gained system namespace", got, err)
			}
		})
	}
	for _, name := range []string{"cmin", "cmax"} {
		b, ns := tableOIDBinder(t)
		if n, err := b.querySystemAttribute(name, ns, &ns.Qualifiers[0], nil, 1, parser.Position{}); n != nil || err != nil {
			t.Fatal("unsupported command ID became executable", name, n, err)
		}
	}
}

func TestQuerySystemAttributeTargetAuthority(t *testing.T) {
	b, ns := tableOIDBinder(t)
	b.Target.Relation, b.Target.Ref = b.Query.Sources[0].Relation, b.Query.Sources[0].Ref
	ns.Qualifiers = ns.Qualifiers[:1]
	ns.Qualifiers[0].SystemOccurrence = -1
	node, err := b.querySystemAttribute("ctid", ns, nil, nil, 1, parser.Position{Character: 9})
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.SystemAttributeExpr)
	if n.QueryOccurrence != 0 || n.Scope != 1 || n.Frame != 1 || !b.Target.SystemAttributeRead || !b.Target.returningReadRequired || len(b.Target.Uses) != 0 {
		t.Fatal("target became a FROM source or ordinary attribute", n)
	}
	binding := Binding{Ref: b.Target.Ref, Relation: b.Target.Relation, SystemAttributeRead: true, SystemColumns: b.Target.SystemColumns, Returning: &Returning{ReadRequired: true}}
	for _, stmt := range []parser.Statement{&parser.Insert{}, &parser.Update{}, &parser.Delete{}} {
		whole, columns := false, false
		if err := BindingRequirements(binding, stmt, func(r Requirement) error {
			if r.Privilege == authorization.SelectPrivilege {
				if r.WholeRelation {
					whole = true
				} else {
					columns = true
					if !slices.Equal(r.SystemColumns, []int32{-1}) || r.WholeRow || len(r.Columns) != 0 {
						t.Fatal("target system MAC demand lost exact attribute", r)
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !whole || !columns {
			t.Fatal("missing independent target SELECT and system-column requirements", stmt)
		}
	}
	if plan.GenerationImmutable(n) || plan.GenerationHasTableOID(n) {
		t.Fatal("physical ctid became a generated owner recipe")
	}
	generated := n
	generated.SubID, generated.Scope, generated.Frame = -6, 0, 0
	generated.Info = expr.Info{Usage: b.Type(sqltype.Oid).Usage()}
	if !plan.GenerationImmutable(generated) || !plan.GenerationHasTableOID(generated) {
		t.Fatal("generated tableoid semantics changed")
	}
}

func TestQuerySystemAttributeMergeWhen(t *testing.T) {
	for _, name := range []string{"ctid", "xmin", "cmin", "xmax", "cmax"} {
		for _, target := range []bool{false, true} {
			b, ns := tableOIDBinder(t)
			b.Query.Frame, b.Target.mergeWhenFrame = 7, 7
			ns.Qualifiers = ns.Qualifiers[:1]
			if target {
				b.Target.Relation, b.Target.Ref = b.Query.Sources[0].Relation, b.Query.Sources[0].Ref
				ns.Qualifiers[0].SystemOccurrence = -1
			}
			pos := parser.Position{Character: 19}
			node, err := b.querySystemAttribute(name, ns, &ns.Qualifiers[0], nil, 7, pos)
			diagnostic, ok := err.(*sqlstate.Error)
			if node != nil || !ok || diagnostic.Code != "42P10" || diagnostic.Message != `cannot use system column "`+name+`" in MERGE WHEN condition` || diagnostic.Position != "19" {
				t.Fatal(name, target, node, err)
			}
			if len(b.Dependencies) != 0 || b.Query.Sources[0].SystemAttributeRead || b.Target.SystemAttributeRead {
				t.Fatal("rejected system read acquired an executable dependency")
			}
			// Names alone do not confer physical identity or its restrictions.
			ns.Qualifiers[0].Columns = []QueryColumn{{Descriptor: catalog.Column{Name: name}}}
			ordinary := ns.Qualifiers[0].Columns[0]
			if node, err := b.querySystemAttribute(name, ns, &ns.Qualifiers[0], &ordinary, 7, pos); node != nil || err != nil {
				t.Fatal("ordinary shadow acquired physical WHEN restriction", node, err)
			}
			ns.Qualifiers[0].Columns = nil
			ns.Qualifiers[0].SystemOccurrence = 0
			if node, err := b.querySystemAttribute(name, ns, &ns.Qualifiers[0], nil, 7, pos); node != nil || err != nil {
				t.Fatal("derived item acquired physical WHEN restriction", node, err)
			}
		}
	}
	for _, nested := range []bool{false, true} {
		b, ns := tableOIDBinder(t)
		b.Query.Frame, b.Target.mergeWhenFrame = 7, 7
		name := "tableoid"
		if nested {
			// SELECT owns a new expression environment even for a correlated
			// read of the enclosing MERGE source occurrence.
			b.Query.Frame = 8
			name = "xmin"
		}
		node, err := b.querySystemAttribute(name, ns, &ns.Qualifiers[0], nil, 7, parser.Position{})
		if err != nil || node == nil || node.(plan.SystemAttributeExpr).QueryOccurrence != 1 {
			t.Fatal("legal system occurrence rejected", nested, node, err)
		}
	}
}
