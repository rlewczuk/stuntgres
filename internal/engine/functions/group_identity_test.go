package functions

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// GroupIdentity pins what each node treats as its own grouping identity; the
// engine supplies child comparison through same.

func identical(a, b expr.Expr) bool { return a == b }

func TestGroupIdentityPrecisionOnlyUnderDefinitionEquality(t *testing.T) {
	d := temporalTestTypes()
	info := expr.Info{Usage: d[sqltype.Timestamptz].Usage()}
	a := CurrentDateTime{Info: info, Kind: parser.CurrentTimestamp, HasPrecision: true, Precision: 2}
	b := a
	b.Precision = 3
	if !a.SameGroup(b, identical, false) {
		t.Error("GROUP BY matching distinguished precisions")
	}
	if a.SameGroup(b, identical, true) {
		t.Error("CHECK definition equality ignored precision")
	}
	b.HasPrecision = false
	if a.SameGroup(b, identical, false) {
		t.Error("precision presence ignored")
	}
}

func TestGroupIdentityIgnoresPresentation(t *testing.T) {
	types := formatUnitTypes()
	bytea := types[sqltype.Bytea]
	plan, err := sqltype.BindBinary("btrim", bytea.Usage(), bytea.Usage(), bytea.Usage())
	if err != nil {
		t.Fatal(err)
	}
	arg := binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}}
	call := Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: plan, Args: []expr.Expr{arg, arg}}
	trim := call
	trim.Syntax = parser.SQLFunctionSyntax
	if !call.SameGroup(trim, identical, false) {
		t.Error("TRIM spelling changed grouping identity")
	}
	other := binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage(), Pos: parser.Position{Offset: 7, Character: 8}}}
	trim.Args = []expr.Expr{arg, other}
	if call.SameGroup(trim, identical, false) {
		t.Error("children not compared through same")
	}
	if call.SameGroup(String{Info: call.Info}, identical, false) {
		t.Error("different node types matched")
	}
}

func TestGroupIdentityTimezoneLocalIgnoresZone(t *testing.T) {
	d := temporalTestTypes()
	value := temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamptz], 0))
	utc := temporalTestLiteral(sqltype.String(d[sqltype.Text], "UTC"))
	cet := temporalTestLiteral(sqltype.String(d[sqltype.Text], "CET"))
	info := expr.Info{Usage: d[sqltype.Timestamp].Usage()}
	local := Timezone{Info: info, Value: value, Local: true}
	if !local.SameGroup(Timezone{Info: info, Value: value, Zone: utc, Local: true}, identical, false) {
		t.Error("AT LOCAL compared a zone")
	}
	at := Timezone{Info: info, Value: value, Zone: utc}
	if at.SameGroup(Timezone{Info: info, Value: value, Zone: cet}, identical, false) {
		t.Error("AT TIME ZONE ignored its zone")
	}
	if at.SameGroup(local, identical, false) {
		t.Error("AT LOCAL matched AT TIME ZONE")
	}
}
