package analyze

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestCallArgumentSetAdmission(t *testing.T) {
	for _, clause := range []string{"", "SELECT", "FROM"} {
		for _, common := range []bool{false, true} {
			b := &Binder{}
			b.Consumer.CallArguments = true
			b.Clause.AggregateClause = clause
			pos := parser.Position{Offset: 12, Character: 13}
			var err error
			if common {
				err = b.AdmitSetReturning(pos)
			} else {
				err = b.stringSetAllowed(pos)
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "set-returning functions are not allowed in CALL arguments" || diagnostic.Position != "13" || diagnostic.Hint != "" {
				t.Fatalf("clause=%q common=%t: diagnostic=%#v", clause, common, diagnostic)
			}
			if b.Clause.QueryHasTargetSet || b.Clause.AggregateClause != clause || !b.Consumer.CallArguments {
				t.Fatal("rejected CALL argument changed admission state")
			}
		}
	}
}
