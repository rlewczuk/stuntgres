package schemacmds_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSSLInfoNestedDefaultClosure(t *testing.T) {
	e, _ := sslInfoUnitInstalled(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "pgcrypto"}})
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	extensionUnitPublish(t, e, op, stage)
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	bind := func(sql string) expr.Expr {
		t.Helper()
		raw := enginetest.Statement(t, sql).(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil { t.Fatal(err) }
		return node
	}
	// Bind a real argument tree; this unit inspects admission authority rather
	// than issuing SQL or inferring retention from equal constant outcomes.
	n := bind("SELECT digest(CASE WHEN ssl_is_used() THEN 'tls' ELSE 'plain' END,'sha256')").(plan.InstalledCryptoExpr)
	conditional, ok := n.Args[0].(plan.CaseExpr)
	if !ok || len(conditional.Arms) != 1 { t.Fatal("missing nested conditional", n.Args[0]) }
	leaf, ok := conditional.Arms[0].Condition.(plan.InstalledSSLInfoExpr)
	if !ok { t.Fatal("missing nested installed inquiry", conditional.Arms[0].Condition) }
	if err := stored.ValidateInstalledSSLInfo(leaf, e.CatalogRead()); err != nil { t.Fatal(err) }
	if !analyze.ClosedSSLInfoDefault(leaf) || n.Properties().Volatility != expr.Volatile { t.Fatal("invalid authentic nested fixture") }
	t.Run("Genuine", func(t *testing.T) {
		if !analyze.ClosedPgcryptoDefault(n) { t.Error("crypto owner rejected conditional containing authenticated sslinfo") }
		raw := enginetest.Statement(t, "SELECT digest(CASE WHEN ssl_is_used() THEN 'tls' ELSE 'plain' END,'sha256')").(*parser.Select).Targets[0]
		if _, err := b.BindDefault(raw, catalog.Column{Name: "payload", Usage: b.Type(sqltype.Bytea).Usage()}); err != nil { t.Fatal("actual nested DEFAULT admission", err) }
	})
	withCondition := func(condition expr.Expr) plan.InstalledCryptoExpr {
		next := n
		next.Args = slices.Clone(n.Args)
		branch := conditional
		branch.Arms = slices.Clone(conditional.Arms)
		branch.Arms[0].Condition = condition
		next.Args[0] = branch
		return next
	}
	t.Run("Forged", func(t *testing.T) {
		for _, mutate := range []func(*plan.InstalledSSLInfoExpr){
			func(n *plan.InstalledSSLInfoExpr) { n.RoutineReference = nil },
			func(n *plan.InstalledSSLInfoExpr) {
				p := *n.Procedure
				origin := *p.Origin
				installation := *origin.Installation
				origin.Installation = &installation
				p.Origin = &origin
				n.Procedure, n.RoutineReference = &p, &p
			},
			func(n *plan.InstalledSSLInfoExpr) { n.Name = "ssl_client_cert_present" },
			func(n *plan.InstalledSSLInfoExpr) { n.Ref.Epoch = 0 },
			func(n *plan.InstalledSSLInfoExpr) { n.Usage = b.Type(sqltype.Int4).Usage() },
			func(n *plan.InstalledSSLInfoExpr) { n.Property.RowDependent = true },
			func(n *plan.InstalledSSLInfoExpr) { n.Property.ParameterDependent = true },
			func(n *plan.InstalledSSLInfoExpr) { n.Property.StageDependent = true },
			func(n *plan.InstalledSSLInfoExpr) {
				p := *n.Procedure
				p.Implementation = extension.StockUUIDOssp
				n.Procedure, n.RoutineReference = &p, &p
			},
		} {
			bad := leaf
			mutate(&bad)
			if analyze.ClosedSSLInfoDefault(bad) { t.Fatal("forged or nonclosed leaf accepted") }
			if probe := withCondition(bad); analyze.ClosedPgcryptoDefault(probe) || analyze.ClosedSSLInfoDefault(probe) { t.Fatal("nested crypto admitted forged inquiry") }
		}
	})
	t.Run("BareCarrier", func(t *testing.T) {
		probe := withCondition(leaf.SSLInfo)
		if analyze.ClosedPgcryptoDefault(probe) || analyze.ClosedSSLInfoDefault(probe) { t.Fatal("nested crypto admitted bare inquiry") }
	})
	t.Run("UnrelatedVolatile", func(t *testing.T) {
		probe := withCondition(bind("SELECT random() > 0.5"))
		if analyze.ClosedPgcryptoDefault(probe) || analyze.ClosedSSLInfoDefault(probe) { t.Fatal("nested crypto admitted unrelated volatile producer") }
	})
	if conditional.Arms[0].Condition != (expr.Expr)(leaf) || n.Args[0].(plan.CaseExpr).Arms[0].Condition != (expr.Expr)(leaf) { t.Fatal("negative probes mutated original argument backing") }
}
