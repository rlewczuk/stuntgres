package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"testing"
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
			e := New()
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
				if description.sequences != nil || description.regtypes != nil {
					t.Fatal("stored recipe entered caller literal provenance", description.sequences, description.regtypes)
				}
			}
			assertBorrowed()
			input := catalogRef(t, e, e.CatalogRead(), "input")
			stored := rulesOf(e.CatalogRead().MustRelation(input.Address))[0]
			if len(stored.Actions()[0].Binding().Prepared.SequenceConstants) == 0 {
				t.Fatal("recipe lost its own sequence capture")
			}
			if tc.conditional && (stored.ConditionBinding() == nil || len(stored.ConditionBinding().Prepared.SequenceConstants) == 0) {
				t.Fatal("condition lost its recipe capture")
			}
			ownedAST, owned := sequencePreparedDescription(t, e, s, query("nextval('own_draw')"))
			ownedRef := catalogRef(t, e, e.CatalogRead(), "own_draw")
			assertOwned := func() {
				if owned.sequences == nil || len(owned.sequences.Constants) != 1 || owned.sequences.Constants[0].Ref != ownedRef || owned.regtypes != nil {
					t.Fatal("raw caller/CTE capture group polluted or lost", owned.sequences, owned.regtypes)
				}
			}
			assertOwned()
			_, regtype := sequencePreparedDescription(t, e, s, "SELECT 'integer'::regtype")
			if regtype.regtypes == nil || len(regtype.regtypes.constants) != 1 {
				t.Fatal("direct regtype capture was discarded")
			}
			before := description.analysis
			sessionSQL(t, s, "COMMENT ON TABLE t IS 'invalidate rule target dependency'")
			result, err := sequencePreparedExecute(e, s, ast, &description)
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err != nil || description.analysis == before {
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
			previous := owned.analysis
			captures := append([]sequence.Constant(nil), owned.sequences.Constants...)
			before = description.analysis
			if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			setup()
			result, err = sequencePreparedExecute(e, s, ast, &description)
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err != nil || description.analysis == before {
				t.Fatal("borrowed current recipe was fenced as an old caller literal", err)
			}
			assertBorrowed()
			result, err = sequencePreparedExecute(e, s, ownedAST, &owned)
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("retired caller literal exposed rows")
			}
			if !errors.Is(err, ids.ErrCatalogReplaced) || owned.analysis != previous || !reflect.DeepEqual(captures, owned.sequences.Constants) {
				t.Fatal("direct caller lifetime fence or atomic capture publication changed", err)
			}
		})
	}
}
