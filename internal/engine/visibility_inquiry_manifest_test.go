package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestVisibilityInquiryStoredNullManifest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		predicate  string
		visibility bool
	}{
		{"wrapped-null", "pg_type_is_visible(NULL::integer) IS NULL", true},
		{"bare-null", "pg_type_is_visible(NULL) IS NULL", false},
		{"conversion-wrapped-null", "convert_to(payload, NULL::text) IS NULL", false},
		{"conversion-nonnull", "convert_to(payload, 'UTF8') IS NOT NULL", true},
		{"demanded-sibling-first", "pg_type_is_visible(id) IS NOT NULL AND convert_to(payload, NULL::text) IS NULL", true},
		{"demanded-sibling-last", "convert_to(payload, NULL::text) IS NULL AND pg_type_is_visible(id) IS NOT NULL", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			// This internal engine fixture runs the real CHECK binder and stored
			// renderer, then publishes their expression and demand manifest.
			sessionSQL(t, s, "CREATE TABLE manifest_check(id OID, payload TEXT, CHECK ("+tc.predicate+"))")
			view := e.CatalogRead()
			relation := view.MustRelation(catalogRef(t, e, view, "manifest_check").Address)
			check := relation.Constraints.Check[0]
			switch tc.name {
			case "wrapped-null":
				inquiry, ok := check.Expression.(plan.NullTestExpr).Child.(functions.VisibilityInquiry)
				if !ok {
					t.Fatalf("wrapped NULL inquiry folded to %T", check.Expression.(plan.NullTestExpr).Child)
				}
				coercion, ok := inquiry.Arg.(plan.CoercionExpr)
				if !ok || coercion.ResultUsage().Type.ID() != sqltype.Oid {
					t.Fatalf("missing integer-to-OID coercion: %T", inquiry.Arg)
				}
				literal, ok := coercion.Child.(plan.LiteralExpr)
				if !ok || literal.ResultUsage().Type.ID() != sqltype.Int4 || !literal.Value.IsNull() {
					t.Fatalf("missing integer NULL beneath coercion: %T", coercion.Child)
				}
			case "bare-null":
				literal, ok := check.Expression.(plan.NullTestExpr).Child.(plan.LiteralExpr)
				if !ok || literal.ResultUsage().Type.ID() != sqltype.Bool || !literal.Value.IsNull() {
					t.Fatal("bare NULL must still fold to an executable BOOL NULL")
				}
			case "conversion-wrapped-null":
				conversion, ok := check.Expression.(plan.NullTestExpr).Child.(functions.Binary)
				if !ok || conversion.Plan.Immutable() {
					t.Fatal("expected retained catalog-sensitive binary conversion")
				}
				coercion, ok := conversion.Args[1].(plan.CoercionExpr)
				if !ok || coercion.ResultUsage().Type.ID() != sqltype.Name {
					t.Fatalf("missing text-to-NAME coercion: %T", conversion.Args[1])
				}
				literal, ok := coercion.Child.(plan.LiteralExpr)
				if !ok || literal.ResultUsage().Type.ID() != sqltype.Text || !literal.Value.IsNull() {
					t.Fatalf("missing text NULL beneath coercion: %T", coercion.Child)
				}
			}

			// Retained visibility nodes historically demand a snapshot even when
			// their argument is a coercion-wrapped NULL. Binary conversions keep
			// their separate NULL-sensitive policy; neither can erase a sibling's
			// demand. These expectations are not derived from Render's recipe.
			if check.VisibilitySnapshot != tc.visibility {
				t.Errorf("stored visibility manifest = %t, want %t", check.VisibilitySnapshot, tc.visibility)
			}
			var visibility bool
			if _, err := (scalarRenderer{owner: relation, visibilitySnapshot: &visibility}).sql(context.Background(), view, check.Expression); err != nil {
				t.Fatal("render stored CHECK", err)
			}
			if visibility != tc.visibility {
				t.Errorf("rendered visibility demand = %t, want %t", visibility, tc.visibility)
			}

			// Validate the independently specified persisted contract, not just
			// agreement between today's writer and today's validator.
			expected := *relation
			expected.Constraints.Check = append([]catalog.CheckConstraint(nil), relation.Constraints.Check...)
			expected.Constraints.Check[0].VisibilitySnapshot = tc.visibility
			if err := validateChecks(view, &expected, map[string]bool{}); err != nil {
				t.Error("preserved CHECK visibility manifest rejected", err)
			}
			expected.Constraints.Check[0].VisibilitySnapshot = !tc.visibility
			err := validateChecks(view, &expected, map[string]bool{})
			if err == nil || err.Error() != "stuntgres: inconsistent check visibility snapshot manifest" {
				t.Errorf("corrupt visibility manifest error = %v", err)
			}
		})
	}
}
