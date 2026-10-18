package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
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
	collation := func(name string) *collationDescriptor {
		for _, descriptor := range view.Descriptors() {
			if d, ok := descriptor.(*collationDescriptor); ok && d.name == name {
				return d
			}
		}
		t.Fatal("missing genuine collation", name)
		return nil
	}
	source, destination := collation("expr_retag_source"), collation("expr_retag_destination")
	other, otherDestination := collation("expr_retag_other"), collation("expr_retag_other_destination")
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{source.Address(): destination.Address(), other.Address(): otherDestination.Address()}
	remap := remappedCollationCapabilities(view.state, mapping)
	if remap == nil {
		t.Fatal("fixture lacks actual identity replacement")
	}
	tableRef := catalogRef(t, e, view, "expr_retag_t")
	table := view.MustRelation(tableRef.Address)
	original := table.defaults[table.attributes[0]].expression.(comparisonExpression)
	oldRef, conflict := original.predicate.InputCollation()
	if oldRef.ID != uint32(source.oid) || conflict || original.Usage.Collation.ID != 0 {
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
	mapped := rebound.(comparisonExpression)
	newRef, conflict := mapped.predicate.InputCollation()
	if newRef.ID != uint32(destination.oid) || !newRef.Valid() || conflict || newRef == oldRef {
		t.Fatal("hidden comparison input not remapped", newRef, conflict)
	}
	newSQL, err := defaultSQL(context.Background(), view, mapped)
	if err != nil || !strings.Contains(newSQL, "expr_retag_destination") || strings.Contains(newSQL, "expr_retag_source") {
		t.Fatal("stored renderer lost actual destination authority", newSQL, err)
	}
	ctx := &EvalContext{Context: context.Background()}
	value, err := mapped.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := value.Boolean()
	if err != nil || !truth {
		t.Fatal(value, err)
	}
	if ref, conflict := original.predicate.InputCollation(); ref != oldRef || conflict {
		t.Fatal("source provider mutated")
	}
	if actual, err := defaultSQL(context.Background(), view, original); err != nil || actual != oldSQL {
		t.Fatal("source operand graph/render changed", actual, err)
	}
	viewRef := catalogRef(t, e, view, "expr_retag_v")
	stored := view.MustRelation(viewRef.Address).view.query.Root.(*projectNode).expressions[0].(comparisonExpression)
	selected, ambiguous := stored.predicate.InputCollation()
	if !ambiguous || selected.ID != 100 || stored.Usage.Collation.ID != 0 {
		t.Fatal("stored implicit conflict erased", selected, ambiguous)
	}
	rebound, changed, err = retagBoundTypes(view, stored, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mapped = rebound.(comparisonExpression)
	if ref, conflict := mapped.predicate.InputCollation(); ref != selected || !conflict {
		t.Fatal("retag lost demand flag", ref, conflict)
	}
	if mapped.left.ResultUsage().Collation.ID != uint32(destination.oid) || mapped.right.ResultUsage().Collation.ID != uint32(otherDestination.oid) {
		t.Fatal("destination operands not genuine mapped capabilities")
	}
	if stored.left.ResultUsage().Collation.ID != uint32(source.oid) || stored.right.ResultUsage().Collation.ID != uint32(other.oid) {
		t.Fatal("source conflict operands mutated")
	}
	left, right := mapped.left.ResultUsage().Type, mapped.right.ResultUsage().Type
	_, err = mapped.predicate.Evaluate(context.Background(), sqltype.String(left, "A"), sqltype.String(right, "A"), sqltype.InputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}) {
		t.Fatal("mapped conflict no longer demanded", err)
	}
	storedExpression := func(name string) BoundExpr {
		ref := catalogRef(t, e, view, name)
		return view.MustRelation(ref.Address).view.query.Root.(*projectNode).expressions[0]
	}
	saved := storedExpression("expr_retag_case").(caseExpression)
	ref, conflict := saved.arms[0].equality.InputCollation()
	if ref.ID != uint32(other.oid) || conflict {
		t.Fatal("saved CASE test incorrectly kept explicit strength", ref, conflict)
	}
	rebound, changed, err = retagBoundTypes(view, saved, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mappedCase := rebound.(caseExpression)
	if ref, conflict := mappedCase.arms[0].equality.InputCollation(); ref.ID != uint32(otherDestination.oid) || conflict {
		t.Fatal("CASE equality authority not remapped", ref, conflict)
	}
	if ref, _ := saved.arms[0].equality.InputCollation(); ref.ID != uint32(other.oid) {
		t.Fatal("CASE source arm mutated")
	}

	row := storedExpression("expr_retag_row").(rowComparisonExpression)
	rebound, changed, err = retagBoundTypes(view, row, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mappedRow := rebound.(rowComparisonExpression)
	if ref, conflict := mappedRow.ordering[1].InputCollation(); ref != selected || !conflict {
		t.Fatal("lexical pair authority lost conflict", ref, conflict)
	}
	if mappedRow.comparisons[1].left.ResultUsage().Collation.ID != uint32(destination.oid) || row.comparisons[1].left.ResultUsage().Collation.ID != uint32(source.oid) {
		t.Fatal("row pair remap mutated source or missed destination")
	}
	mappedRow.comparisons = append([]comparisonExpression(nil), mappedRow.comparisons...)
	for i, pair := range mappedRow.comparisons {
		if i == 1 {
			pair.left = literalExpression{boundExpressionInfo(pair.left), sqltype.String(pair.left.ResultUsage().Type, "A")}
			pair.right = literalExpression{boundExpressionInfo(pair.right), sqltype.String(pair.right.ResultUsage().Type, "A")}
		}
		mappedRow.comparisons[i] = pair
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
	first := mappedRow.comparisons[0]
	first.right = first.left
	mappedRow.comparisons[0] = first
	_, err = mappedRow.Eval(ctx)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" || diagnostic.Message != "could not determine which collation to use for string comparison" {
		t.Fatal("reached lexical pair failed to demand authority", err)
	}

	membership := storedExpression("expr_retag_membership").(subqueryExpression)
	if len(membership.membership) != 1 {
		t.Fatal("uncorrelated equality did not retain real hash strategy")
	}
	rebound, changed, err = retagBoundTypes(view, membership, identity, remap)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	mappedMembership := rebound.(subqueryExpression)
	if len(mappedMembership.membership) != 1 {
		t.Fatal("retag lost hash strategy")
	}
	keyType := mappedMembership.comparisons[0].left.ResultUsage().Type
	_, err = mappedMembership.membership[0].Hash(context.Background(), sqltype.String(keyType, "A"))
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" || diagnostic.Message != "could not determine which collation to use for string hashing" {
		t.Fatal("actual membership hash lost indeterminate input", err)
	}
	if membership.comparisons[0].left.ResultUsage().Collation.ID != uint32(source.oid) || mappedMembership.comparisons[0].left.ResultUsage().Collation.ID != uint32(destination.oid) {
		t.Fatal("membership input remap/source immutability broken")
	}
	projectionRef := catalogRef(t, e, view, "expr_retag_projection")
	projection := view.MustRelation(projectionRef.Address).view.query.Root.(*projectNode)
	demanded, err := storedProjectionDemand(projection, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if demanded == projection || demanded.child != projection.child || len(demanded.Schema()) != len(projection.Schema()) {
		t.Fatal("stored demand changed clause stages or output layout")
	}
	rowContext := &EvalContext{Context: context.Background(), Scope: 1, Frame: projection.frame, Row: []sqltype.Value{
		sqltype.Null(table.columns[0].Usage.Type),
		sqltype.String(table.columns[1].Usage.Type, "A"),
		sqltype.String(table.columns[2].Usage.Type, "A"),
	}}
	value, err = demanded.expressions[0].Eval(rowContext)
	if err != nil || !value.IsNull() {
		t.Fatal("unreferenced stored payload still demanded provider", value, err)
	}
	value, err = demanded.expressions[1].Eval(rowContext)
	truth, boolErr = value.Boolean()
	if err != nil || boolErr != nil || !truth {
		t.Fatal("referenced stored comparison lost authority", value, err, boolErr)
	}
	_, err = projection.expressions[0].Eval(rowContext)
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
	types := view.MustRelation(typesRef.Address).columns
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
		{"expr_retag_domain_top", uint32(otherDestination.oid), true},
		{"expr_retag_domain_nested", uint32(destination.oid), false},
		{"expr_retag_domain_bubble", uint32(otherDestination.oid), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := storedExpression(tc.name).(comparisonExpression)
			sourceSQL, err := defaultSQL(context.Background(), view, source)
			if err != nil {
				t.Fatal(err)
			}
			sourceRef, sourceConflict := source.predicate.InputCollation()
			rebound, changed, err := retagBoundTypes(view, source, mappedType, remap)
			if err != nil || !changed {
				t.Fatal("domain authority remap", changed, err)
			}
			mapped := rebound.(comparisonExpression)
			ref, conflict := mapped.predicate.InputCollation()
			if !ref.Valid() || ref.ID != tc.ref || conflict {
				t.Fatal("mapped domain consumer capability", ref, conflict)
			}
			if tc.top && mapped.left.ExplicitCollation() != source.left.ExplicitCollation() {
				t.Fatal("mapped top explicit diagnostic location changed", mapped.left.ExplicitCollation())
			}
			targetName := "expr_retag_domain_destination"
			if tc.name == "expr_retag_domain_bubble" {
				targetName = "expr_retag_domain_default_destination"
			}
			mappedSQL, err := defaultSQL(context.Background(), view, mapped)
			if err != nil || mappedSQL == sourceSQL || !strings.Contains(mappedSQL, targetName) {
				t.Fatal("domain renderer lost mapped graph", mappedSQL, err)
			}
			value, err := mapped.Eval(&EvalContext{Context: context.Background()})
			truth, failure := value.Boolean()
			if err != nil || failure != nil || !truth {
				t.Fatal("mapped domain consumer", value, err, failure)
			}
			if ref, conflict := source.predicate.InputCollation(); ref != sourceRef || conflict != sourceConflict {
				t.Fatal("source domain provider mutated")
			}
			if actual, err := defaultSQL(context.Background(), view, source); err != nil || actual != sourceSQL {
				t.Fatal("source domain rendering changed", actual, err)
			}
		})
	}
}
