package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func rewriteOwner(t *testing.T) (*core.Engine, catalog.Read, *catalog.Relation, *sqltype.Descriptor) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)", "CREATE RULE a AS ON UPDATE TO src WHERE NEW.v>OLD.v DO INSTEAD NOTHING", "CREATE RULE b AS ON INSERT TO src DO ALSO (INSERT INTO dst VALUES(NEW.v+1); SELECT NEW.v)", "CREATE RULE c AS ON DELETE TO src WHERE OLD.v>0 DO NOTHING", "CREATE VIEW v AS SELECT v FROM dst"} {
		enginetest.SessionSQL(t, s, q)
	}
	read := e.CatalogRead()
	owner := read.MustRelation(enginetest.CatalogRef(t, e, read, "src").Address)
	typ, _ := read.LookupType(sqltype.PgNodeTree)
	return e, read, owner, typ
}

func TestPGRewriteTrees(t *testing.T) {
	_, read, owner, typ := rewriteOwner(t)
	list := owner.Rules.(stored.RuleList)
	qual, action, err := stored.RewriteTrees(t.Context(), read, owner, typ, &list[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := qual.Tree()
	if reflect.ValueOf(q).FieldByName("rule").Pointer() != reflect.ValueOf(&list[0]).Pointer() {
		t.Fatal("qualification lost installed element")
	}
	a, _ := action.Tree()
	if reflect.ValueOf(a).FieldByName("rule").Pointer() != reflect.ValueOf(&list[0]).Pointer() {
		t.Fatal("action lost installed element")
	}
	if list[0].ConditionBinding() == nil || list[0].ConditionPresentation() == nil {
		t.Fatal("fixture lost analyzed binding")
	}
	text, err := qual.Type().Output(t.Context(), qual)
	if err != nil || !strings.Contains(text, "scope 4294967295 frame 1") || !strings.Contains(text, "scope 4294967295 frame 2") {
		t.Fatal(text, err)
	}
	want := `{RewriteActions {Nothing event "UPDATE" pseudo OLD 1 NEW 2}}`
	if got, err := action.Type().Output(t.Context(), action); err != nil || got != want {
		t.Fatal(got, err)
	}
	q2, a2, err := stored.RewriteTrees(t.Context(), read, owner, typ, &list[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := q2.Type().Output(t.Context(), q2); err != nil || got != "<>" || q2.IsNull() {
		t.Fatal(got, err)
	}
	ordered, err := a2.Type().Output(t.Context(), a2)
	if err != nil || strings.Index(ordered, "parser.Insert") >= strings.Index(ordered, "parser.Select") || !strings.Contains(ordered, `Integer 1`) || !strings.Contains(ordered, "frame 2") {
		t.Fatal(ordered, err)
	}
	var d *sqlstate.Error
	if err := q.(stored.RewriteQualCarrier).Preflight(t.Context(), 0); !errors.As(err, &d) || d.Code != "22023" || d.Message != "expression contains variables" {
		t.Fatal(err)
	}
	d = nil
	if err := q.(stored.RewriteQualCarrier).Preflight(t.Context(), 4294967295); !errors.As(err, &d) || d.Code != "22023" || d.Message != "expression contains variables of more than one relation" {
		t.Fatal(err)
	}
	oldQual, _, err := stored.RewriteTrees(t.Context(), read, owner, typ, &list[2], nil)
	if err != nil {
		t.Fatal(err)
	}
	oldTree, _ := oldQual.Tree()
	if err := oldTree.(stored.RewriteQualCarrier).Preflight(t.Context(), 4294967295); err != nil {
		t.Fatal("OLD-only preflight incorrectly requires lookup", err)
	}
}

type rewriteWalkContext struct {
	context.Context
	cancel context.CancelFunc
	calls  atomic.Int64
	after  int64
}

func (c *rewriteWalkContext) Err() error {
	if c.calls.Add(1) == c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPGRewriteCapture(t *testing.T) {
	e, read, owner, typ := rewriteOwner(t)
	list := owner.Rules.(stored.RuleList)
	source := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2618})
	if len(source.Columns) != 8 || len(source.Attributes) != 8 {
		t.Fatal("rewrite descriptor width", source.Columns)
	}
	typeIDs := []sqltype.ID{26, 19, 26, 18, 18, 16, 194, 194}
	for i, column := range source.Columns {
		captured, _ := read.LookupType(typeIDs[i])
		coll := uint32(0)
		if i == 1 || i >= 6 {
			coll = 950
		}
		if column.Usage.Type != captured || column.Usage.Typmod != -1 || column.Usage.Collation.ID != coll || column.Nullable || !column.NullabilityKnown || column.OriginTable != 2618 || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			t.Fatal("rewrite captured descriptor", i, column)
		}
	}
	_, value, err := stored.RewriteTrees(t.Context(), read, owner, typ, &list[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	carrier, _ := value.Tree()
	ctx, cancel := context.WithCancel(t.Context())
	walk := &rewriteWalkContext{Context: ctx, cancel: cancel, after: 8}
	defer cancel()
	if text, err := value.Type().Output(walk, value); !errors.Is(err, context.Canceled) || text != "" {
		t.Fatal("canceled walk published/cached partial output", text, err)
	}
	original, err := carrier.StructuralOutput(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if text, err := value.Type().Output(t.Context(), value); err != nil || text != original {
		t.Fatal("canceled output poisoned cache", text, err)
	}
	foreign := engine.New().CatalogRead()
	foreignType, _ := foreign.LookupType(sqltype.PgNodeTree)
	copyRule := list[1]
	copyOwner := *owner
	for _, tc := range []struct {
		read  catalog.Read
		owner *catalog.Relation
		typ   *sqltype.Descriptor
		rule  *stored.Rule
	}{{read, owner, typ, &copyRule}, {read, &copyOwner, typ, &list[1]}, {foreign, owner, typ, &list[1]}, {read, owner, foreignType, &list[1]}, {read, owner, nil, &list[1]}, {catalog.Read{}, owner, typ, &list[1]}} {
		if _, _, err := stored.RewriteTrees(t.Context(), tc.read, tc.owner, tc.typ, tc.rule, nil); err == nil {
			t.Fatal("foreign/copy/detached definition admitted")
		}
	}
	viewOwner := read.MustRelation(enginetest.CatalogRef(t, e, read, "v").Address)
	definition := stored.ViewOf(viewOwner)
	viewQual, viewAction, err := stored.RewriteTrees(t.Context(), read, viewOwner, typ, nil, definition)
	if err != nil {
		t.Fatal(err)
	}
	viewCarrier, _ := viewAction.Tree()
	if reflect.ValueOf(viewCarrier).FieldByName("view").Pointer() != reflect.ValueOf(definition).Pointer() {
		t.Fatal("view definition detached")
	}
	copyView := *definition
	if _, _, err := stored.RewriteTrees(t.Context(), read, viewOwner, typ, nil, &copyView); err == nil {
		t.Fatal("copied view admitted")
	}
	if absent, err := viewQual.Type().Output(t.Context(), viewQual); err != nil || absent != "<>" || viewQual.IsNull() {
		t.Fatal(absent, err)
	}
	// Cancellation after an actual visitor row never visits a later definition.
	visitCtx, stop := context.WithCancel(t.Context())
	defer stop()
	count := 0
	err = stored.VisitRewriteCatalog(visitCtx, read, owner, typ, func(stored.RewriteCatalogDefinition) error { count++; stop(); return nil })
	if count != 1 || !errors.Is(err, context.Canceled) {
		t.Fatal(count, err)
	}
	enginetest.Run(t, e, "CREATE OR REPLACE RULE b AS ON INSERT TO src DO NOTHING")
	current := e.CatalogRead()
	if _, _, err := stored.RewriteTrees(t.Context(), current, owner, typ, &list[1], nil); err == nil {
		t.Fatal("stale relation accepted")
	}
	enginetest.Run(t, e, "DROP TABLE dst CASCADE")
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	// Concurrent outputs only see the captured predecessor and successful cache.
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := value.Type().Output(t.Context(), value)
			if err != nil || got != original {
				t.Error(got, err)
			}
		}()
	}
	workers.Wait()
	if got, err := carrier.StructuralOutput(t.Context()); err != nil || got != original {
		t.Fatal("retained definition re-resolved", got, err)
	}
	if got, err := viewCarrier.StructuralOutput(t.Context()); err != nil || !strings.Contains(got, "parser.Select") {
		t.Fatal("retained view after reset", got, err)
	}
}
