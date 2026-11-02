package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestIntarrayProviderLateHelperCollision(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	// A genuine other installed provider supplies the cstring formal; no
	// fabricated executable declaration or SQL native-ABI loader is involved.
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION seg")
	enginetest.RoleExecute(t, e, op, "ALTER FUNCTION seg_in(cstring) RENAME TO _intbig_in")
	before := e.Catalog.State
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "_intbig_in", Signature: "2275"}
	collider := before.MustProcedure(before.NameAddress(key))
	e.Mu.Lock()
	failed, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "intarray"}})
	e.Mu.Unlock()
	extensionUnitDiscardFailed(t, e, failed)
	var diagnostic *sqlstate.Error
	wantContext := "SQL statement \"CREATE FUNCTION _intbig_in(cstring)\nRETURNS intbig_gkey\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 422"
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || diagnostic.Message != `function "_intbig_in" already exists with same argument types` || diagnostic.Context != wantContext || e.Catalog.State != before || e.Catalog.State.MustProcedure(collider.Address()) != collider {
		t.Fatal("late helper collision changed committed declaration authority", diagnostic, err)
	}
	if symbol, err := extension.ProcedureSymbol(e.CatalogRead(), collider); err != nil || symbol != "seg_in" {
		t.Fatal("collider lost original sealed provider", symbol, err)
	}
}

func TestIntarrayProviderPublication(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		scope, err := e.StartStatement(op)
		if err != nil {
			done()
			t.Fatal(err)
		}
		_, err = scope.Execute(&parser.CreateExtension{Name: parser.Identifier{Text: "intarray"}}, 0, nil)
		if err != nil {
			scope.Finish(err)
			done()
			t.Fatal(err)
		}
		before := e.Catalog.State
		if cancelled {
			ctx, cancel := context.WithCancel(op.Context())
			op.Ctx = ctx
			cancel()
		} else {
			e.Mu.Lock()
			address := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			namespace := *e.Catalog.State.MustNamespace(address)
			namespace.ObjectRevision++
			e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
			e.Catalog.State.SetDescriptor(address, &namespace)
			before = e.Catalog.State
			e.Mu.Unlock()
		}
		failure := scope.Finish(nil)
		done()
		if failure == nil || e.Catalog.State != before {
			t.Fatal("cancelled/stale provider publication changed catalog", failure)
		}
		for _, name := range []string{"query_int", "_query_int", "intbig_gkey", "_intbig_gkey"} {
			if before.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name}) != (ids.ObjectAddress{}) {
				t.Fatal("failed publication leaked private type", name)
			}
		}
	}
}
