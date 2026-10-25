package engine

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRegroleCapturedAuthority(t *testing.T) {
	e := New()
	aclUnitRoles(e, "unit_role", "Mixed.Role", `a"b`, "select", "10")
	old := e.Roles()
	input := regobj.RoleInput{Roles: old}
	for _, tc := range []struct {
		text string
		id   uint32
	}{{" UNIT_ROLE ", 20000}, {`"Mixed.Role"`, 20001}, {`"a""b"`, 20002}, {`"select"`, 20003}, {`"10"`, 20004}} {
		id, err := input.ResolveRegrole(context.Background(), tc.text)
		if err != nil || id != tc.id {
			t.Fatal(tc.text, id, err)
		}
	}
	for _, tc := range []struct{ text, code, message string }{{"", "42602", "invalid name syntax"}, {"unit_role.x", "42602", "invalid name syntax"}, {`""`, "42704", `role "" does not exist`}, {"MiSsInG", "42704", `role "missing" does not exist`}} {
		_, err := input.ResolveRegrole(context.Background(), tc.text)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(tc.text, err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := input.ResolveRegrole(canceled, "unit_role"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	d := aclUnitType(t, e, sqltype.Regrole)
	arrayType := aclUnitType(t, e, 4097)
	value, err := sqltype.RegroleValue(d, 20000)
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.ArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{value, sqltype.Null(d)})
	if err != nil {
		t.Fatal(err)
	}
	rows := exec.NewFinalRows([]catalog.Column{{Name: "r", Usage: d.Usage()}}, 1)
	defer rows.Close()
	output := regobj.RoleOutput{Owner: &e.Catalog.Owner, Roles: old}
	if err := regobj.CaptureRoleOutputValue(rows, output, array); err != nil {
		t.Fatal(err)
	}
	role, _ := old.Lookup("unit_role")
	e.Mu.Lock()
	next := cloneRoleFixture(e.RoleStore.Published)
	retireFixtureRole(next, role.Ref)
	putFixtureRole(next, authorization.RoleDescriptor{Ref: e.RoleStore.Domain.Principal(30000), Name: "unit_role", Inherit: true, ConnectionLimit: -1})
	e.RoleStore.PublishLocked(next)
	e.Mu.Unlock()
	if id, err := input.ResolveRegrole(context.Background(), "unit_role"); err != nil || id != 20000 {
		t.Fatal("old immutable input", id, err)
	}
	fresh := regobj.RoleInput{Roles: e.Roles()}
	if id, err := fresh.ResolveRegrole(context.Background(), "unit_role"); err != nil || id != 30000 {
		t.Fatal("fresh input", id, err)
	}
	if text, err := rows.FormatRegrole(20000); err != nil || text != "unit_role" {
		t.Fatal("retained output", text, err)
	}
	current := regobj.RoleOutput{Owner: &e.Catalog.Owner, Roles: e.Roles()}
	for _, tc := range []struct {
		id   uint32
		text string
	}{{0, "-"}, {20000, "20000"}, {30000, "unit_role"}, {20001, `"Mixed.Role"`}, {20002, `"a""b"`}, {20003, `"select"`}, {20004, `"10"`}, {4294967295, "4294967295"}} {
		if text, err := current.FormatRegrole(tc.id); err != nil || text != tc.text {
			t.Fatal(tc.id, text, err)
		}
	}
	current.QuoteAll = true
	if text, err := current.FormatRegrole(30000); err != nil || text != `"unit_role"` {
		t.Fatal("forced quoting", text, err)
	}
	foreign := regobj.RoleOutput{Owner: &New().Catalog.Owner, Roles: old}
	if _, err := foreign.FormatRegrole(20000); err == nil {
		t.Fatal("foreign output authority admitted")
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	pos := parser.Position{Offset: 7, Character: 8}
	captured, err := binder.InputConstant("unit_role", d.Usage(), pos)
	if err != nil {
		t.Fatal(err)
	}
	description := NewDescription(DescriptionFields{Temporals: append([]analyze.TemporalConstant(nil), binder.Prepared.Temporals...), CatalogOwner: &e.Catalog.Owner, CatalogEpoch: e.CatalogRead().Epoch()})
	retained := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, description)
	again, err := retained.InputConstant("not_resolved_again", d.Usage(), pos)
	if err != nil || !sqltype.SameDatum(captured, again) {
		t.Fatal("prepared identity retention", err)
	}
	if !analyze.RefreshableTemporal(binder.Prepared.Temporals[0]) {
		t.Fatal("full analysis invalidation cannot refresh role literal")
	}
	textType := aclUnitType(t, e, sqltype.Text)
	text := plan.LiteralExpr{Info: expr.Info{Usage: textType.Usage()}, Value: sqltype.String(textType, "unit_role")}
	coercionPlan, err := sqltype.PlanCoercion(textType.Usage(), d.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	runtime := plan.CoercionExpr{Info: expr.Info{Usage: d.Usage()}, Child: text, Plan: coercionPlan, Mode: sqltype.Explicit}
	snapshot := false
	if _, err := (stored.ScalarRenderer{CommentSnapshot: &snapshot}).SQL(context.Background(), e.CatalogRead(), runtime); err != nil || !snapshot {
		t.Fatal("stored runtime input omitted statement snapshot demand", snapshot, err)
	}
	scope := &StatementScope{Input: sqltype.InputEnvironment{Regrole: fresh}}
	resolved, err := runtime.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: HostOf(scope)}})
	if err != nil {
		t.Fatal("stored runtime input propagation", err)
	}
	if id, err := resolved.RoleOID(); err != nil || id != 30000 {
		t.Fatal("runtime used noncurrent role authority", id, err)
	}
}

func TestRegroleStoredLiteral(t *testing.T) {
	e := New()
	d := aclUnitType(t, e, sqltype.Regrole)
	arrayType := aclUnitType(t, e, 4097)
	view := e.CatalogRead()
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "constant of the type regrole cannot be used here"}
	for _, id := range []uint32{0, 10, 4294967295} {
		value, _ := sqltype.RegroleValue(d, id)
		if got := analyze.ValidateRegroleStoredLiteral(value); !reflect.DeepEqual(got, want) {
			t.Fatal("complete stored diagnostic", got)
		}
		literal := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: value}
		if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, literal); !reflect.DeepEqual(err, want) {
			t.Fatal("renderer boundary", err)
		}
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: strconv.FormatUint(uint64(id), 10)}}
		if err := binder.CaptureViewLiterals(raw, literal, analyze.ViewTargetLiteral, 0); !reflect.DeepEqual(err, want) {
			t.Fatal("view freeze boundary", err)
		}
		// Data serialization is legal even when the exact same datum is banned
		// as a stored expression's scalar Const.
		if _, err := regobj.Literal(context.Background(), view, literal); err != nil {
			t.Fatal("row data codec rejected role identity", err)
		}
	}
	value, _ := sqltype.RegroleValue(d, 10)
	array, err := sqltype.ArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{value})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(60000, "unit_role_domain", "UNIT_ROLE_DOMAIN", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(value)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := wrapped.RoleOID(); err != nil || id != 10 || wrapped.Type() != domain {
		t.Fatal("domain wrapper lost owning descriptor or physical identity", id, err)
	}
	for _, allowed := range []sqltype.Value{sqltype.Null(d), array, wrapped, sqltype.Null(arrayType)} {
		if err := analyze.ValidateRegroleStoredLiteral(allowed); err != nil {
			t.Fatal("non-scalar-Const rejected", err)
		}
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: value}
	textType := aclUnitType(t, e, sqltype.Text)
	coercionPlan, err := sqltype.PlanCoercion(d.Usage(), textType.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	outer := plan.CoercionExpr{Info: expr.Info{Usage: textType.Usage()}, Child: literal, Plan: coercionPlan, Mode: sqltype.Explicit}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, outer); !reflect.DeepEqual(err, want) {
		t.Fatal("outer cast hid prohibited child", err)
	}
	arrayLiteral := plan.LiteralExpr{Info: expr.Info{Usage: arrayType.Usage()}, Value: array}
	renderer := stored.QueryRenderer{View: stored.NewView(stored.ViewParts{
		Literals:   map[analyze.ViewLiteralPath]plan.LiteralExpr{{Node: 1}: arrayLiteral},
		CastUsages: map[analyze.ViewLiteralPath]sqltype.Usage{{}: arrayType.Usage()},
	})}
	raw := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue}}}
	ordinal := 0
	text, err := renderer.ExpressionSQLAt(context.Background(), view, raw, analyze.ViewLiteralPath{}, &ordinal, stored.DeparseParent{})
	if err != nil || text != "CAST('{10}' AS REGROLE[])" {
		t.Fatal("opaque role array acquired redundant replay cast", text, err)
	}
}
