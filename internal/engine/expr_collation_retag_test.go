package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExprCollationRetagAuthority(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		`CREATE COLLATION expr_retag_source FROM pg_catalog."C"`,
		`CREATE COLLATION expr_retag_other FROM pg_catalog."C"`,
		`CREATE COLLATION expr_retag_destination FROM pg_catalog."C"`,
		`CREATE COLLATION expr_retag_other_destination FROM pg_catalog."C"`,
		`CREATE TABLE expr_retag_t(v BOOL DEFAULT ('A' COLLATE expr_retag_source='A'),a TEXT COLLATE expr_retag_source,b TEXT COLLATE expr_retag_other)`,
		`CREATE VIEW expr_retag_v AS SELECT a=b AS same FROM expr_retag_t`,
		`CREATE VIEW expr_retag_case AS SELECT CASE a COLLATE expr_retag_source WHEN b COLLATE expr_retag_other THEN 1 ELSE 0 END AS answer FROM expr_retag_t`,
		`CREATE VIEW expr_retag_row AS SELECT (1,a)<(2,b) AS answer FROM expr_retag_t`,
		`CREATE VIEW expr_retag_membership AS SELECT a IN (SELECT b FROM expr_retag_t) AS answer FROM expr_retag_t`,
		`CREATE VIEW expr_retag_projection AS SELECT a=b AS unused,a COLLATE expr_retag_source=b AS selected FROM expr_retag_t`,
		`CREATE DOMAIN expr_retag_domain AS TEXT COLLATE expr_retag_source`,
		`CREATE DOMAIN expr_retag_domain_destination AS TEXT COLLATE expr_retag_destination`,
		`CREATE DOMAIN expr_retag_domain_default AS TEXT`,
		`CREATE DOMAIN expr_retag_domain_default_destination AS TEXT`,
		`CREATE VIEW expr_retag_domain_types AS SELECT 'A'::expr_retag_domain AS s,'A'::expr_retag_domain_destination AS d,'A'::expr_retag_domain_default AS f,'A'::expr_retag_domain_default_destination AS g`,
		`CREATE VIEW expr_retag_domain_top AS SELECT ('A' COLLATE expr_retag_other)::expr_retag_domain='A' AS answer`,
		`CREATE VIEW expr_retag_domain_nested AS SELECT (('A' COLLATE expr_retag_other)||'')::expr_retag_domain='A' AS answer`,
		`CREATE VIEW expr_retag_domain_bubble AS SELECT (('A' COLLATE expr_retag_other)||'')::expr_retag_domain_default='A' AS answer`,
	} {
		sessionSQL(t, owner, q)
	}
	view := e.CatalogRead()
	collation := func(name string) *catalog.Collation {
		for _, descriptor := range view.Descriptors() {
			if d, ok := descriptor.(*catalog.Collation); ok && d.Name() == name {
				return d
			}
		}
		t.Fatal("missing genuine collation", name)
		return nil
	}
	source, destination := collation("expr_retag_source"), collation("expr_retag_destination")
	other, otherDestination := collation("expr_retag_other"), collation("expr_retag_other_destination")
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{source.Address(): destination.Address(), other.Address(): otherDestination.Address()}
	remap := typedef.RemappedCollationCapabilities(view.State(), mapping)
	if remap == nil {
		t.Fatal("fixture lacks actual identity replacement")
	}
	tableRef := catalogRef(t, e, view, "expr_retag_t")
	table := view.MustRelation(tableRef.Address)
	original := table.ColumnMeta.Defaults[table.Attributes[0]].Expression.(plan.ComparisonExpr)
	oldRef, conflict := original.Predicate.InputCollation()
	if oldRef.ID != uint32(source.OID) || conflict || original.Usage.Collation.ID != 0 {
		t.Fatal("BOOL result hid wrong input authority", oldRef, conflict, original.Usage)
	}
	oldSQL, err := defaultSQL(context.Background(), view, original)
	if err != nil {
		t.Fatal(err)
	}
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	rebound, changed, err := retagBoundTypes(view, original, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mapped := rebound.(plan.ComparisonExpr)
	newRef, conflict := mapped.Predicate.InputCollation()
	if newRef.ID != uint32(destination.OID) || !newRef.Valid() || conflict || newRef == oldRef {
		t.Fatal("hidden comparison input not remapped", newRef, conflict)
	}
	newSQL, err := defaultSQL(context.Background(), view, mapped)
	if err != nil || !strings.Contains(newSQL, "expr_retag_destination") || strings.Contains(newSQL, "expr_retag_source") {
		t.Fatal("stored renderer lost actual destination authority", newSQL, err)
	}
	ctx := &EvalContext{EvalStatement: evalStatement{Context: context.Background()}}
	value, err := mapped.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := value.Boolean()
	if err != nil || !truth {
		t.Fatal(value, err)
	}
	if ref, conflict := original.Predicate.InputCollation(); ref != oldRef || conflict {
		t.Fatal("source provider mutated")
	}
	if actual, err := defaultSQL(context.Background(), view, original); err != nil || actual != oldSQL {
		t.Fatal("source operand graph/render changed", actual, err)
	}
	viewRef := catalogRef(t, e, view, "expr_retag_v")
	stored := viewOf(view.MustRelation(viewRef.Address)).query.Root.(*plan.ProjectNode).Expressions[0].(plan.ComparisonExpr)
	selected, ambiguous := stored.Predicate.InputCollation()
	if !ambiguous || selected.ID != 100 || stored.Usage.Collation.ID != 0 {
		t.Fatal("stored implicit conflict erased", selected, ambiguous)
	}
	rebound, changed, err = retagBoundTypes(view, stored, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mapped = rebound.(plan.ComparisonExpr)
	if ref, conflict := mapped.Predicate.InputCollation(); ref != selected || !conflict {
		t.Fatal("retag lost demand flag", ref, conflict)
	}
	if mapped.Left.ResultUsage().Collation.ID != uint32(destination.OID) || mapped.Right.ResultUsage().Collation.ID != uint32(otherDestination.OID) {
		t.Fatal("destination operands not genuine mapped capabilities")
	}
	if stored.Left.ResultUsage().Collation.ID != uint32(source.OID) || stored.Right.ResultUsage().Collation.ID != uint32(other.OID) {
		t.Fatal("source conflict operands mutated")
	}
	left, right := mapped.Left.ResultUsage().Type, mapped.Right.ResultUsage().Type
	_, err = mapped.Predicate.Evaluate(context.Background(), sqltype.String(left, "A"), sqltype.String(right, "A"), sqltype.InputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}) {
		t.Fatal("mapped conflict no longer demanded", err)
	}
	storedExpression := func(name string) expr.Expr {
		ref := catalogRef(t, e, view, name)
		return viewOf(view.MustRelation(ref.Address)).query.Root.(*plan.ProjectNode).Expressions[0]
	}
	saved := storedExpression("expr_retag_case").(plan.CaseExpr)
	ref, conflict := saved.Arms[0].Equality.InputCollation()
	if ref.ID != uint32(other.OID) || conflict {
		t.Fatal("saved CASE test incorrectly kept explicit strength", ref, conflict)
	}
	rebound, changed, err = retagBoundTypes(view, saved, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mappedCase := rebound.(plan.CaseExpr)
	if ref, conflict := mappedCase.Arms[0].Equality.InputCollation(); ref.ID != uint32(otherDestination.OID) || conflict {
		t.Fatal("CASE equality authority not remapped", ref, conflict)
	}
	if ref, _ := saved.Arms[0].Equality.InputCollation(); ref.ID != uint32(other.OID) {
		t.Fatal("CASE source arm mutated")
	}

	row := storedExpression("expr_retag_row").(plan.RowComparisonExpr)
	rebound, changed, err = retagBoundTypes(view, row, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mappedRow := rebound.(plan.RowComparisonExpr)
	if ref, conflict := mappedRow.Ordering[1].InputCollation(); ref != selected || !conflict {
		t.Fatal("lexical pair authority lost conflict", ref, conflict)
	}
	if mappedRow.Comparisons[1].Left.ResultUsage().Collation.ID != uint32(destination.OID) || row.Comparisons[1].Left.ResultUsage().Collation.ID != uint32(source.OID) {
		t.Fatal("row pair remap mutated source or missed destination")
	}
	mappedRow.Comparisons = append([]plan.ComparisonExpr(nil), mappedRow.Comparisons...)
	for i, pair := range mappedRow.Comparisons {
		if i == 1 {
			pair.Left = plan.LiteralExpr{Info: expr.InfoOf(pair.Left), Value: sqltype.String(pair.Left.ResultUsage().Type, "A")}
			pair.Right = plan.LiteralExpr{Info: expr.InfoOf(pair.Right), Value: sqltype.String(pair.Right.ResultUsage().Type, "A")}
		}
		mappedRow.Comparisons[i] = pair
	}
	value, err = mappedRow.Eval(ctx)
	truth, boolErr := value.Boolean()
	if err != nil || boolErr != nil || !truth {
		t.Fatal("earlier lexical pair demanded later conflict", value, err, boolErr)
	}
	// A decisive row with literal operands must simplify as a row, not fold
	// each scalar pair independently and demand an unreachable string pair.
	if _, _, err := simplifyConditional(ctx, mappedRow, false); err != nil {
		t.Fatal("row simplification crossed lexical frontier", err)
	}
	first := mappedRow.Comparisons[0]
	first.Right = first.Left
	mappedRow.Comparisons[0] = first
	_, err = mappedRow.Eval(ctx)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" || diagnostic.Message != "could not determine which collation to use for string comparison" {
		t.Fatal("reached lexical pair failed to demand authority", err)
	}

	membership := storedExpression("expr_retag_membership").(plan.SubqueryExpr)
	if len(membership.Membership) != 1 {
		t.Fatal("uncorrelated equality did not retain real hash strategy")
	}
	rebound, changed, err = retagBoundTypes(view, membership, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mappedMembership := rebound.(plan.SubqueryExpr)
	if len(mappedMembership.Membership) != 1 {
		t.Fatal("retag lost hash strategy")
	}
	keyType := mappedMembership.Comparisons[0].Left.ResultUsage().Type
	_, err = mappedMembership.Membership[0].Hash(context.Background(), sqltype.String(keyType, "A"))
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" || diagnostic.Message != "could not determine which collation to use for string hashing" {
		t.Fatal("actual membership hash lost indeterminate input", err)
	}
	if membership.Comparisons[0].Left.ResultUsage().Collation.ID != uint32(source.OID) || mappedMembership.Comparisons[0].Left.ResultUsage().Collation.ID != uint32(destination.OID) {
		t.Fatal("membership input remap/source immutability broken")
	}
	projectionRef := catalogRef(t, e, view, "expr_retag_projection")
	projection := viewOf(view.MustRelation(projectionRef.Address)).query.Root.(*plan.ProjectNode)
	demanded, err := storedProjectionDemand(projection, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if demanded == projection || demanded.Child != projection.Child || len(demanded.Schema()) != len(projection.Schema()) {
		t.Fatal("stored demand changed clause stages or output layout")
	}
	rowContext := &EvalContext{
		EvalStatement: evalStatement{Context: context.Background()},
		EvalFrame: evalFrame{
			Scope: 1,
			Frame: projection.Frame,
			Row: []sqltype.Value{
				sqltype.Null(table.Columns[0].Usage.Type),
				sqltype.String(table.Columns[1].Usage.Type, "A"),
				sqltype.String(table.Columns[2].Usage.Type, "A"),
			},
		},
	}
	value, err = demanded.Expressions[0].Eval(rowContext)
	if err != nil || !value.IsNull() {
		t.Fatal("unreferenced stored payload still demanded provider", value, err)
	}
	value, err = demanded.Expressions[1].Eval(rowContext)
	truth, boolErr = value.Boolean()
	if err != nil || boolErr != nil || !truth {
		t.Fatal("referenced stored comparison lost authority", value, err, boolErr)
	}
	_, err = projection.Expressions[0].Eval(rowContext)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
		t.Fatal("stored source definition mutated or conflict suppressed", err)
	}
	if full, err := storedProjectionDemand(projection, nil); err != nil || full != projection {
		t.Fatal("full consumer lost stored targets", full, err)
	}
	if full, err := storedProjectionDemand(projection, []int{0, 1}); err != nil || full != projection {
		t.Fatal("star demand changed stored targets", full, err)
	}
	if _, err := storedProjectionDemand(projection, []int{2}); err == nil {
		t.Fatal("malformed stored slot admitted")
	}
	typesRef := catalogRef(t, e, view, "expr_retag_domain_types")
	types := view.MustRelation(typesRef.Address).Columns
	mappedType := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == types[0].Usage.Type {
			return types[1].Usage.Type
		}
		if d == types[2].Usage.Type {
			return types[3].Usage.Type
		}
		return d
	}
	for _, tc := range []struct {
		name string
		ref  uint32
		top  bool
	}{
		{"expr_retag_domain_top", uint32(otherDestination.OID), true},
		{"expr_retag_domain_nested", uint32(destination.OID), false},
		{"expr_retag_domain_bubble", uint32(otherDestination.OID), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := storedExpression(tc.name).(plan.ComparisonExpr)
			sourceSQL, err := defaultSQL(context.Background(), view, source)
			if err != nil {
				t.Fatal(err)
			}
			sourceRef, sourceConflict := source.Predicate.InputCollation()
			rebound, changed, err := retagBoundTypes(view, source, mappedType, remap)
			if err != nil || !changed {
				t.Fatal("domain authority remap", changed, err)
			}
			mapped := rebound.(plan.ComparisonExpr)
			ref, conflict := mapped.Predicate.InputCollation()
			if !ref.Valid() || ref.ID != tc.ref || conflict {
				t.Fatal("mapped domain consumer capability", ref, conflict)
			}
			if tc.top && mapped.Left.ExplicitCollation() != source.Left.ExplicitCollation() {
				t.Fatal("mapped top explicit diagnostic location changed", mapped.Left.ExplicitCollation())
			}
			targetName := "expr_retag_domain_destination"
			if tc.name == "expr_retag_domain_bubble" {
				targetName = "expr_retag_domain_default_destination"
			}
			mappedSQL, err := defaultSQL(context.Background(), view, mapped)
			if err != nil || mappedSQL == sourceSQL || !strings.Contains(mappedSQL, targetName) {
				t.Fatal("domain renderer lost mapped graph", mappedSQL, err)
			}
			value, err := mapped.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
			truth, failure := value.Boolean()
			if err != nil || failure != nil || !truth {
				t.Fatal("mapped domain consumer", value, err, failure)
			}
			if ref, conflict := source.Predicate.InputCollation(); ref != sourceRef || conflict != sourceConflict {
				t.Fatal("source domain provider mutated")
			}
			if actual, err := defaultSQL(context.Background(), view, source); err != nil || actual != sourceSQL {
				t.Fatal("source domain rendering changed", actual, err)
			}
		})
	}
}
