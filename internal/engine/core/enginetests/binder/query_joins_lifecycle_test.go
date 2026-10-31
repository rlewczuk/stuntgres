package binder_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestQueryJoinsParameterAnalysis(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE a(l TEXT,k INT)", "CREATE TABLE b(r TEXT,k INT)"} {
		enginetest.Run(t, e, q)
	}
	s := enginetest.StartSession(t, e, nil)
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	for _, tc := range []struct {
		q      string
		result sqltype.ID
	}{
		{"SELECT a.l FROM a JOIN b ON a.k=$1 AND a.k=b.k", sqltype.Text},
		{"SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", sqltype.Int4},
	} {
		description, err := e.Describe(op, enginetest.Statement(t, tc.q))
		if err != nil || description.Parameters.Count != 1 || len(description.Parameters.Types) != 1 || description.Parameters.Types[0].Type.ID() != sqltype.Int4 || len(description.Results) != 1 || description.Results[0].Usage.Type.ID() != tc.result {
			t.Fatal(tc, description, err)
		}
	}
}
