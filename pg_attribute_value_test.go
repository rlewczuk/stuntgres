package stuntgres

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGAttributeCarrier(t *testing.T) {
	typ, ok := engine.New().CatalogRead().LookupType(sqltype.ID(2277))
	if !ok {
		t.Fatal("missing ANYARRAY identity")
	}
	value := sqltype.CatalogResultNull(typ.Usage())
	got, err := exportValue(context.Background(), value, sqltype.OutputEnvironment{})
	if err != nil || got != nil {
		t.Fatal("typed NULL export", got, err)
	}
	if scanType(typ) != reflect.TypeFor[any]() || typ.DatabaseName() != "ANYARRAY" || typ.Concrete() {
		t.Fatal("metadata/admission", scanType(typ), typ.DatabaseName(), typ.Concrete())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := exportValue(ctx, value, sqltype.OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := exportValue(context.Background(), sqltype.Value{}, sqltype.OutputEnvironment{}); err == nil {
		t.Fatal("invalid value exported")
	}
}
