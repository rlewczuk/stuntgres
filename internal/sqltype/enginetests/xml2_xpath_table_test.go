package enginetests

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func xpathTableShape(t *testing.T, usages ...sqltype.Usage) *sqltype.Descriptor {
	t.Helper()
	members := make([]sqltype.CompositeMember, len(usages))
	for i, usage := range usages {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: string(rune('a' + i)), Usage: usage}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func TestXML2XPathTableTextInput(t *testing.T) {
	ctx := context.Background()
	types := txidSnapshotTypes()
	text, integer := types[sqltype.Text], types[sqltype.Int4]
	shape := xpathTableShape(t, text.Usage(), integer.Usage())
	key, err := integer.Input(ctx, "7", -1)
	if err != nil {
		t.Fatal(err)
	}
	source := []sqltype.Value{key, sqltype.String(text, "<doc><a>10</a><a>20</a></doc>")}
	notice := func(d sqlstate.Error) { t.Fatal("unexpected notice", d) }
	rows, err := sqltype.XML2Table(ctx, source, shape, "/doc/a", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, notice)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for i, want := range []string{"(7,10)", "(7,20)"} {
		got, err := shape.Output(ctx, rows[i])
		if err != nil || got != want || rows[i].CompositeField(0).Type() != text || rows[i].CompositeField(1).Type() != integer {
			t.Fatal("actual source OUTPUT / member INPUT", got, err)
		}
	}
	held := rows[0]
	source[1] = sqltype.String(text, "<doc><a>x</a></doc>")
	failed, err := sqltype.XML2Table(ctx, source, shape, "/doc/a", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, notice)
	var diagnostic *sqlstate.Error
	if failed != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != `invalid input syntax for type integer: "x"` {
		t.Fatal("input error/atomic result", failed, err)
	}
	if got, err := shape.Output(ctx, held); err != nil || got != "(7,10)" {
		t.Fatal("later input mutated owned accepted tuple", got, err)
	}
	source = []sqltype.Value{key, sqltype.String(text, "<doc><a>10</a></doc>"), key, sqltype.String(text, "<doc><a>x</a></doc>")}
	if failed, err := sqltype.XML2Table(ctx, source, shape, "/doc/a", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, notice); err == nil || failed != nil {
		t.Fatal("later document published earlier output", failed, err)
	}
}

type xpathNullCheck struct {
	values  []sqltype.Value
	failure error
}

func (c *xpathNullCheck) CheckDomain(_ context.Context, _ *sqltype.Descriptor, value sqltype.Value) error {
	c.values = append(c.values, value)
	return c.failure
}

func TestXML2XPathTableNullDomain(t *testing.T) {
	types := txidSnapshotTypes()
	text := types[sqltype.Text]
	domain, err := sqltype.Domain(90001, "table_domain", "table_domain", text.Usage(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	shape := xpathTableShape(t, text.Usage(), text.Usage(), domain.Usage())
	failure := errors.New("domain NULL constraint")
	checks := &xpathNullCheck{failure: failure}
	source := []sqltype.Value{sqltype.String(text, "key"), sqltype.String(text, "<doc><a>x</a></doc>")}
	rows, err := sqltype.XML2Table(context.Background(), source, shape, "/doc/a", sqltype.InputEnvironment{DomainChecks: checks}, sqltype.OutputEnvironment{}, func(sqlstate.Error) {})
	if rows != nil || err != failure || len(checks.values) != 1 || !checks.values[0].IsNull() || checks.values[0].Type() != domain {
		t.Fatal("spare NULL skipped domain input", rows, checks.values, err)
	}
	checks.values = nil
	rows, err = sqltype.XML2Table(context.Background(), nil, shape, "[", sqltype.InputEnvironment{DomainChecks: checks}, sqltype.OutputEnvironment{}, func(sqlstate.Error) {})
	if err != nil || len(rows) != 0 || len(checks.values) != 0 {
		t.Fatal("empty source invoked a domain", rows, checks.values, err)
	}
}

func TestXML2XPathTableConversionCancellation(t *testing.T) {
	types := txidSnapshotTypes()
	text := types[sqltype.Text]
	shape := xpathTableShape(t, text.Usage(), text.Usage(), text.Usage())
	source := []sqltype.Value{sqltype.String(text, "key"), sqltype.String(text, "<doc><a>x</a><a>y</a></doc>")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var notices []sqlstate.Error
	rows, err := sqltype.XML2Table(ctx, source, shape, "/doc/a|count(/doc/a)", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, func(d sqlstate.Error) { notices = append(notices, d); cancel() })
	if rows != nil || !errors.Is(err, context.Canceled) || len(notices) != 1 || notices[0].Message != "unsupported XQuery result: 3" {
		t.Fatal("notice-admitted cancellation", rows, notices, err)
	}
	var healthy []sqlstate.Error
	rows, err = sqltype.XML2Table(context.Background(), source, shape, "/doc/a|count(/doc/a)", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, func(d sqlstate.Error) { healthy = append(healthy, d) })
	want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "unsupported XQuery result: 3"}
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(healthy, []sqlstate.Error{want, want, want}) {
		t.Fatal("healthy independent invocation", rows, healthy, err)
	}
}
