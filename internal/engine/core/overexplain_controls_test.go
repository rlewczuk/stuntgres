package core_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func overexplainInstalledOwner(t *testing.T, read catalog.Read) (*catalog.Extension, *catalog.Procedure, *catalog.TSDictionary) {
	t.Helper()
	ext := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(read, ext); err != nil {
		t.Fatal(err)
	}
	p := read.MustProcedure(ext.Installation.Members[1])
	d := read.MustTSDictionary(ext.Installation.Members[5])
	if p.Implementation != extension.StockUnaccent || p.Origin.Installation != ext.Installation || d.Origin.Installation != ext.Installation || d.Runtime != functions.StockUnaccent || d.Template != ext.Installation.Members[4] {
		t.Fatal("installed provider/definition authority not genuine")
	}
	if !ext.Installation.Valid() || ext.Installation.Cohort != ext.Address() || ext.Installation.Implementation != extension.StockUnaccent {
		t.Fatal("installation owner identity invalid")
	}
	return ext, p, d
}

func TestOverexplainControlLifetime(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	// SQL establishes the actual registered provider and stored definition. R6
	// owns public row/NULL/metadata witnesses; this unit inspects real backing.
	for _, q := range []string{"CREATE EXTENSION unaccent", "CREATE TABLE overexplain_control_owner(id integer,v text)", "CREATE VIEW overexplain_control_view AS SELECT id,unaccent(v) AS v FROM overexplain_control_owner"} {
		enginetest.SessionSQL(t, s, q)
	}
	before := e.CatalogRead()
	ext, procedure, dictionary := overexplainInstalledOwner(t, before)
	installation, origin, runtime := ext.Installation, procedure.Origin, dictionary.Runtime
	members := slices.Clone(ext.Members)
	memberBacking := &ext.Members[0]
	viewRef := enginetest.CatalogRef(t, e, before, "overexplain_control_view")
	view := before.MustRelation(viewRef.Address)
	definition := stored.ViewOf(view)
	storedRoot := definition.Query().Root.(*plan.ProjectNode)
	storedExpressions := slices.Clone(storedRoot.Expressions)
	storedBacking := &storedRoot.Expressions[0]
	storedRevision := definition.DefinitionRevision()
	storedDependencies := slices.Clone(definition.Dependencies())
	call, ok := storedRoot.Expressions[1].(plan.InstalledTextExpr)
	if !ok || call.Procedure != procedure || call.Ref.Address != procedure.Address() {
		t.Fatal("stored consumer did not capture the actual installed declaration owner")
	}
	if err := s.Settings.LoadOverexplain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != before.State() || e.CatalogRead().Get(ext.Address()) != ext || e.CatalogRead().Get(procedure.Address()) != procedure || e.CatalogRead().Get(dictionary.Address()) != dictionary || e.CatalogRead().Get(viewRef.Address) != view || peer.Settings.OverexplainLoaded() {
		t.Fatal("activation replaced installed objects/definitions or leaked physical authority")
	}
	const query = "EXPLAIN (DEBUG, VERBOSE, FORMAT JSON) SELECT id,v FROM overexplain_control_view ORDER BY id"
	ast, description := enginetest.PreparedDescription(t, e, s, query)
	defer e.RetirePreparedDescription(&description)
	owner, analysis, generation := description.Folds, description.Analysis, description.PlanGeneration
	r, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Rows.Close()
	resource := r.Rows.(*exec.FinalRows)
	backing := resource.Anonymous[0]
	cells := make([]sqltype.Value, 1)
	if ok, err := resource.Next(context.Background(), cells); err != nil || !ok {
		t.Fatal("missing accepted debug output", err)
	}
	retained := cells[0]
	text, err := retained.Type().Output(context.Background(), retained)
	if err != nil {
		t.Fatal(err)
	}
	resource.Close()
	if !resource.Closed || resource.Source != nil || owner.Live() || owner.Current().Generation != 0 || description.FoldPlanning != nil {
		t.Fatal("ordinary debug retained live source/accepted executable graph")
	}
	explainRejectFoldPublication(t, e, s, ast, &description)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "pg_overexplain") {
		t.Fatal("dump serialized session activation")
	}
	if e.CatalogRead().State() != before.State() || ext.Installation != installation || procedure.Origin != origin || dictionary.Runtime != runtime || &ext.Members[0] != memberBacking || !reflect.DeepEqual(ext.Members, members) {
		t.Fatal("Dump mutated accepted provider backing")
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	after := e.CatalogRead()
	current, currentProcedure, currentDictionary := overexplainInstalledOwner(t, after)
	currentView := after.MustRelation(enginetest.CatalogRef(t, e, after, "overexplain_control_view").Address)
	currentDefinition := stored.ViewOf(currentView)
	if currentDefinition == definition || currentDefinition.Query().Root == storedRoot {
		t.Fatal("replacement adopted original stored graph ownership")
	}
	if err := currentDefinition.Validate(after, currentView); err != nil {
		t.Fatal("imported stored definition lost remapped provider authority", err)
	}
	if after.Epoch() <= before.Epoch() || current == ext || current.Installation == installation || currentProcedure == procedure || currentDictionary == dictionary || currentProcedure.Implementation != procedure.Implementation || currentDictionary.Runtime != runtime {
		t.Fatal("actual replacement did not rebuild owned catalog graph while preserving registered executable provider")
	}
	if !s.Settings.OverexplainLoaded() || peer.Settings.OverexplainLoaded() {
		t.Fatal("replacement transported/cleared physical activation")
	}
	if err := enginetest.ValidateObjectRef(e, after, viewRef); err == nil {
		t.Fatal("old bound ref escaped replacement fence")
	}
	next, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal("ordinary held definition failed to rebind", err)
	}
	nextResource := next.Rows.(*exec.FinalRows)
	if &nextResource.Anonymous[0][0] == &backing[0] {
		t.Fatal("rebind reused prior output row backing")
	}
	nextResource.Close()
	if description.Folds != owner || owner.Live() || owner.Current().Generation != 0 || description.FoldPlanning != nil {
		t.Fatal("control replacement granted child fold-publication rights")
	}
	// The original accepted description may reanalyze; its predecessor remains
	// immutable and never becomes the imported definition's owner.
	if analysis == nil || generation != 0 || procedure.Origin != origin || ext.Installation != installation || dictionary.Runtime != runtime || &ext.Members[0] != memberBacking || !reflect.DeepEqual(ext.Members, members) {
		t.Fatal("replacement mutated original accepted owner/generation/backing")
	}
	if definition.Query().Root != storedRoot || definition.DefinitionRevision() != storedRevision || &storedRoot.Expressions[0] != storedBacking || !reflect.DeepEqual(storedRoot.Expressions, storedExpressions) || !reflect.DeepEqual(definition.Dependencies(), storedDependencies) {
		t.Fatal("controls mutated original stored definition generation/expression/dependency backing")
	}
	target := engine.New()
	destination := enginetest.StartSession(t, target, nil)
	domain := target.Catalog.Domain
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	imported, importedProcedure, _ := overexplainInstalledOwner(t, target.CatalogRead())
	if target.Catalog.Domain != domain || target.Catalog.Domain == e.Catalog.Domain || imported == current || importedProcedure == currentProcedure || imported.Installation == current.Installation || importedProcedure.Implementation != procedure.Implementation || destination.Settings.OverexplainLoaded() {
		t.Fatal("independent destination adopted source descriptors/physical session authority")
	}
	if err := destination.Settings.LoadOverexplain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reset(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if !destination.Settings.OverexplainLoaded() {
		t.Fatal("Reset unloaded destination's own session")
	}
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	overexplainInstalledOwner(t, target.CatalogRead())
	if !destination.Settings.OverexplainLoaded() {
		t.Fatal("Restore overwrote destination's own activation")
	}
	e.RetirePreparedDescription(&description)
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Settings.OverexplainLoaded() || s.State != session.Ended || len(s.Operations) != 0 {
		t.Fatal("physical end retained active resources")
	}
	for _, resource := range s.Resources {
		if resource != nil {
			t.Fatal("terminal session retained resource attachment")
		}
	}
	frozen, err := retained.Type().Output(context.Background(), retained)
	if err != nil || frozen != text || backing[0] != retained || before.Get(procedure.Address()) != procedure || before.Get(view.Address()) != view {
		t.Fatal("accepted output or original stored provider changed after native controls/physical end", err)
	}
}
