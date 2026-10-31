package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestVerifyHeapamAuthorityCatalogKinds(t *testing.T) {
	e, _, _ := typedUnitCatalog(t, false)
	heapUnitInstalled(t, e)
	n := heapUnitExpression(t, e)
	view := e.CatalogRead()
	reader, _ := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	for _, tc := range []struct {
		oid                         ids.OID
		name, code, message, detail string
	}{
		{1259, "pg_class", "0A000", "verify_heapam requires stored heap data", ""},
		{12081, "pg_available_extensions", "42809", `cannot check relation "pg_available_extensions"`, "This operation is not supported for views."},
		{12003, "pg_tables", "42809", `cannot check relation "pg_tables"`, "This operation is not supported for views."},
	} {
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tc.oid})
		if d.Name() != tc.name || d.RelationKind != catalog.CatalogRelation {
			t.Fatal("not an actual catalog execution descriptor", d)
		}
		for _, physical := range []bool{false, true} {
			args := heapUnitArgs(t, e, d, false)
			args[2] = sqltype.Boolean(enginetest.Usage(e, sqltype.Bool).Type, physical)
			_, err := scope.WriteHost().Contrib().VerifyHeapam(args, n.Usage.Type)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
			if !reflect.DeepEqual(err, want) {
				t.Fatal("projected kind/availability precedence", tc.name, physical, err, want)
			}
		}
	}
}
