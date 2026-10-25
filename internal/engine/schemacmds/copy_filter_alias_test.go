package schemacmds_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A unit evaluator supplies the exact CHECK-body cause to the real scoped
// adapter. It does not pretend the public domain evaluator captures regclass.
type copyAliasInputFailure struct {
	err   error
	calls int
}

func (f *copyAliasInputFailure) CheckDomain(ctx context.Context, _ *sqltype.Descriptor, _ sqltype.Value) error {
	f.calls++
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.err
}
func (f *copyAliasInputFailure) ResolveRegclass(ctx context.Context, _ string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return 0, f.err
}

func TestCopyFilterAliasProvenance(t *testing.T) {
	missing := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "missing" does not exist`}
	soft := sqlstate.SoftInputError(missing)
	primitive := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "bad primitive input"}
	typmod := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "bad type modifier"}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"unmarked lookup", missing, false},
		{"producer soft", soft, true},
		{"hard outer", sqlstate.HardInputError(soft), false},
		{"soft cannot override hard", sqlstate.SoftInputError(sqlstate.HardInputError(soft)), false},
		{"unrelated wrapper", fmt.Errorf("CHECK: %w", soft), false},
		{"primitive", primitive, true},
		{"hard primitive-shaped helper", sqlstate.HardInputError(primitive), false},
		{"hard typmod", sqlstate.HardInputError(typmod), false},
	} {
		if got := sqltype.InputSoftError(tc.err); got != tc.want {
			t.Fatal(tc.name, got, tc.want)
		}
	}
	var diagnostic *sqlstate.Error
	if !errors.As(soft, &diagnostic) || diagnostic != missing || !errors.Is(soft, missing) || soft.Error() != missing.Error() || soft.(interface{ SQLState() string }).SQLState() != "42P01" {
		t.Fatal("lost public error cause", soft)
	}
	copied := *missing
	copied.Position, copied.Context = "7", "outer context"
	positioned := sqlstate.InputDiagnostic(soft, &copied)
	if !sqltype.InputSoftError(positioned) || missing.Position != "" || !errors.As(positioned, &diagnostic) || *diagnostic != copied {
		t.Fatal("presentation stripped provenance or mutated source", positioned)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("caller error")} {
		if sqlstate.SoftInputError(cause) != cause || sqlstate.HardInputError(cause) != cause {
			t.Fatal("ordinary error identity", cause)
		}
	}

	for _, tc := range []struct {
		text string
		soft bool
	}{
		{"", true}, {"SETOF integer", true}, {"int4(", false}, {"float(0)", false},
	} {
		_, err := parser.ParseTypeName(context.Background(), tc.text, nil)
		if err == nil || sqltype.InputSoftError(err) != tc.soft {
			t.Fatal("type parser origin", tc.text, err)
		}
	}
	_, signatureErr := parser.ParseRegprocedureName("abs", func(string) error { t.Fatal("unexpected operand"); return nil })
	if !sqltype.InputSoftError(signatureErr) {
		t.Fatal("procedure delimiter origin", signatureErr)
	}
	signatureErr = parser.ParseRegOperatorSignature("+", func(string) error { return nil }, func(string) error { t.Fatal("unexpected operand"); return nil })
	if !sqltype.InputSoftError(signatureErr) {
		t.Fatal("operator delimiter origin", signatureErr)
	}
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, `CREATE DOMAIN public.copy_alias_unit AS text CHECK(VALUE<>'')`))
	view := e.CatalogRead()
	domain := domainUnitType(t, view, "copy_alias_unit").SQLType
	evaluator := &copyAliasInputFailure{err: soft}
	adapter := exec.InputDomainChecks(evaluator, view)
	hard := adapter.CheckDomain(context.Background(), domain, sqltype.Null(domain))
	if evaluator.calls != 1 || sqltype.InputSoftError(hard) || !errors.Is(hard, missing) || !errors.As(hard, &diagnostic) || diagnostic != missing {
		t.Fatal("real domain boundary softened alias CHECK cause", hard, evaluator.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.CheckDomain(ctx, domain, sqltype.Null(domain)); err != context.Canceled {
		t.Fatal("domain cancellation identity", err)
	}

	regclass, _ := view.LookupType(sqltype.Regclass)
	array, err := sqltype.Array(63001, "_copy_alias_unit", regclass)
	if err != nil {
		t.Fatal(err)
	}
	composite, err := sqltype.Composite(63002, "copy_alias_record", "public.copy_alias_record", 63003, []sqltype.CompositeMember{{Attribute: 1, Name: "v", Usage: regclass.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []struct {
		typ  *sqltype.Descriptor
		text string
	}{{array, "{missing}"}, {composite, "(missing)"}} {
		for _, cause := range []error{soft, sqlstate.HardInputError(primitive)} {
			_, err := shape.typ.InputWithEnvironment(context.Background(), shape.text, -1, sqltype.InputEnvironment{Regclass: &copyAliasInputFailure{err: cause}})
			if sqltype.InputSoftError(err) != sqltype.InputSoftError(cause) || !errors.Is(err, cause) {
				t.Fatal("nested input stripped source provenance", shape.text, err)
			}
		}
	}
}
