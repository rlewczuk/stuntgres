package engine

import (
	"bytes"
	"context"
	"stuntgres/internal/sqlstate"
	"testing"
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
				source := New()
				s := sessionStart(t, source, nil)
				sessionSQL(t, s, "CREATE TABLE t(a INT CONSTRAINT c CHECK("+expr+" IS NULL AND a=1))")
				sessionSQL(t, s, "INSERT INTO t VALUES(1)")
				before := source.CatalogRead()
				ref := catalogRef(t, source, before, "t")
				descriptor := before.state.descriptors[ref.Address]
				var dump bytes.Buffer
				if err := source.Dump(ctx, &dump); err != nil {
					t.Fatal(err)
				}
				target := New()
				peer := sessionStart(t, target, nil)
				if self {
					target, peer = source, s
				}
				if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
					t.Fatal(err)
				}
				if !self && source.CatalogRead().state.descriptors[ref.Address] != descriptor {
					t.Fatal("foreign replacement mutated source")
				}
				view := target.CatalogRead()
				imported := catalogRef(t, target, view, "t")
				if err := validateChecks(view, view.state.descriptors[imported.Address].(*relationDescriptor), map[string]bool{}); err != nil {
					t.Fatal(err)
				}
				transactionRows(t, peer, "SELECT a FROM t", 1)
				sessionSQL(t, peer, "UPDATE t SET a=1")
				op, err := peer.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				_, err = target.Execute(op, statement(t, "INSERT INTO t VALUES(2)"), 0, nil)
				op.Done()
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "c"`, Detail: "Failing row contains (2).", Schema: "public", Table: "t", Constraint: "c"})
				var again bytes.Buffer
				if err := target.Dump(ctx, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
					t.Fatal("replacement changed persisted inquiry", err)
				}
			})
		}
	}
}
