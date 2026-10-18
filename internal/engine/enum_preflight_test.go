package engine

import (
	"context"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestEnumInvalidLabelPreflightNoReservation(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('old')"))
	mood := domainUnitType(t, e.CatalogRead(), "mood")
	resource := lockResource{ObjectRef: e.domain.Ref(e.state.epoch, mood.Address())}
	holder := sessionStart(t, e, nil)
	ref := transactionBegin(t, holder)
	defer transactionEnd(t, holder, ref, false)
	if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, resource, typeExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(op.ctx, time.Second)
	defer cancel()
	op.ctx = ctx
	for _, rename := range []bool{false, true} {
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		catalog, member := e.state, e.nextEnumMember
		ast := &parser.AlterEnum{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "mood"}}}, Label: parser.EnumLabel{Text: strings.Repeat("x", 64)}, Old: parser.EnumLabel{Text: "old"}, Rename: rename}
		_, failure := e.executeEnumDDL(scope, ast, 0)
		d, ok := failure.(*sqlstate.Error)
		e.locks.mu.Lock()
		entry := e.locks.entries[resource]
		_, owned := entry.holders[scope.owner.ticket]
		queued := len(entry.pending)
		e.locks.mu.Unlock()
		unchanged := e.state == catalog && e.nextEnumMember == member && scope.catalog == nil && scope.owner.catalog == nil
		finish := scope.Finish(failure)
		if !ok || d.Code != "42602" || d.Detail != "Labels must be 63 bytes or less." || owned || queued != 0 || !unchanged || finish != failure {
			t.Fatal("invalid immutable label entered queue/reserved member/staged catalog", failure, owned, queued, unchanged, finish)
		}
	}
}
