package engine

import (
	"testing"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/index"
)

func TestPLExceptionRoutedEffectAdmission(t *testing.T) {
	e := New()
	sess := sessionStart(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE routed(v integer) PARTITION BY LIST(v)",
		"CREATE TABLE routed_zero PARTITION OF routed FOR VALUES IN (0)",
		"CREATE TABLE routed_one PARTITION OF routed FOR VALUES IN (1)",
	} {
		sessionSQL(t, sess, query)
	}
	scope := foreignActionUnitScope(t, e, sess, "INSERT INTO routed VALUES(0),(1),(0),(1) RETURNING v")
	state := scope.PartitionInsert
	if state == nil || len(state.Order) != 2 || scope.foreignActionsSlot != nil || scope.effectAdmission.catalog != nil {
		t.Fatal("alternating accepted rows did not retain allocation-free per-destination admission")
	}
	view := e.CatalogRead()
	var original [2]*index.UniqueWriteBinding
	for i, group := range state.Order {
		original[i] = group.Unique
		if group.Unique == nil || group.Unique.Ref != group.Ref || group.Unique.Catalog != view.State() || group.Unique.Parent != view.MustRelation(group.Ref.Address) || group.Unique.ConstraintEffectsRelevant {
			t.Fatal("physical group lost its exact irrelevant admission", group)
		}
		if len(scope.Stage.Rows[group.Ref.Address]) != 2 {
			t.Fatal("alternating accepted destinations lost rows")
		}
	}
	if original[0] == original[1] {
		t.Fatal("physical destinations share a mutable admission binding")
	}
	// Repeat the real group-selection/binding seam in alternating order. Each
	// current physical binding must be reused; relevance must not fall back to
	// the single-target discovery memo, whose path scans catalog descriptors.
	for range 4 {
		for i, group := range state.Order {
			scope.UniqueBinding = group.Unique
			binding, err := dml.BindUniqueWrite(scope.WriteHost(), group.Ref)
			if err != nil || binding != original[i] || scope.foreignEffectRelevant(group.Ref, view) || scope.effectAdmission.catalog != nil {
				t.Fatal("alternation discarded retained physical admission", err)
			}
		}
	}
	// Real unrelated DDL publishes a different immutable catalog authority
	// while the admitted routed statement remains live. Even unchanged parent
	// descriptors cannot authorize reuse of the old catalog's relevance bit.
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE relevance_catalog_tick(v integer)")
	current := e.CatalogRead()
	if current.State() == view.State() {
		t.Fatal("catalog invalidation witness did not publish a new root")
	}
	for i, group := range state.Order {
		scope.UniqueBinding = original[i]
		scope.effectAdmission = foreignEffectAdmission{}
		if scope.foreignEffectRelevant(group.Ref, current) || scope.effectAdmission.catalog != current.State() || scope.effectAdmission.ref != group.Ref {
			t.Fatal("stale catalog binding bypassed current-authority classification")
		}
		binding, err := dml.BindUniqueWrite(scope.WriteHost(), group.Ref)
		if err != nil || binding == original[i] || binding.Ref != group.Ref || binding.Catalog != current.State() || binding.ConstraintEffectsRelevant {
			t.Fatal("physical admission did not rebuild under new catalog authority", err)
		}
		group.Unique = binding
		scope.effectAdmission = foreignEffectAdmission{}
		if scope.foreignEffectRelevant(group.Ref, current) || scope.effectAdmission.catalog != nil || original[i].Catalog != view.State() {
			t.Fatal("rebuilt admission was not retained or mutated the old binding")
		}
	}
	// A current binding of the other destination is not this destination's
	// authority, even though both happen to have irrelevant constraints.
	first, second := state.Order[0], state.Order[1]
	scope.UniqueBinding = second.Unique
	scope.effectAdmission = foreignEffectAdmission{}
	if scope.foreignEffectRelevant(first.Ref, current) || scope.effectAdmission.ref != first.Ref || scope.effectAdmission.catalog != current.State() {
		t.Fatal("relevance reused a different physical reference")
	}
	if scope.foreignActionsSlot != nil {
		t.Fatal("unrelated routed writes allocated transition bookkeeping")
	}
}

func TestPLExceptionEffectBindingRelevance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setup    []string
		relevant bool
	}{
		{"unrelated", []string{
			"CREATE TABLE target(id integer)",
			"CREATE TABLE parent(id integer PRIMARY KEY)",
			"CREATE TABLE child(id integer REFERENCES parent(id))",
		}, false},
		{"outgoing", []string{
			"CREATE TABLE parent(id integer PRIMARY KEY)",
			"CREATE TABLE target(id integer REFERENCES parent(id))",
		}, true},
		{"incoming_not_valid_standalone_index", []string{
			"CREATE TABLE target(id integer)",
			"CREATE UNIQUE INDEX target_key ON target(id)",
			"CREATE TABLE child(id integer)",
			"ALTER TABLE child ADD CONSTRAINT child_fk FOREIGN KEY(id) REFERENCES target(id) NOT VALID",
		}, true},
		{"exclusion_only", []string{
			"CREATE TABLE target(id integer,CONSTRAINT target_ex EXCLUDE USING btree(id WITH =))",
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			sess := sessionStart(t, e, nil)
			for _, query := range tc.setup {
				sessionSQL(t, sess, query)
			}
			view := e.CatalogRead()
			ref := catalogRef(t, e, view, "target")
			binding, err := index.BuildUniqueWriteBinding(ref, view.MustRelation(ref.Address), view)
			if err != nil || binding.ConstraintEffectsRelevant != tc.relevant || binding.Ref != ref || binding.Catalog != view.State() {
				t.Fatal("binding did not capture exact current FK/exclusion relevance", binding, err)
			}
		})
	}
}
