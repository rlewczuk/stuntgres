package tablecmds_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
)

func TestCheckReviewScalarInquiryCatalogReplacement(t *testing.T) {
	for _, expr := range []string{"obj_description(0,'pg_class')", "obj_description(0)", "col_description(0,1)", "shobj_description(0,'pg_authid')"} {
		for _, self := range []bool{false, true} {
			name := "foreign/" + expr
			if self {
				name = "self/" + expr
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				source := engine.New()
				s := enginetest.StartSession(t, source, nil)
				enginetest.SessionSQL(t, s, "CREATE TABLE t(a INT CONSTRAINT c CHECK("+expr+" IS NULL AND a=1))")
				enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1)")
				before := source.CatalogRead()
				ref := enginetest.CatalogRef(t, source, before, "t")
				descriptor := before.Get(ref.Address)
				var dump bytes.Buffer
				if err := engine.Dump(ctx, source, &dump); err != nil {
					t.Fatal(err)
				}
				target := engine.New()
				peer := enginetest.StartSession(t, target, nil)
				if self {
					target, peer = source, s
				}
				if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
					t.Fatal(err)
				}
				if !self && source.CatalogRead().Get(ref.Address) != descriptor {
					t.Fatal("foreign replacement mutated source")
				}
				view := target.CatalogRead()
				imported := enginetest.CatalogRef(t, target, view, "t")
				if err := table.ValidateChecks(view, view.MustRelation(imported.Address), map[string]bool{}); err != nil {
					t.Fatal(err)
				}
				enginetest.TransactionRows(t, peer, "SELECT a FROM t", 1)
				enginetest.SessionSQL(t, peer, "UPDATE t SET a=1")
				op, err := peer.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				_, err = target.Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(2)"), 0, nil)
				op.Done()
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "c"`, Detail: "Failing row contains (2).", Schema: "public", Table: "t", Constraint: "c"})
				var again bytes.Buffer
				if err := engine.Dump(ctx, target, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
					t.Fatal("replacement changed persisted inquiry", err)
				}
			})
		}
	}
}
