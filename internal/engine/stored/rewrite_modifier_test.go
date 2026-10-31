package stored

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestPGRewriteMutationOwners(t *testing.T) {
	_, _, _, action := ruleDisplayFixture(t)
	var typedNil *analyze.Binding
	for _, tc := range []struct {
		name       string
		definition *plan.CTEDefinition
	}{
		{"missing", nil},
		{"opaque", &plan.CTEDefinition{Statement: &parser.Insert{}, Mutation: opaqueRoutineMutation{}}},
		{"typed_nil", &plan.CTEDefinition{Statement: &parser.Insert{}, Mutation: typedNil}},
		{"missing_payload", &plan.CTEDefinition{Statement: &parser.Insert{}}},
		{"wrong_command", &plan.CTEDefinition{Statement: &parser.Update{}, Mutation: &action.binding}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := action
			copy.binding.CTEs = []*plan.CTEDefinition{tc.definition}
			rule := NewRule(RuleParts{Event: "INSERT", Actions: []RuleAction{copy}})
			text, err := (RewriteActionCarrier{rule: &rule}).StructuralOutput(t.Context())
			if err == nil || text != "" || !strings.Contains(err.Error(), "mutation") && !strings.Contains(err.Error(), "CTE") {
				t.Fatal("invalid modifier became partial/empty success", text, err)
			}
		})
	}
}
