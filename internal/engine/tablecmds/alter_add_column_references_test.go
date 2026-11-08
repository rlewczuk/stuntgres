package tablecmds_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func alterForeignKeyDeclarationNames(declarations []table.AlterForeignKeyDeclaration) []string {
	var names []string
	for _, declaration := range declarations {
		names = append(names, declaration.Name)
	}
	return names
}

func TestAlterAddColumnReferencesDeclarations(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT,pid INT)")
	old := alterConstraintDescriptor(t, e, "t")
	original := *old
	original.Columns, original.Attributes, original.Index = slices.Clone(old.Columns), slices.Clone(old.Attributes), maps.Clone(old.Index)
	view, before, store := e.CatalogRead(), e.Catalog.State, e.Storage.Relations[old.Address()]
	plain := []parser.AlterTableAction{{Action: parser.AddColumn, Definition: &parser.Column{Name: "x"}}}
	var empty []table.AlterForeignKeyDeclaration
	if allocations := testing.AllocsPerRun(1, func() { empty = table.AlterForeignKeyDeclarations(view, old, plain) }); allocations != 0 || empty != nil {
		t.Fatal("ordinary ADD allocated FK work", allocations, empty)
	}
	for _, tc := range []struct {
		name, actions  string
		names, targets []string
		origins        []int
	}{
		{"empty", "ADD x INT", nil, nil, nil},
		{"skip", "ADD IF NOT EXISTS pid INT CONSTRAINT t_x_fkey CHECK(pid>0) CONSTRAINT t_x_fkey NOT NULL CONSTRAINT t_x_fkey UNIQUE CONSTRAINT t_x_fkey REFERENCES missing,ADD x INT REFERENCES p", []string{"t_x_fkey"}, []string{"p"}, []int{1}},
		{"repeat", "ADD x INT REFERENCES p REFERENCES q,ADD IF NOT EXISTS x INT REFERENCES missing", []string{"t_x_fkey", "t_x_fkey1"}, []string{"p", "q"}, []int{0, 0}},
		{"drop_before", "DROP pid,ADD IF NOT EXISTS pid INT REFERENCES p,ADD IF NOT EXISTS pid INT REFERENCES missing", []string{"t_pid_fkey"}, []string{"p"}, []int{1}},
		{"drop_after", "ADD IF NOT EXISTS pid INT REFERENCES p,DROP pid,ADD IF NOT EXISTS pid INT REFERENCES missing", []string{"t_pid_fkey"}, []string{"p"}, []int{0}},
		{"duplicate", "ADD pid INT REFERENCES missing", nil, nil, nil},
		{"duplicate_later", "ADD x INT REFERENCES missing,ADD x INT REFERENCES missing", nil, nil, nil},
		{"explicit_before", "ADD FOREIGN KEY(x) REFERENCES q,ADD x INT REFERENCES p", []string{"t_x_fkey", "t_x_fkey1"}, []string{"p", "q"}, []int{1, 0}},
		{"explicit_after", "ADD x INT REFERENCES p,ADD FOREIGN KEY(x) REFERENCES q", []string{"t_x_fkey", "t_x_fkey1"}, []string{"p", "q"}, []int{0, 1}},
		{"check_before", "ADD x INT CONSTRAINT t_x_fkey CHECK(x>0) REFERENCES p", []string{"t_x_fkey1"}, []string{"p"}, []int{0}},
		{"check_after", "ADD x INT REFERENCES p CONSTRAINT t_x_fkey CHECK(x>0)", []string{"t_x_fkey1"}, []string{"p"}, []int{0}},
		{"unique_before", "ADD x INT CONSTRAINT t_x_fkey UNIQUE REFERENCES p", []string{"t_x_fkey1"}, []string{"p"}, []int{0}},
		{"unique_after", "ADD x INT REFERENCES p CONSTRAINT t_x_fkey UNIQUE", []string{"t_x_fkey1"}, []string{"p"}, []int{0}},
		{"table_check_before", "ADD CONSTRAINT t_x_fkey CHECK(x>0),ADD x INT REFERENCES p", []string{"t_x_fkey"}, []string{"p"}, []int{1}},
		{"table_check_after", "ADD x INT REFERENCES p,ADD CONSTRAINT t_x_fkey CHECK(x>0)", []string{"t_x_fkey"}, []string{"p"}, []int{0}},
		{"explicit_check_before", "ADD CONSTRAINT t_x_fkey CHECK(x>0),ADD FOREIGN KEY(x) REFERENCES p", []string{"t_x_fkey1"}, []string{"p"}, []int{1}},
		{"explicit_check_after", "ADD FOREIGN KEY(x) REFERENCES p,ADD CONSTRAINT t_x_fkey CHECK(x>0)", []string{"t_x_fkey"}, []string{"p"}, []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := "ALTER TABLE t " + tc.actions
			statements, err := parser.Parse(query)
			if err != nil {
				t.Fatal(err)
			}
			raw := statements[0].(*parser.AlterTable)
			snapshot, err := parser.Parse(query)
			if err != nil {
				t.Fatal(err)
			}
			declarations := table.AlterForeignKeyDeclarations(view, old, raw.Actions)
			if !slices.Equal(alterForeignKeyDeclarationNames(declarations), tc.names) || len(declarations) != len(tc.origins) || len(tc.names) == 0 && declarations != nil {
				t.Fatal("wrong projected stream", declarations, tc.names)
			}
			for i, declaration := range declarations {
				if declaration.Action != tc.origins[i] || declaration.Definition.Target.Parts[0].Text != tc.targets[i] {
					t.Fatal("lost projected origin/order", declarations)
				}
				if declaration.ConstraintIndex >= 0 {
					column := raw.Actions[declaration.Action].Definition
					if !slices.Equal(declaration.Definition.Columns, []parser.Identifier{{Text: column.Name, Pos: column.Pos}}) || len(column.Constraints[declaration.ConstraintIndex].ForeignKey.Columns) != 0 {
						t.Fatal("normalization mutated inline AST", declaration, column)
					}
				}
			}
			retained := table.AlterForeignKeyDeclarations(view, old, raw.Actions)
			if len(declarations) != 0 {
				declarations[0].Definition.Target.Parts[0].Text = "changed"
				declarations[0].Definition.Columns[0].Text = "changed"
				if retained[0].Definition.Target.Parts[0].Text != tc.targets[0] || retained[0].Definition.Columns[0].Text == "changed" {
					t.Fatal("projection results share mutable backing")
				}
			}
			if !reflect.DeepEqual(raw, snapshot[0]) || !reflect.DeepEqual(old, &original) || e.Catalog.State != before || e.Storage.Relations[old.Address()] != store {
				t.Fatal("effectful declaration projection")
			}
		})
	}
}

func TestAlterAddColumnReferencesAdmission(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, query := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE q(id INT PRIMARY KEY)", "CREATE TABLE t(id INT,pid INT)"} {
		sessionSQL(t, s, query)
	}
	old := alterConstraintDescriptor(t, e, "t")
	p, q := alterConstraintDescriptor(t, e, "p"), alterConstraintDescriptor(t, e, "q")
	query := "ALTER TABLE t ADD FOREIGN KEY(n) REFERENCES q(id),ADD n INT REFERENCES p(id) REFERENCES p(id),ADD IF NOT EXISTS pid INT REFERENCES missing,ADD a INT UNIQUE REFERENCES t(a)"
	scope, err := alterConstraintExecuteScope(t, e, s, query)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	keys := tablecmds.ForeignKeysOrZero(scope).AlterKeys
	if len(keys) != 4 || len(tablecmds.ForeignKeysOrZero(scope).ValidationKeys) != 0 {
		t.Fatal("wrong ADD/validation capture partition", keys)
	}
	wantTargets := []ids.ObjectAddress{p.Address(), p.Address(), old.Address(), q.Address()}
	wantOrigins := []int{1, 1, 3, 0}
	for i, key := range keys {
		if key.Action != wantOrigins[i] || key.Target.Address != wantTargets[i] || key.Storage != e.Storage.Relations[wantTargets[i]] {
			t.Fatal("capture not in projected execution order", keys)
		}
		if i == 2 {
			if key.Index != (ids.ObjectAddress{}) {
				t.Fatal("invented preexisting self provider", key)
			}
		} else {
			parent := p
			if i == 3 {
				parent = q
			}
			if key.Index != parent.Constraints.Unique[0].Index {
				t.Fatal("wrong external provider capture", key)
			}
		}
	}
	for _, parent := range []*catalog.Relation{p, q} {
		foreignKeyHeldMode(t, e, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), parent.Address()), 0, scope.LockTicket, lock.ShareRowExclusive)
	}
	foreignKeyHeldMode(t, e, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), old.Address()), 0, scope.LockTicket, lock.AccessExclusive)
	candidate := scope.Catalog.Descriptors[old.Address()].(*catalog.Relation)
	if len(candidate.Constraints.ForeignKeys) != 4 || candidate.Constraints.ForeignKeys[2].Index != candidate.Constraints.Unique[0].Index || len(old.Columns) != 2 || len(old.Constraints.ForeignKeys) != 0 {
		t.Fatal("installation bypassed private completed keys", candidate)
	}
	owned := table.CloneAlterForeignKeyAdditions(keys)
	owned[0].Definition.Columns[0].Text = "changed"
	owned[0].Definition.Target.Parts[0].Text = "changed"
	owned[0].Definition.Referenced[0].Text = "changed"
	if keys[0].Definition.Columns[0].Text != "n" || keys[0].Definition.Target.Parts[0].Text != "p" || keys[0].Definition.Referenced[0].Text != "id" {
		t.Fatal("checkpoint clone borrowed declaration backing")
	}
	expected := table.CloneAlterForeignKeyAdditions(keys)
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	keys[0].Definition.Columns[0].Text = "changed"
	keys[0].Definition.Target.Parts[0].Text = "changed"
	keys[0].Definition.Referenced[0].Text = "changed"
	keys[0].Name = "changed"
	keys[0].Target = ids.ObjectRef{}
	scope.ForeignKeysSlot.AlterKeys = append(keys, keys[0])
	if err := scope.EndPLException(mark, true, nil); err != nil {
		t.Fatal(err)
	}
	restored := tablecmds.ForeignKeysOrZero(scope).AlterKeys
	if !reflect.DeepEqual(restored, expected) || &restored[0].Definition.Columns[0] == &keys[0].Definition.Columns[0] || &restored[0].Definition.Target.Parts[0] == &keys[0].Definition.Target.Parts[0] || &restored[0].Definition.Referenced[0] == &keys[0].Definition.Referenced[0] {
		t.Fatal("checkpoint retained borrowed ADD capture/definition backing", restored)
	}
	if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	scope.Op.Done()
	lockEmpty(t, e)
	if alterConstraintDescriptor(t, e, "t") != old {
		t.Fatal("canceled admission published candidate")
	}

	pending, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE t ALTER CONSTRAINT pending_fk ENFORCED,ADD n INT CONSTRAINT pending_fk REFERENCES p(id) NOT ENFORCED")
	if err != nil {
		_ = pending.Finish(err)
		t.Fatal(err)
	}
	pendingKeys := tablecmds.ForeignKeysOrZero(pending)
	pendingCandidate := pending.Catalog.Descriptors[old.Address()].(*catalog.Relation)
	if len(pendingKeys.AlterKeys) != 1 || len(pendingKeys.ValidationKeys) != 0 || pendingCandidate.Constraints.ForeignKeys[0].NotEnforced || pendingCandidate.Constraints.ForeignKeys[0].NotValid {
		t.Fatal("MISC failed to consume inline ADD declaration", pendingKeys, pendingCandidate)
	}
	if err := pending.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	pending.Op.Done()
	lockEmpty(t, e)
}

func TestAlterAddColumnReferencesAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, declaration, code string }{
		{"history", "DEFAULT 99 REFERENCES p(id) DEFERRABLE INITIALLY DEFERRED", "23503"},
		{"unique_history", "UNIQUE DEFAULT 99 REFERENCES p(id) DEFERRABLE INITIALLY DEFERRED", "23503"},
		{"unique_duplicate", "UNIQUE DEFAULT 1 REFERENCES p(id)", "23505"},
		{"missing", "REFERENCES missing(id)", "42P01"},
		{"duplicate", "REFERENCES missing(id)", "42701"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			for _, query := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1)", "CREATE TABLE t(id INT UNIQUE,pid INT DEFAULT 1)", "INSERT INTO t(id) VALUES(1)"} {
				sessionSQL(t, s, query)
			}
			if tc.name == "unique_duplicate" {
				sessionSQL(t, s, "INSERT INTO t(id) VALUES(2)")
			}
			old := alterConstraintDescriptor(t, e, "t")
			before, store := e.Catalog.State, e.Storage.Relations[old.Address()]
			root := e.Storage.IndexRoots[old.Constraints.Unique[0].Index]
			columns, attributes := slices.Clone(old.Columns), slices.Clone(old.Attributes)
			defaults := maps.Clone(old.ColumnMeta.Defaults)
			uniques := slices.Clone(old.Constraints.Unique)
			roots := maps.Clone(e.Storage.IndexRoots)
			name := "n"
			if tc.name == "duplicate" {
				name = "pid"
			}
			scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE t ADD extra INT DEFAULT 7,ADD "+name+" INT "+tc.declaration)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				_ = scope.Finish(err)
				t.Fatal("wrong real admission/history failure", err)
			}
			if finish := scope.Finish(err); finish == nil {
				t.Fatal("failed candidate published")
			}
			scope.Op.Done()
			lockEmpty(t, e)
			if e.Catalog.State != before || alterConstraintDescriptor(t, e, "t") != old || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[old.Constraints.Unique[0].Index] != root || !reflect.DeepEqual(old.Columns, columns) || !slices.Equal(old.Attributes, attributes) || !reflect.DeepEqual(old.ColumnMeta.Defaults, defaults) || !reflect.DeepEqual(old.Constraints.Unique, uniques) || !reflect.DeepEqual(e.Storage.IndexRoots, roots) || len(old.Constraints.ForeignKeys) != 0 {
				t.Fatal("failure mutated retained descriptor/defaults/storage roots")
			}
		})
	}
}

func TestAlterAddColumnReferencesAdmissionFences(t *testing.T) {
	for _, mode := range []string{"cancel", "namespace", "projection", "parent", "provider"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			holder, worker, other := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, other, "CREATE TABLE p(id INT PRIMARY KEY)")
			sessionSQL(t, other, "CREATE TABLE t(id INT)")
			old := alterConstraintDescriptor(t, e, "t")
			parent := catalogRef(t, e, e.CatalogRead(), "p")
			target := catalogRef(t, e, e.CatalogRead(), "t")
			store := e.Storage.Relations[old.Address()]
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "LOCK TABLE p IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, worker, ctx, "ALTER TABLE t ADD IF NOT EXISTS n INT REFERENCES p(id)", false)
			joined := false
			defer func() {
				cancel()
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			lockPending(t, e, 1)
			state := e.Locks.Inspect()
			if entry := state.Resources[lock.Resource{ObjectRef: parent}]; entry == nil || len(entry.Pending) != 1 {
				t.Fatal("did not wait on captured parent", state)
			}
			if entry := state.Resources[lock.Resource{ObjectRef: target}]; entry != nil && len(entry.Holders) != 0 {
				t.Fatal("grants were not acquired in sorted OID order", state)
			}
			want := ids.ErrCatalogReplaced
			switch mode {
			case "cancel":
				cancel()
				want = context.Canceled
			case "namespace":
				sessionSQL(t, other, "CREATE TABLE reserved(v INT CONSTRAINT t_n_fkey CHECK(v>0))")
			case "projection":
				sessionSQL(t, other, "ALTER TABLE t ADD n INT")
			case "parent":
				sessionSQL(t, holder, "ALTER TABLE p RENAME TO renamed")
			case "provider":
				sessionSQL(t, holder, "ALTER TABLE p DROP CONSTRAINT p_pkey,ADD PRIMARY KEY(id)")
			}
			transactionEnd(t, holder, tx, mode == "parent" || mode == "provider")
			lockAwait(t, done, want)
			joined = true
			lockEmpty(t, e)
			current := alterConstraintDescriptor(t, e, "t")
			if len(current.Constraints.ForeignKeys) != 0 || mode != "projection" && e.Storage.Relations[old.Address()] != store {
				t.Fatal("failed wait published FK/storage", current)
			}
			if mode != "projection" && current != old {
				t.Fatal("failed wait published column")
			}
		})
	}
}

func TestAlterAddColumnReferencesAdmissionOrigins(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT)")
	old := alterConstraintDescriptor(t, e, "t")
	statements, err := parser.Parse("ALTER TABLE t ADD FOREIGN KEY(n) REFERENCES q(id),ADD n INT REFERENCES p(id) REFERENCES q(id)")
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*parser.AlterTable)
	declarations := table.AlterForeignKeyDeclarations(e.CatalogRead(), old, raw.Actions)
	var keys []table.AlterForeignKeyAddition
	for _, declaration := range declarations {
		keys = append(keys, table.AlterForeignKeyAddition{AlterForeignKeyDeclaration: declaration})
	}
	additions := []table.AddedColumn{{Attribute: 2, Column: raw.Actions[1].Definition}}
	if err := table.CheckAlterForeignKeyAdditions(raw.Actions, additions, keys); err != nil {
		t.Fatal("authentic origins rejected", err)
	}
	for _, invalid := range [][]table.AlterForeignKeyAddition{keys[:2], append(slices.Clone(keys), keys[0]), {keys[1], keys[0], keys[2]}, {keys[0], keys[0], keys[2]}} {
		if err := table.CheckAlterForeignKeyAdditions(raw.Actions, additions, invalid); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("missing/extra/reordered/duplicate slot accepted", invalid, err)
		}
	}
	if err := table.CheckAlterForeignKeyAdditions(raw.Actions, []table.AddedColumn{{Column: raw.Actions[1].Definition}}, keys); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("skipped addition consumed FK slot", err)
	}
	changed := keys[0].AlterForeignKeyDeclaration
	changed.Name += "1"
	if keys[0].AlterForeignKeyDeclaration.Equal(&changed) {
		t.Fatal("name drift passed declaration fence")
	}
	changed = table.CloneAlterForeignKeyAdditions(keys)[0].AlterForeignKeyDeclaration
	changed.Definition.Referenced[0].Text = "other"
	if keys[0].AlterForeignKeyDeclaration.Equal(&changed) {
		t.Fatal("normalized definition drift passed declaration fence")
	}
}

func TestAlterAddColumnReferencesUniqueSyntax(t *testing.T) {
	query := "ALTER TABLE t ADD skipped INT UNIQUE,ADD n INT CONSTRAINT named UNIQUE NULLS NOT DISTINCT DEFERRABLE INITIALLY DEFERRED REFERENCES p(id),ADD bare INT UNIQUE"
	statements, err := parser.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := parser.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*parser.AlterTable)
	if table.InheritancePureDefinitionPreparation(raw.Actions) {
		t.Fatal("inline UNIQUE bypassed ordinary index-stage preparation")
	}
	additions := []table.AddedColumn{
		{Column: raw.Actions[0].Definition},
		{Attribute: 7, Column: raw.Actions[1].Definition},
		{Attribute: 11, Column: raw.Actions[2].Definition},
	}
	uniques := table.AddedUniqueSyntax(additions)
	if len(uniques) != 2 || uniques[0].Name.Text != "named" || !uniques[0].NullsNotDistinct || !uniques[0].Deferrable || !uniques[0].InitiallyDeferred || uniques[1].Name.Text != "" {
		t.Fatal("lost admitted UNIQUE order/options", uniques)
	}
	for i, unique := range uniques {
		column := additions[i+1].Column
		if !slices.Equal(unique.Columns, []parser.Identifier{{Text: column.Name, Pos: column.Pos}}) {
			t.Fatal("lost stable local identifier/position", unique, column)
		}
	}
	uniques[0].Columns[0].Text = "changed"
	retained := table.AddedUniqueSyntax(additions)
	if retained[0].Columns[0].Text != "n" || !reflect.DeepEqual(raw, snapshot[0]) {
		t.Fatal("UNIQUE normalization borrowed parser backing", retained, raw)
	}
	dedupQuery := "ALTER TABLE t ADD COLUMN k INT UNIQUE PRIMARY KEY, ADD COLUMN n INT CONSTRAINT u UNIQUE PRIMARY KEY, ADD COLUMN m INT UNIQUE NULLS NOT DISTINCT PRIMARY KEY, ADD COLUMN o INT UNIQUE DEFERRABLE PRIMARY KEY"
	statements, err = parser.Parse(dedupQuery)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = parser.Parse(dedupQuery)
	if err != nil {
		t.Fatal(err)
	}
	raw = statements[0].(*parser.AlterTable)
	additions = []table.AddedColumn{
		{Attribute: 5, Column: raw.Actions[0].Definition},
		{Attribute: 6, Column: raw.Actions[1].Definition},
		{Attribute: 7, Column: raw.Actions[2].Definition},
		{Attribute: 8, Column: raw.Actions[3].Definition},
	}
	uniques = table.AddedUniqueSyntax(additions)
	owners := []int{0, 1, 2, 2, 3, 3}
	names := []string{"", "u", "", "", "", ""}
	primary := []bool{true, true, true, false, true, false}
	nulls := []bool{false, false, false, true, false, false}
	deferrable := []bool{false, false, false, false, false, true}
	deferred := []bool{false, false, false, false, false, false}
	if len(uniques) != len(owners) {
		t.Fatal("same-column dedup kept", uniques)
	}
	for i, unique := range uniques {
		column := additions[owners[i]].Column
		if unique.Primary != primary[i] || unique.NullsNotDistinct != nulls[i] || unique.Deferrable != deferrable[i] || unique.InitiallyDeferred != deferred[i] || unique.Name.Text != names[i] || !slices.Equal(unique.Columns, []parser.Identifier{{Text: column.Name, Pos: column.Pos}}) {
			t.Fatal("same-column dedup lost order/name/flags/identifier", i, unique, column)
		}
	}
	uniques[0].Columns[0].Text = "changed"
	dedupRetained := table.AddedUniqueSyntax(additions)
	if dedupRetained[0].Columns[0].Text != "k" || !reflect.DeepEqual(raw, snapshot[0]) {
		t.Fatal("same-column dedup borrowed parser backing", dedupRetained, raw)
	}
	// Two inline primary keys are kept so staging still raises 42P16.
	duplicateQuery := "ALTER TABLE t ADD COLUMN k INT CONSTRAINT first PRIMARY KEY CONSTRAINT second PRIMARY KEY"
	statements, err = parser.Parse(duplicateQuery)
	if err != nil {
		t.Fatal(err)
	}
	duplicateRaw := statements[0].(*parser.AlterTable)
	duplicate := table.AddedUniqueSyntax([]table.AddedColumn{{Attribute: 5, Column: duplicateRaw.Actions[0].Definition}})
	if len(duplicate) != 2 || !duplicate[0].Primary || !duplicate[1].Primary || duplicate[0].Name.Text != "first" || duplicate[1].Name.Text != "second" {
		t.Fatal("duplicate primary keys were deduplicated", duplicate)
	}
}

func TestAlterAddColumnReferencesUniquePublication(t *testing.T) {
	for _, clause := range []string{
		"ADD pid INT UNIQUE REFERENCES t(pid)",
		"ADD pid INT REFERENCES t(target),ADD target INT UNIQUE",
		"ADD target INT UNIQUE,ADD pid INT REFERENCES t(target)",
	} {
		for _, commit := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/cancel", true: "/commit"}[commit], func(t *testing.T) {
				e := engine.New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, "CREATE TABLE t(id INT UNIQUE)")
				sessionSQL(t, s, "INSERT INTO t VALUES(1)")
				old := alterConstraintDescriptor(t, e, "t")
				before, store := e.Catalog.State, e.Storage.Relations[old.Address()]
				root := e.Storage.IndexRoots[old.Constraints.Unique[0].Index]
				scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE t "+clause)
				if err != nil {
					_ = scope.Finish(err)
					t.Fatal(err)
				}
				candidate := scope.Catalog.Descriptors[old.Address()].(*catalog.Relation)
				if len(candidate.Constraints.Unique) != 2 || len(candidate.Constraints.ForeignKeys) != 1 {
					t.Fatal("missing completed key/FK", candidate)
				}
				key, fk := candidate.Constraints.Unique[1], candidate.Constraints.ForeignKeys[0]
				backing := scope.Catalog.Descriptors[key.Index].(*catalog.Relation)
				privateRoot := index.JournalIndexesOrZero(scope.Catalog).UniqueRoots[key.Index]
				if key.OID == 0 || fk.Index != key.Index || !slices.Equal(fk.Referenced, key.Attributes) || backing.SQLIndex.Parent != old.Address() || backing.SQLIndex.Constraint == nil || backing.SQLIndex.Constraint.OID != key.OID || backing.SQLIndex.Keys[0].Attribute != key.Attributes[0] || privateRoot == nil {
					t.Fatal("self FK did not select actual staged UNIQUE provider/root", key, fk, backing)
				}
				if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[key.Index] != nil || e.Catalog.State.Get(key.Index) != nil || len(old.Constraints.Unique) != 1 || len(old.Columns) != 1 {
					t.Fatal("private UNIQUE/FK work published before Finish")
				}
				if commit {
					if err := scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
					if e.Catalog.State.MustRelation(old.Address()).Constraints.ForeignKeys[0].Index != key.Index || e.Catalog.State.Get(key.Index) != backing || e.Storage.IndexRoots[key.Index] != privateRoot {
						t.Fatal("Finish lost logical/backing/root identity")
					}
				} else {
					if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[old.Constraints.Unique[0].Index] != root || e.Storage.IndexRoots[key.Index] != nil {
						t.Fatal("cancellation leaked UNIQUE/FK/catalog/index work")
					}
				}
				scope.Op.Done()
				lockEmpty(t, e)
			})
		}
	}
}
