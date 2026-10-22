package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestCopyFilterInputProvenance(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		`CREATE DOMAIN public.copy_input_positive AS int CONSTRAINT positive CHECK(VALUE>0)`,
		`CREATE DOMAIN public.copy_input_cast AS text CHECK(VALUE::int>0)`,
		`CREATE DOMAIN public.copy_input_nested AS text CHECK(VALUE::public.copy_input_positive>0)`,
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}
	integer, _ := view.LookupType(sqltype.Int4)
	positive := domainUnitType(t, view, "copy_input_positive").SQLType
	cast := domainUnitType(t, view, "copy_input_cast").SQLType
	nested := domainUnitType(t, view, "copy_input_nested").SQLType
	input := evaluation.Input
	input.DomainChecks = exec.InputDomainChecks(evaluation, view)
	for _, tc := range []struct {
		typ         *sqltype.Descriptor
		value, code string
		soft        bool
	}{
		{integer, "bad", "22P02", true},
		{cast, "bad", "22P02", false},
		{positive, "-1", "23514", true},
		{nested, "-1", "23514", false},
	} {
		_, err := tc.typ.InputWithEnvironment(evaluation.Context, tc.value, -1, input)
		var record *sqlstate.Error
		if !errors.As(err, &record) || record.Code != tc.code || sqltype.InputSoftError(err) != tc.soft {
			t.Fatal("provenance", tc.typ.SQLName(), err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := input.DomainChecks.CheckDomain(ctx, positive, sqltype.Null(integer)); err != context.Canceled {
		t.Fatal("cancellation identity", err)
	}
}
