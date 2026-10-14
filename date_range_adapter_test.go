package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDateRangeAdapterEnvironmentDiagnostics(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	d, _ := e.CatalogRead().LookupType(sqltype.DateRange)
	v, err := importParameter(ctx, "[01/02/03,01/03/03)", d.Usage(), sqltype.InputEnvironment{DateStyle: sqltype.DateStyle{Order: sqltype.DateDMY}})
	if err != nil { t.Fatal(err) }
	out, err := exportValue(ctx, v, sqltype.OutputEnvironment{DateStyle: sqltype.DateStyle{Style: sqltype.DateGerman, Order: sqltype.DateDMY}})
	if err != nil || out != "[01.02.2003,01.03.2003)" { t.Fatal(out, err) }
	for _, tc := range []struct{ input, code, message string }{
		{"[bad,2000-01-01)", "22007", `invalid input syntax for type date: "bad"`},
		{"[5874897-12-31,5874897-12-31]", "22008", "date out of range"},
		{"[infinity,-infinity]", "22000", "range lower bound must be less than or equal to range upper bound"},
	} {
		importer := frameImporter([]driver.Value{tc.input})
		err := importer(ctx, engine.ParameterInfo{Types: []sqltype.Usage{d.Usage()}}, make([]sqltype.Value, 1), sqltype.InputEnvironment{})
		want := Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Context: `unnamed portal parameter $1 = '...'`}
		var got *Error
		if !errors.As(err, &got) || *got != want { t.Fatalf("got=%#v want=%#v err=%v", got, want, err) }
	}
}
