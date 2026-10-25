package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestCopyFilterProviderProvenance(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, `CREATE DOMAIN public.copy_provider_proof AS text CHECK(VALUE<>'')`))
	view := e.CatalogRead()
	domain := domainUnitType(t, view, "copy_provider_proof").SQLType
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	acl := e.CaptureACLItemInput(auth, op)
	e.Mu.Unlock()
	input := sqltype.InputEnvironment{ACLItem: acl}
	for _, tc := range []struct {
		id         sqltype.ID
		text, code string
		typmod     int32
	}{
		{1007, "[1:0]={}", "2202E", -1},
		{1007, "[2147483648:2147483648]={1}", "54000", -1},
		{sqltype.Int4Range, "[3,1)", "22000", -1},
		{sqltype.JSONPath, "last", "42601", -1},
		{sqltype.JSONPath, `$ ? (@ like_regex "(")`, "2201B", -1},
		{sqltype.JSONPath, `$ ? (@ like_regex "a" flag "x")`, "0A000", -1},
		{sqltype.Bit, "1", "22026", 2},
		{sqltype.JSONB, `"\u0000"`, "22P05", -1},
		{sqltype.TSVector, strings.Repeat("x", 2047), "54000", -1},
		{sqltype.ACLItem, strings.Repeat("n", 64) + "=r/postgres", "42622", -1},
		{sqltype.ACLItem, "copy_provider_absent=r/postgres", "42704", -1},
	} {
		typ, ok := view.LookupType(tc.id)
		if !ok {
			t.Fatal("missing actual provider", tc.id)
		}
		_, cause := typ.InputWithEnvironment(context.Background(), tc.text, tc.typmod, input)
		copyFilterProviderDomainCause(t, view, domain, cause, tc.code)
	}
	query, err := sqltype.QueryInt(63002, "copy_provider_query", "public.copy_provider_query")
	if err != nil {
		t.Fatal(err)
	}
	_, cause := query.InputWithEnvironment(context.Background(), strings.Repeat("!", 17)+"1", -1, input)
	copyFilterProviderDomainCause(t, view, domain, cause, "54001")
}

func TestCopyFilterProviderReparseDiagnosticPosition(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT '[1:0]={}'::integer[]").(*parser.Select).Targets[0]
	_, cause := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	var original *sqlstate.Error
	want := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2202E",
		Message: "upper bound cannot be less than lower bound", Position: "8",
	}
	if !errors.As(cause, &original) || *original != want || !sqltype.InputSoftError(cause) {
		t.Fatal("actual bound array literal lost source diagnostic", cause)
	}
	cooked := table.AlterTypeCheckReparseError(cause)
	var diagnostic *sqlstate.Error
	expected := want
	expected.Position = ""
	if !errors.As(cooked, &diagnostic) || *diagnostic != expected || diagnostic == original || *original != want || !sqltype.InputSoftError(cooked) {
		t.Fatal("CHECK reparse changed source authority or failed to clear its internal source position", cooked, original)
	}
	if table.AlterTypeCheckReparseError(context.Canceled) != context.Canceled {
		t.Fatal("CHECK reparse changed cancellation identity")
	}
}

// Real provider errors enter the real scoped adapter; only the CHECK evaluator
// seam supplies that already-produced cause. This is not public SQL emulation.
func copyFilterProviderDomainCause(t *testing.T, view catalog.Read, domain *sqltype.Descriptor, cause error, code string) {
	t.Helper()
	var original *sqlstate.Error
	if !errors.As(cause, &original) || original.Code != code || !sqltype.InputSoftError(cause) {
		t.Fatal("actual provider did not produce the specified source-soft error", code, cause)
	}
	snapshot := *original
	positioned := *original
	positioned.Position = "7"
	cooked := table.AlterCookingError(sqlstate.InputDiagnostic(cause, &positioned))
	var cookedDiagnostic *sqlstate.Error
	if !sqltype.InputSoftError(cooked) || !errors.As(cooked, &cookedDiagnostic) || cookedDiagnostic.Position != "" || cookedDiagnostic.Context != snapshot.Context {
		t.Fatal("DDL cooking stripped source origin or diagnostic context", code, cooked)
	}
	evaluation := &copyAliasInputFailure{err: cause}
	adapter := exec.InputDomainChecks(evaluation, view)
	hard := adapter.CheckDomain(context.Background(), domain, sqltype.Null(domain))
	var diagnostic *sqlstate.Error
	if evaluation.calls != 1 || sqltype.InputSoftError(hard) || !errors.Is(hard, cause) || !errors.As(hard, &diagnostic) || diagnostic != original || *original != snapshot {
		t.Fatal("scoped domain lost hard authority, cause, diagnostic or evaluator-once", code, hard, evaluation.calls)
	}
	if marked, ok := hard.(*sqlstate.InputError); !ok || marked.Recoverable() {
		t.Fatal("domain did not retain the outer hard source marker", code, hard)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.CheckDomain(ctx, domain, sqltype.Null(domain)); err != context.Canceled {
		t.Fatal("scoped cancellation identity changed", err)
	}
}
