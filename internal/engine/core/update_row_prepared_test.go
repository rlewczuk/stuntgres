package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
)

// Capture cells are caller-SQL provenance, not a cache of borrowed stored recipe
// literals. Public held-statement outcomes remain in the shared Stored scenario.
func TestUpdateRowPreparedRecipeCaptures(t *testing.T) {
	for _, tc := range []struct {
		name             string
		conditional, cte bool
	}{
		{name: "action"}, {name: "conditional_residual", conditional: true}, {name: "modifying_cte", cte: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			setup := func() {
				for _, q := range []string{"CREATE TABLE input(id INT)", "CREATE TABLE t(id INT,a INT,b INT)", "CREATE SEQUENCE draw", "CREATE SEQUENCE gate", "CREATE SEQUENCE own_draw"} {
					sessionSQL(t, s, q)
				}
				action := "UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT nextval('draw')+t.id*0 AS x) SELECT x,x+100 FROM v) WHERE t.id=NEW.id AND 'integer'::regtype='integer'::regtype"
				rule := "CREATE RULE r AS ON INSERT TO input DO ALSO " + action
				if tc.conditional {
					rule = "CREATE RULE r AS ON INSERT TO input WHERE nextval('gate')>0 DO INSTEAD " + action
				}
				if tc.cte {
					rule = "CREATE RULE r AS ON INSERT TO input DO INSTEAD " + action + " RETURNING id"
				}
				sessionSQL(t, s, rule)
			}
			setup()
			query := func(value string) string {
				insert := "INSERT INTO input(id) VALUES(" + value + ")"
				if tc.cte {
					return "WITH moved AS (" + insert + " RETURNING id) SELECT id FROM moved"
				}
				return insert
			}
			ast, description := sequencePreparedDescription(t, e, s, query("1"))
			assertBorrowed := func() {
				if description.Sequences != nil || core.DescriptionRegtypes(&description) != nil {
					t.Fatal("stored recipe entered caller literal provenance", description.Sequences, core.DescriptionRegtypes(&description))
				}
			}
			assertBorrowed()
			input := enginetest.CatalogRef(t, e, e.CatalogRead(), "input")
			stored := stored.RulesOf(e.CatalogRead().MustRelation(input.Address))[0]
			if len(stored.Actions()[0].Binding().Prepared.SequenceConstants) == 0 {
				t.Fatal("recipe lost its own sequence capture")
			}
			if tc.conditional && (stored.ConditionBinding() == nil || len(stored.ConditionBinding().Prepared.SequenceConstants) == 0) {
				t.Fatal("condition lost its recipe capture")
			}
			ownedAST, owned := sequencePreparedDescription(t, e, s, query("nextval('own_draw')"))
			ownedRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "own_draw")
			assertOwned := func() {
				if owned.Sequences == nil || len(owned.Sequences.Constants) != 1 || owned.Sequences.Constants[0].Ref != ownedRef || core.DescriptionRegtypes(&owned) != nil {
					t.Fatal("raw caller/CTE capture group polluted or lost", owned.Sequences, core.DescriptionRegtypes(&owned))
				}
			}
			assertOwned()
			_, regtype := sequencePreparedDescription(t, e, s, "SELECT 'integer'::regtype")
			if core.DescriptionRegtypes(&regtype) == nil || len(core.DescriptionRegtypes(&regtype).Constants) != 1 {
				t.Fatal("direct regtype capture was discarded")
			}
			before := core.DescriptionAnalysis(&description)
			sessionSQL(t, s, "COMMENT ON TABLE t IS 'invalidate rule target dependency'")
			result, err := sequencePreparedExecute(e, s, ast, &description)
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err != nil || core.DescriptionAnalysis(&description) == before {
				t.Fatal("stored dependency failed normal reanalysis", err)
			}
			assertBorrowed()
			result, err = sequencePreparedExecute(e, s, ownedAST, &owned)
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			assertOwned()
			previous := core.DescriptionAnalysis(&owned)
			captures := append([]sequence.Constant(nil), owned.Sequences.Constants...)
			before = core.DescriptionAnalysis(&description)
			if err := engine.Reset(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			setup()
			result, err = sequencePreparedExecute(e, s, ast, &description)
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err != nil || core.DescriptionAnalysis(&description) == before {
				t.Fatal("borrowed current recipe was fenced as an old caller literal", err)
			}
			assertBorrowed()
			result, err = sequencePreparedExecute(e, s, ownedAST, &owned)
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("retired caller literal exposed rows")
			}
			if !errors.Is(err, ids.ErrCatalogReplaced) || core.DescriptionAnalysis(&owned) != previous || !reflect.DeepEqual(captures, owned.Sequences.Constants) {
				t.Fatal("direct caller lifetime fence or atomic capture publication changed", err)
			}
		})
	}
}
