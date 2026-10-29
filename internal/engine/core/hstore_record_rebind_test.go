package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// A genuine replacement changes both catalog lifetime and installed descriptor
// ownership. Only the successful reanalysis candidate may replace its capture.
func TestHstoreRecordCapturedLiteralLifetime(t *testing.T) {
	t.Run("same_oid", func(t *testing.T) { hstoreRecordCapturedLiteralLifetime(t, false) })
	t.Run("displaced_oid", func(t *testing.T) { hstoreRecordCapturedLiteralLifetime(t, true) })
}

func hstoreRecordCapturedLiteralLifetime(t *testing.T, displaced bool) {
	source, target := engine.New(), engine.New()
	ss, ts := enginetest.StartSession(t, source, nil), enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, ss, "CREATE EXTENSION hstore")
	if displaced {
		enginetest.SessionSQL(t, ts, "CREATE TYPE hs_capture_displaced AS (v text)")
	}
	enginetest.SessionSQL(t, ts, "CREATE EXTENSION hstore")
	lookup := func(e *core.Engine) *sqltype.Descriptor {
		v := e.CatalogRead()
		ext := v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
		return v.MustType(ext.Members[0]).SQLType
	}
	old, current := lookup(source), lookup(target)
	if displaced && old.ID() == current.ID() {
		t.Fatal("destination did not displace the installed nominal OID")
	}
	value, err := old.Input(context.Background(), "a=>1", -1)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := source.CatalogRead().LookupType(sqltype.Text)
	initial := analyze.TemporalConstant{Offset: 10, TypeID: old.ID(), Typmod: -1, Value: value}
	description := core.Description{CatalogOwner: source.CatalogRead().Owner(), CatalogEpoch: source.CatalogRead().Epoch(), Results: []catalog.Column{{Name: "v", Usage: text.Usage()}}, Temporals: []analyze.TemporalConstant{initial}, RefreshTemporals: true}
	before := append([]analyze.TemporalConstant(nil), description.Temporals...)
	pos := parser.Position{Offset: 10, Character: 11}
	binder := enginetest.ExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, &description)
	fresh, err := binder.InputConstant("a=>2", current.Usage(), pos)
	if err != nil || fresh.Type() != current || fresh == value || len(binder.Prepared.Temporals) != 1 || binder.Prepared.Temporals[0].Value != fresh || binder.Prepared.Temporals[0].TypeID != current.ID() {
		t.Fatal("replacement lost current private capture or duplicated stale capture", err)
	}
	if !reflect.DeepEqual(description.Temporals, before) {
		t.Fatal("candidate changed published source capture")
	}
	// A later candidate consumes the newly accepted descriptor without reparsing,
	// even though the description's original public signature lifetime stays fixed.
	accepted := description
	accepted.Temporals = append([]analyze.TemporalConstant(nil), binder.Prepared.Temporals...)
	again := enginetest.ExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, &accepted)
	reused, err := again.InputConstant("invalid input must not be reparsed", current.Usage(), pos)
	if err != nil || reused != fresh || len(again.Prepared.Temporals) != 1 {
		t.Fatal("current capture reparsed or changed backing", err)
	}
	failed := enginetest.ExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, &description)
	if _, err := failed.InputConstant("invalid hstore", current.Usage(), pos); err == nil {
		t.Fatal("malformed replacement candidate accepted")
	}
	if !reflect.DeepEqual(description.Temporals, before) {
		t.Fatal("failed candidate changed source capture")
	}
	structural := description
	structural.RefreshTemporals = false
	unchanged := enginetest.ExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, &structural)
	if _, err := unchanged.InputConstant("a=>2", current.Usage(), pos); err == nil {
		t.Fatal("structural replay replaced foreign-generation capture")
	}
	fenced := description
	fenced.Results = []catalog.Column{{Name: "v", Usage: old.Usage()}}
	reject := enginetest.ExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, &fenced)
	_, err = reject.InputConstant("a=>1", current.Usage(), pos)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cached plan must not change result type" {
		t.Fatal("datum retag bypassed held result fence", err)
	}
	raw := enginetest.Statement(t, "SELECT 'a=>1'::hstore").(*parser.Select).Targets[0]
	literalPosition := raw.(parser.CastExpr).Child.(parser.LiteralExpr).Value.Pos
	boundFence := fenced
	boundFence.Temporals = append([]analyze.TemporalConstant(nil), fenced.Temporals...)
	boundFence.Temporals[0].Offset = literalPosition.Offset
	bound := enginetest.ExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, &boundFence)
	_, err = bound.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}) {
		t.Fatal("literal positioning changed the global lifetime diagnostic", err)
	}
	ordinary := enginetest.ExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, nil)
	badInteger := enginetest.Statement(t, "SELECT 'oops'::integer").(*parser.Select).Targets[0]
	_, err = ordinary.Bind(badInteger, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != `invalid input syntax for type integer: "oops"` || diagnostic.Position != "8" {
		t.Fatal("ordinary codec input lost its literal position", err)
	}
	codecUnsupported := &sqlstate.Error{Code: "0A000", Message: "codec-owned unsupported input"}
	positioned := analyze.InputErrorAt(sqlstate.SoftInputError(codecUnsupported), parser.Position{Character: 8})
	if !errors.As(positioned, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Position != "8" {
		t.Fatal("lifetime provenance changed codec-owned 0A000 positioning", positioned)
	}
}
