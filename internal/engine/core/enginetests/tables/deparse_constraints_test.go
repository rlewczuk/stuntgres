package tables_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type deparseTrap struct {
	expr.Info
	calls *int
}

func (n deparseTrap) Eval(expr.Context) (sqltype.Value, error) {
	*n.calls++
	panic("stored tree evaluated")
}

func TestDeparseConstraintsNoEvalAndCarrierLifetime(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT DEFAULT (1/0),CONSTRAINT c CHECK(a>0))")
	view := e.CatalogRead()
	d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
	treeType, _ := view.LookupType(sqltype.PgNodeTree)
	saved, err := plan.CatalogTree(treeType, d.ColumnMeta.Defaults[1].Expression)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "DROP TABLE t")
	payload, err := saved.Tree()
	if err != nil {
		t.Fatal(err)
	}
	got, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), payload.(plan.BoundTree).Expression, stored.DeparseParent{})
	if err != nil || got != "(1 / 0)" {
		t.Fatal(got, err)
	}
	structural, err := treeType.Output(context.Background(), saved)
	if err != nil || !strings.Contains(structural, "Arithmetic") || strings.Contains(structural, "SELECT") {
		t.Fatal(structural, err)
	}
	calls := 0
	trap := deparseTrap{Info: expr.Info{Usage: d.Columns[0].Usage}, calls: &calls}
	if _, err := (stored.Deparser{View: view}).SQL(context.Background(), trap, stored.DeparseParent{}); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	if plan.StructuralNull(trap) || calls != 0 {
		t.Fatal("NULL producer evaluated")
	}
	for _, input := range []string{"", "x", string([]byte{0xff, 0})} {
		_, err := treeType.Input(context.Background(), input, -1)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cannot accept a value of type pg_node_tree" {
			t.Fatal(err)
		}
	}
}

func TestDeparseConstraintsDefinitionIdentityAndNullProducer(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT DEFAULT NULL,b INT DEFAULT (NULL::integer),c INT DEFAULT COALESCE(NULL,1),d INT NOT NULL,CHECK(c>0))")
	view := e.CatalogRead()
	d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
	if len(d.ColumnMeta.Defaults) != 1 || d.ColumnMeta.Defaults[3].OID == 0 || d.Constraints.Check[0].OID == 0 || d.Constraints.NotNull[0].OID == 0 {
		t.Fatal(d.ColumnMeta.Defaults, d.Constraints.Check, d.Constraints.NotNull)
	}
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	old := d.ColumnMeta.Defaults[3].OID
	enginetest.Run(t, e, "ALTER TABLE t ALTER c SET DEFAULT 3")
	next := e.CatalogRead().MustRelation(d.Address())
	if next.ColumnMeta.Defaults[3].OID == old || next.Constraints.Check[0].OID != d.Constraints.Check[0].OID {
		t.Fatal("definition identity replacement/preservation")
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER c SET DEFAULT NULL")
	if len(e.CatalogRead().MustRelation(d.Address()).ColumnMeta.Defaults) != 0 {
		t.Fatal("NULL definition persisted")
	}
}

func TestDeparseConstraintsStrictnessAndSuppliedAttributes(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	textType, _ := view.LookupType(sqltype.Text)
	oidType, _ := view.LookupType(sqltype.Oid)
	treeType, _ := view.LookupType(sqltype.PgNodeTree)
	boolType, _ := view.LookupType(sqltype.Bool)
	literal := func(v sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: v.Type().Usage()}, Value: v}
	}
	zero, _ := sqltype.OIDValue(oidType, 0)
	tree, err := plan.CatalogTree(treeType, literal(zero))
	if err != nil {
		t.Fatal(err)
	}
	for _, constraint := range []bool{false, true} {
		args := []expr.Expr{literal(tree), literal(zero), literal(sqltype.Boolean(boolType, false))}
		if constraint {
			args = []expr.Expr{literal(zero), literal(sqltype.Boolean(boolType, false))}
		}
		signature := functions.DeparseExprPretty
		if constraint {
			signature = functions.DeparseConstraintPretty
		}
		for nullable := range args {
			test := append([]expr.Expr(nil), args...)
			test[nullable] = literal(sqltype.Null(test[nullable].ResultUsage().Type))
			node := functions.Deparse{Info: expr.Info{Usage: textType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Signature: signature, Args: test}
			value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			if err != nil || !value.IsNull() || value.Type() != textType {
				t.Fatal(constraint, nullable, value, err)
			}
		}
	}
	enginetest.Run(t, e, "CREATE TABLE t(prefix TEXT,a INT,b INT,CONSTRAINT c CHECK(b>0))")
	original := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
	node := original.Constraints.Check[0].Expression
	enginetest.Run(t, e, "ALTER TABLE t DROP prefix")
	enginetest.Run(t, e, `CREATE TABLE supplied(x TEXT,y TEXT,"Renamed value" TEXT)`)
	current := e.CatalogRead()
	supplied := current.MustRelation(enginetest.RoleTableRef(t, e, "supplied").Address)
	got, err := (stored.Deparser{View: current, Relation: supplied, Pretty: true}).SQL(context.Background(), node, stored.DeparseParent{})
	if err != nil || got != `"Renamed value" > 0` {
		t.Fatal(got, err)
	}
	if _, err := (stored.Deparser{View: current}).SQL(context.Background(), node, stored.DeparseParent{}); err == nil {
		t.Fatal("Var accepted with zero relation")
	}
	before := e.Catalog.NextOID()
	if _, err := enginetest.Describe(e, enginetest.Statement(t, "SELECT pg_get_expr($1,$2,$3)")); err != nil {
		t.Fatal(err)
	}
	if e.Catalog.NextOID() != before {
		t.Fatal("Describe allocated definition identities")
	}
}

func TestDeparseConstraintsAllocatorOccupancy(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT DEFAULT 1 NOT NULL,CHECK(a>0))")
	view := e.CatalogRead()
	d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
	for _, id := range []ids.OID{d.ColumnMeta.Defaults[1].OID, d.Constraints.NotNull[0].OID, d.Constraints.Check[0].OID} {
		e.Mu.Lock()
		e.Catalog.InstallOIDFrontierLocked(id)
		next, err := e.Catalog.AllocateOIDLocked()
		e.Mu.Unlock()
		if err != nil || next == id || catalog.DefinitionOIDUsed(d, next) {
			t.Fatal(id, next, err)
		}
	}
	bad := catalog.CloneState(view.State())
	copy := *d
	copy.Constraints.Check = append([]catalog.CheckConstraint(nil), d.Constraints.Check...)
	copy.Constraints.Check[0].OID = d.ColumnMeta.Defaults[1].OID
	bad.SetDescriptor(d.Address(), &copy)
	if err := catalog.ValidateDefinitionOIDs(e.Catalog.Read(bad)); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	e.Mu.Lock()
	err := control.RemapDefinitionOIDs(e.Catalog.Read(bad), e.Catalog.AllocateOIDLocked)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateDefinitionOIDs(e.Catalog.Read(bad)); err != nil {
		t.Fatal(err)
	}
	if d.Constraints.Check[0].OID == d.ColumnMeta.Defaults[1].OID {
		t.Fatal("remap mutated original")
	}
}
