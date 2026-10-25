package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestEnumInvalidLabelPreflightNoReservation(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('old')"))
	mood := domainUnitType(t, e.CatalogRead(), "mood")
	resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), mood.Address())}
	holder := sessionStart(t, e, nil)
	ref := transactionBegin(t, holder)
	defer transactionEnd(t, holder, ref, false)
	if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, resource, lock.TypeExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(op.Ctx, time.Second)
	defer cancel()
	op.Ctx = ctx
	for _, rename := range []bool{false, true} {
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		// A probe reservation on each side shows the failure reserved none.
		member, err := e.Catalog.AllocateEnumMemberLocked()
		if err != nil {
			t.Fatal(err)
		}
		catalog := e.Catalog.State
		ast := &parser.AlterEnum{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "mood"}}}, Label: parser.EnumLabel{Text: strings.Repeat("x", 64)}, Old: parser.EnumLabel{Text: "old"}, Rename: rename}
		_, failure := executeEnumDDL(e, scope, ast, 0)
		d, ok := failure.(*sqlstate.Error)
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[resource]
		_, owned := entry.Holders[scope.Owner.Ticket]
		queued := len(entry.Pending)
		next, err := e.Catalog.AllocateEnumMemberLocked()
		unchanged := e.Catalog.State == catalog && err == nil && next == member+2 && scope.Catalog == nil && scope.Owner.Catalog == nil
		finish := scope.Finish(failure)
		if !ok || d.Code != "42602" || d.Detail != "Labels must be 63 bytes or less." || owned || queued != 0 || !unchanged || finish != failure {
			t.Fatal("invalid immutable label entered queue/reserved member/staged catalog", failure, owned, queued, unchanged, finish)
		}
	}
}
