package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestLanguageInquirySnapshotOwnership(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TRUSTED LANGUAGE language_inquiry HANDLER pg_catalog.plpgsql_call_handler")
	enginetest.Run(t, e, "CREATE FUNCTION language_inquiry_bad(x integer) RETURNS integer LANGUAGE language_inquiry AS 'BEGIN RETURN 1;'")
	enginetest.Run(t, e, "CREATE FUNCTION language_inquiry_result() RETURNS cstring LANGUAGE language_inquiry AS 'BEGIN RETURN NULL; END'")
	old := e.CatalogRead()
	public := old.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	find := func(name string) *catalog.Procedure {
		t.Helper()
		address := old.State().ProceduresNamed(public, name)[0]
		d, ok := old.Procedure(address)
		if !ok {
			t.Fatal("missing source declaration", name)
		}
		return d
	}
	bad, result := find("language_inquiry_bad"), find("language_inquiry_result")
	badDefinition, resultDefinition := bad.SQL, result.SQL
	formal := &badDefinition.Formals[0]
	alias, err := catalog.LookupLanguage(old, "language_inquiry")
	if err != nil {
		t.Fatal(err)
	}
	round, ok := old.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1708})
	if !ok || round.Language != (ids.ObjectAddress{}) || round.DeclarationBody == nil {
		t.Fatal("missing genuine native SQL declaration")
	}
	body := round.DeclarationBody
	inquiry := func(read catalog.Read, d *catalog.Procedure) string {
		t.Helper()
		path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, public}
		scope := &core.StatementScope{Inquiry: core.NewInquiry(core.InquiryFields{CommentCatalog: read, DeparseAuth: catalogAuthorization(e, read), DeparsePath: path})}
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
		oidType, _ := read.LookupType(sqltype.Oid)
		textType, _ := read.LookupType(sqltype.Text)
		value, err := sqltype.OIDValue(oidType, uint32(d.Address().ObjectID))
		if err != nil {
			t.Fatal(err)
		}
		arg := plan.LiteralExpr{Info: expr.Info{Usage: oidType.Usage()}, Value: value}
		node := functions.Deparse{Info: expr.Info{Usage: textType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Signature: functions.DeparseFunction, Args: []expr.Expr{arg}}
		value, err = node.Eval(ctx)
		if err != nil {
			t.Fatal("declaration inquiry invoked execution admission", err)
		}
		text, err := value.Text()
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	oldBad, oldResult, oldRound := inquiry(old, bad), inquiry(old, result), inquiry(old, round)
	enginetest.Run(t, e, "ALTER LANGUAGE language_inquiry RENAME TO language_inquiry_renamed")
	enginetest.Run(t, e, "ALTER LANGUAGE sql RENAME TO language_inquiry_sql")
	enginetest.Run(t, e, "ALTER LANGUAGE internal RENAME TO language_inquiry_internal")
	enginetest.Run(t, e, "ALTER LANGUAGE c RENAME TO language_inquiry_c")
	current := e.CatalogRead()
	for _, tc := range []struct {
		d        *catalog.Procedure
		language string
	}{
		{bad, "language_inquiry_renamed"}, {result, "language_inquiry_renamed"}, {round, "language_inquiry_sql"},
	} {
		if current.Get(tc.d.Address()) != tc.d || !strings.Contains(inquiry(current, tc.d), "\n LANGUAGE "+tc.language+"\n") {
			t.Fatal("inquiry did not use captured actual language identity", tc.d.Name())
		}
	}
	internal, ok := current.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
	if !ok || internal.Language != (ids.ObjectAddress{}) || !strings.Contains(inquiry(current, internal), "\n LANGUAGE language_inquiry_internal\n") {
		t.Fatal("native internal identity followed display label")
	}
	foundHandler := false
	for handler := range catalog.DefinitionProceduresNamedAt(current, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "plpgsql_call_handler") {
		foundHandler = true
		if handler.Language != (ids.ObjectAddress{}) || !strings.Contains(inquiry(current, handler), "\n LANGUAGE language_inquiry_c\n") {
			t.Fatal("native C identity followed display label")
		}
	}
	if !foundHandler {
		t.Fatal("missing genuine installed C handler declaration")
	}
	if inquiry(old, bad) != oldBad || inquiry(old, result) != oldResult || inquiry(old, round) != oldRound || old.Get(alias.Address()) != alias || alias.Name() != "language_inquiry" {
		t.Fatal("current language presentation mutated retained inquiry snapshot")
	}
	if e.CatalogRead().State() != current.State() || bad.SQL != badDefinition || result.SQL != resultDefinition || &bad.SQL.Formals[0] != formal || bad.SQL.PL != nil || bad.SQL.PLSyntax != nil || result.SQL.PL != nil || result.SQL.PLSyntax != nil || round.DeclarationBody != body {
		t.Fatal("metadata inquiry compiled source or mutated genuine definition backing")
	}
}
