package engine

import (
	"context"
	"errors"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritedRestoreFixture(t *testing.T) (*Engine, *Operation, map[string]ids.ObjectRef, map[restoredGenerationKey]restoredGenerationRecipe) {
	t.Helper()
	source := New()
	ownershipUnitExec(t, source, "CREATE ROLE captured_owner")
	e := source.restoreStage()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	declarations := []string{
		"CREATE TABLE public.p(a INT,g INT GENERATED ALWAYS AS(NULL) VIRTUAL)",
		"CREATE TABLE public.q(a INT)",
		"CREATE TABLE public.c(CONSTRAINT local_nn NOT NULL a) INHERITS(public.p,public.q)",
		"CREATE TABLE public.unrelated(a INT)",
	}
	refs := make(map[string]ids.ObjectRef)
	origins := make(map[restoredGenerationKey]restoredGenerationRecipe)
	for _, sql := range declarations {
		raw := statement(t, sql).(*parser.CreateTable)
		roleExecute(t, e, op, sql)
		auth, err := op.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.resolveRelationReference(auth, raw.Name, false)
		if err != nil {
			t.Fatal(err)
		}
		refs[raw.Name.Parts[len(raw.Name.Parts)-1].Text] = ref
		e.captureRestoredPlaceholders(ref, raw, origins)
	}
	return e, op, refs, origins
}

func TestRestoreInheritedDefinitionChecks(t *testing.T) {
	e, op, refs, origins := inheritedRestoreFixture(t)
	raw := statement(t, "ALTER TABLE ONLY public.p ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	key := restoredGenerationKey{refs["p"].Address, 2}
	if len(origins) != 2 {
		t.Fatal("actual root/inherited typed-NULL declarations were not captured", origins)
	}
	if err := e.pairRestoredGeneration(op, raw, origins); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*restoredGenerationRecipe){
		"epoch":     func(r *restoredGenerationRecipe) { r.ref.Epoch++ },
		"owner":     func(r *restoredGenerationRecipe) { r.ref = New().catalog.domain.Ref(r.ref.Epoch, r.ref.Address) },
		"attribute": func(r *restoredGenerationRecipe) { r.attribute++ },
		"identity":  func(r *restoredGenerationRecipe) { r.generation++ },
		"kind":      func(r *restoredGenerationRecipe) { r.kind = generationStored },
		"usage":     func(r *restoredGenerationRecipe) { r.usage = testUsage(e, sqltype.Int8) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := maps.Clone(origins)
			record := bad[key]
			mutate(&record)
			bad[key] = record
			before := e.catalog.state
			if err := e.pairRestoredGeneration(op, raw, bad); err == nil || e.catalog.state != before {
				t.Fatal("mismatched generation gained authority", err)
			}
		})
	}
	missing := maps.Clone(origins)
	delete(missing, key)
	if err := e.pairRestoredGeneration(op, raw, missing); err == nil {
		t.Fatal("unpaired generation accepted")
	}
	// Perform the genuine supported repair and capture its actual final identity.
	roleExecute(t, e, op, "ALTER TABLE ONLY public.p ALTER g SET EXPRESSION AS(a+1)")
	repair, err := e.captureRestoredGeneration(op, raw)
	if err != nil {
		t.Fatal(err)
	}
	check := statement(t, "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)").(*parser.AlterTable)
	if err := e.restoreInheritedCheck(op, check, []restoredGenerationRecipe{repair}); err != nil {
		t.Fatal("actual connected other-parent CHECK rejected", err)
	}
	// Start every private rejection with an admitted parser shape. Unsupported
	// public syntax must not fail in statement() before this recognizer runs.
	const admitted = "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)"
	for _, test := range []struct {
		name   string
		change func(*parser.AlterTable)
	}{
		{"unrelated", func(raw *parser.AlterTable) { raw.Name.Parts[len(raw.Name.Parts)-1].Text = "unrelated" }},
		{"only", func(raw *parser.AlterTable) { raw.Only = true }},
		{"if_exists", func(raw *parser.AlterTable) { raw.IfExists = true }},
		{"parameter", func(raw *parser.AlterTable) { raw.Actions[0].Check.Expression = parser.ParameterExpr{Number: 1} }},
		{"multiple_actions", func(raw *parser.AlterTable) {
			other := statement(t, "ALTER TABLE public.q ADD CONSTRAINT other CHECK(a<10)").(*parser.AlterTable)
			raw.Actions = append(raw.Actions, other.Actions[0])
		}},
		{"not_valid", func(raw *parser.AlterTable) { raw.Actions[0].Check.NotValid = true }},
	} {
		candidate := statement(t, admitted).(*parser.AlterTable)
		test.change(candidate)
		before := e.catalog.state
		if err := e.restoreInheritedCheck(op, candidate, []restoredGenerationRecipe{repair}); err == nil || e.catalog.state != before {
			t.Fatal("unpaired CHECK shape admitted", test.name, err)
		}
	}
	if err := e.restoreInheritedCheck(op, check, nil); err == nil {
		t.Fatal("CHECK admitted without actual generation repair")
	}
	for _, bad := range []restoredGenerationRecipe{
		{ref: repair.ref, attribute: repair.attribute, generation: repair.generation + 1, usage: repair.usage, kind: repair.kind},
		{ref: refs["unrelated"], attribute: repair.attribute, generation: repair.generation, usage: repair.usage, kind: repair.kind},
		{ref: repair.ref, attribute: repair.attribute, generation: repair.generation, usage: testUsage(e, sqltype.Int8), kind: repair.kind},
	} {
		if err := e.restoreInheritedCheck(op, check, []restoredGenerationRecipe{bad}); err == nil {
			t.Fatal("foreign definition authorized CHECK", bad)
		}
	}
	// Real inherited origins, not mere graph connectivity, remain mandatory.
	before := e.catalog.state
	child := *before.MustRelation(refs["c"].Address)
	child.inheritance = cloneInheritance(child.inheritance)
	child.inheritance.columns[2] = inheritedColumn{origins: []inheritanceOrigin{{parent: refs["q"].Address, attribute: 1}}}
	changed := cloneCatalog(before)
	changed.setDescriptor(child.address, &child)
	e.catalog.state = changed
	childRaw := statement(t, "ALTER TABLE ONLY public.c ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	if err := e.pairRestoredGeneration(op, childRaw, origins); err == nil || e.catalog.state != changed {
		t.Fatal("forged generation origin accepted", err)
	}
	e.catalog.state = before
	targets := e.importTargets
	e.importTargets = nil
	if err := e.restoreInheritedCheck(op, check, []restoredGenerationRecipe{repair}); err == nil {
		t.Fatal("ordinary CHECK gained importer authority")
	}
	e.importTargets = targets
	canceled, cancel := context.WithCancel(context.Background())
	cancelOp, done, err := testOperation(e, canceled)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	cancel()
	if err := e.restoreInheritedCheck(cancelOp, check, []restoredGenerationRecipe{repair}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled CHECK gained authority", err)
	}
	roleExecute(t, e, op, "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)")
	current := e.CatalogRead().MustRelation(refs["c"].Address)
	if provenance := current.inheritance.checks["positive"]; provenance.local || len(provenance.origins) != 1 || provenance.origins[0].parent != refs["q"].Address {
		t.Fatal("actual ADD did not rebuild exact other-parent support", provenance)
	}
	retained, err := e.closeRestoredRecipesForData(op, statement(t, "INSERT INTO public.c(a) VALUES(1)").(*parser.Insert), []restoredGenerationRecipe{repair})
	if err != nil || len(retained) != 0 {
		t.Fatal("data did not close actual component repair lifetime", retained, err)
	}
}

func TestRestoreInheritedOwnerPair(t *testing.T) {
	e, op, refs, origins := inheritedRestoreFixture(t)
	owner, ok := e.Roles().Lookup("captured_owner")
	if !ok {
		t.Fatal("missing captured role")
	}
	pair, err := e.captureInheritedOwner(op, refs["c"], owner.Ref)
	if err != nil {
		t.Fatal(err)
	}
	raw := statement(t, "ALTER TABLE public.c OWNER TO captured_owner").(*parser.AlterOwner)
	if err := e.restoreInheritedOwner(op, raw, pair); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"ALTER TABLE public.unrelated OWNER TO captured_owner",
		"ALTER TABLE public.c OWNER TO postgres",
		"ALTER TABLE c OWNER TO captured_owner",
		"ALTER TABLE public.c OWNER TO CURRENT_USER",
	} {
		before := e.catalog.state
		if err := e.restoreInheritedOwner(op, statement(t, sql).(*parser.AlterOwner), pair); err == nil || e.catalog.state != before {
			t.Fatal("foreign owner target accepted", sql, err)
		}
	}
	for name, mutate := range map[string]func(*restoredInheritedOwner){
		"missing": func(r *restoredInheritedOwner) { *r = restoredInheritedOwner{} },
		"epoch":   func(r *restoredInheritedOwner) { r.ref.Epoch++ },
		"creator": func(r *restoredInheritedOwner) { r.creator = owner.Ref },
		"target":  func(r *restoredInheritedOwner) { r.ref = refs["unrelated"] },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := pair
			mutate(&candidate)
			before := e.catalog.state
			if err := e.restoreInheritedOwner(op, raw, candidate); err == nil || e.catalog.state != before {
				t.Fatal("mismatched owner identity admitted", err)
			}
		})
	}
	targets := e.importTargets
	e.importTargets = nil
	if err := e.restoreInheritedOwner(op, raw, pair); err == nil {
		t.Fatal("ordinary OWNER gained importer authority")
	}
	e.importTargets = targets
	before := e.CatalogRead().MustRelation(refs["c"].Address)
	nn := restoredNotNull{ref: refs["c"], attribute: 1, constraint: before.notNullConstraints[0].OID}
	roleExecute(t, e, op, "ALTER TABLE public.c OWNER TO captured_owner")
	after := e.CatalogRead().MustRelation(refs["c"].Address)
	if after.owner != owner.Ref || before.owner == owner.Ref || after.notNullConstraints[0].OID != nn.constraint {
		t.Fatal("actual owner transfer changed source capture or physical NN identity")
	}
	if err := e.restoreInheritedOwner(op, raw, pair); err == nil {
		t.Fatal("consumed owner pairing reused")
	}
	generation := statement(t, "ALTER TABLE ONLY public.c ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	if err := e.pairRestoredGeneration(op, generation, origins); err != nil {
		t.Fatal("legal owner metadata copy invalidated actual generation identity", err)
	}
	if err := e.restoreNoInheritNotNull(op, restoreNNRaw("public.c", "a", "local_nn"), nn); err != nil {
		t.Fatal("legal owner metadata copy invalidated actual NN identity", err)
	}
	if current := e.CatalogRead().MustRelation(refs["c"].Address); current.owner != owner.Ref || !current.notNullConstraints[0].NoInherit || current.notNullConstraints[0].OID != nn.constraint {
		t.Fatal("paired NN repair changed final captured owner/identity")
	}
}
