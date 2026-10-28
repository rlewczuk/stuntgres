package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestConstraintEnforcementCandidate(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED)"} {
		sessionSQL(t, s, q)
	}
	old := alterConstraintDescriptor(t, e, "t")
	original := *old
	fk := old.Constraints.ForeignKeys[0]
	e.Mu.Lock()
	next, scan, err := table.EnforceConstraintCandidate(core.DDLHost{E: e}, old, "fk", false)
	e.Mu.Unlock()
	if err != nil || next == old || scan == nil || scan.OID != fk.OID || scan.NotValid || scan.NotEnforced {
		t.Fatal(next, scan, err)
	}
	if !reflect.DeepEqual(old, &original) || &old.Constraints.ForeignKeys[0] == &next.Constraints.ForeignKeys[0] || &old.Constraints.Check[0] != &next.Constraints.Check[0] || &old.Attributes[0] != &next.Attributes[0] || old.Revision() != next.Revision() {
		t.Fatal("candidate mutated borrowed backing or detached unrelated state")
	}
	again, history, err := table.EnforceConstraintCandidate(core.DDLHost{E: e}, next, "fk", false)
	if err != nil || again != next || history != nil {
		t.Fatal("no-op changed predecessor", err)
	}
	for _, name := range []string{"ck", "fk"} {
		v, _, err := table.ValidateConstraintCandidate(old, name)
		var d *sqlstate.Error
		if v != old || !errors.As(err, &d) || d.Code != "55000" || d.Message != "cannot validate NOT ENFORCED constraint" {
			t.Fatal(name, v, err)
		}
	}
	_, _, err = table.EnforceConstraintCandidate(core.DDLHost{E: e}, old, "ck", false)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42809" || d.Message != `cannot alter enforceability of constraint "ck" of relation "t"` {
		t.Fatal(err)
	}
	if e.Catalog.State.MustRelation(old.Address()) != old {
		t.Fatal("candidate was published")
	}
}

func TestConstraintEnforcementForeignKeyLifecycle(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED)"} {
		sessionSQL(t, s, q)
	}
	old := alterConstraintDescriptor(t, e, "t")
	original := old.Constraints.ForeignKeys[0]
	if original.TriggerOIDs != ([4]ids.OID{}) || original.TriggerModes != ([4]catalog.TriggerMode{}) {
		t.Fatal("false construction fabricated triggers", original)
	}
	sessionSQL(t, s, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
	enabled := alterConstraintDescriptor(t, e, "t")
	first := enabled.Constraints.ForeignKeys[0]
	if first.OID != original.OID || first.NotEnforced || first.NotValid || first.Target != original.Target || first.Index != original.Index || dml.SameForeignKeyDefinition(first, original) {
		t.Fatal("invalid enable identity/readmission", first)
	}
	for i, id := range first.TriggerOIDs {
		if id == 0 || slices.Contains(first.TriggerOIDs[:i], id) || first.TriggerMode(i) != catalog.TriggerOrigin {
			t.Fatal(first)
		}
	}
	sessionSQL(t, s, "ALTER TABLE t ALTER CONSTRAINT fk NOT ENFORCED")
	disabled := alterConstraintDescriptor(t, e, "t").Constraints.ForeignKeys[0]
	if disabled.OID != original.OID || !disabled.NotEnforced || !disabled.NotValid || disabled.TriggerOIDs != ([4]ids.OID{}) || disabled.TriggerModes != ([4]catalog.TriggerMode{}) || dml.SameForeignKeyDefinition(first, disabled) {
		t.Fatal(disabled)
	}
	sessionSQL(t, s, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
	last := alterConstraintDescriptor(t, e, "t").Constraints.ForeignKeys[0]
	for _, id := range last.TriggerOIDs {
		if id == 0 || slices.Contains(first.TriggerOIDs[:], id) {
			t.Fatal("removed trigger identity reused", id)
		}
	}
	if !reflect.DeepEqual(old.Constraints.ForeignKeys[0], original) || enabled.Constraints.ForeignKeys[0].TriggerOIDs != first.TriggerOIDs {
		t.Fatal("published predecessor changed")
	}
}

func TestConstraintEnforcementAdmission(t *testing.T) {
	for _, self := range []bool{false, true} {
		for _, tc := range []struct {
			name, declaration, actions string
			parentMode                 lock.Mode
		}{
			{"enable", " NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED", lock.ShareRowExclusive},
			{"disable", "", "ALTER CONSTRAINT fk NOT ENFORCED", lock.AccessExclusive},
			{"enable_disable", " NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED,ALTER CONSTRAINT fk NOT ENFORCED", lock.AccessExclusive},
			{"disable_enable", "", "ALTER CONSTRAINT fk NOT ENFORCED,ALTER CONSTRAINT fk ENFORCED", lock.AccessExclusive},
			{"already_enabled", "", "ALTER CONSTRAINT fk ENFORCED", 0},
			{"already_disabled", " NOT ENFORCED", "ALTER CONSTRAINT fk NOT ENFORCED", 0},
			{"add_enable_disable", "", "", lock.AccessExclusive},
			{"add_generated_enable_disable", "", "", lock.AccessExclusive},
			{"add_generated_collision_enable_disable", "", "", lock.AccessExclusive},
			{"add_generated_namespace", "", "", lock.AccessExclusive},
			{"add_generated_check", "", "", lock.AccessExclusive},
			{"add_generated_nn", "", "", lock.AccessExclusive},
			{"add_generated_drop", "", "", lock.AccessExclusive},
			{"add_generated_two", "", "", lock.AccessExclusive},
			{"add_generated_misc_first", "", "", lock.AccessExclusive},
			{"add_generated_utf8", "", "", lock.AccessExclusive},
			{"add_generated_utf8_table", "", "", lock.AccessExclusive},
			{"add_generated_drop_not_null", "", "", lock.AccessExclusive},
			{"add_generated_drop_not_null_namespace", "", "", lock.AccessExclusive},
			{"add_generated_drop_not_null_noop", "", "", lock.AccessExclusive},
			{"add_generated_drop_not_null_bare", "", "", lock.ShareRowExclusive},
			{"add_generated_drop_not_null_domain", "", "", lock.AccessExclusive},
			{"add_generated_drop_not_null_absent", "", "", lock.AccessExclusive},
		} {
			label, parent := tc.name+"/distinct", "p"
			if self {
				label, parent = tc.name+"/self", "t"
			}
			t.Run(label, func(t *testing.T) {
				e := engine.New()
				s := sessionStart(t, e, nil)
				if !self {
					sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
				}
				sessionSQL(t, s, "CREATE TABLE t(id INT PRIMARY KEY,a INT)")
				target := catalogRef(t, e, e.CatalogRead(), "t")
				actions := tc.actions
				tableSQL := "t"
				if tc.name == "add_enable_disable" {
					actions = "ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES " + parent + "(id) NOT ENFORCED,ALTER CONSTRAINT fk ENFORCED,ALTER CONSTRAINT fk NOT ENFORCED"
				} else if strings.HasPrefix(tc.name, "add_generated_") {
					column, name, companion := "a", "t_a_fkey", ""
					switch tc.name {
					case "add_generated_collision_enable_disable":
						sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED")
						name = "t_a_fkey1"
					case "add_generated_namespace":
						sessionSQL(t, s, "CREATE TABLE other(v INT,CONSTRAINT t_a_fkey CHECK(v>0) NOT ENFORCED)")
						name = "t_a_fkey1"
					case "add_generated_check":
						companion, name = ",ADD CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED", "t_a_fkey1"
					case "add_generated_nn":
						companion, name = ",ADD CONSTRAINT t_a_fkey NOT NULL a", "t_a_fkey1"
					case "add_generated_drop":
						sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED")
						companion = ",DROP CONSTRAINT t_a_fkey"
					case "add_generated_drop_not_null", "add_generated_drop_not_null_namespace", "add_generated_drop_not_null_bare", "add_generated_drop_not_null_domain":
						sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT t_a_fkey NOT NULL a")
						companion = ",ALTER a DROP NOT NULL"
						if tc.name == "add_generated_drop_not_null_namespace" {
							sessionSQL(t, s, "CREATE TABLE other(v INT,CONSTRAINT t_a_fkey CHECK(v>0) NOT ENFORCED)")
							name = "t_a_fkey1"
						}
						if tc.name == "add_generated_drop_not_null_domain" {
							sessionSQL(t, s, "CREATE DOMAIN reserved_domain AS INT CONSTRAINT t_a_fkey CHECK(VALUE>0)")
							name = "t_a_fkey1"
						}
					case "add_generated_drop_not_null_absent":
						companion = ",ALTER a DROP NOT NULL"
					case "add_generated_drop_not_null_noop":
						sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED")
						companion, name = ",ALTER a DROP NOT NULL", "t_a_fkey1"
					case "add_generated_utf8_table":
						relation := strings.Repeat("é", 28)
						tableSQL = `"` + relation + `"`
						sessionSQL(t, s, "ALTER TABLE t RENAME TO "+tableSQL)
						sessionSQL(t, s, "ALTER TABLE "+tableSQL+` ADD CONSTRAINT "`+relation+`_a_fkey" CHECK(false) NOT ENFORCED`)
						if self {
							parent = tableSQL
						}
						name = strings.Repeat("é", 27) + "_a_fkey1"
					case "add_generated_utf8":
						column = strings.Repeat("é", 28)
						sessionSQL(t, s, `ALTER TABLE t RENAME COLUMN a TO "`+column+`"`)
						sessionSQL(t, s, `ALTER TABLE t ADD CONSTRAINT "t_`+column+`_fkey" CHECK(false) NOT ENFORCED`)
						name = "t_" + strings.Repeat("é", 27) + "_fkey1"
					}
					declare := `ADD FOREIGN KEY("` + column + `") REFERENCES ` + parent + "(id) NOT ENFORCED"
					actions = declare + companion
					if tc.name == "add_generated_two" {
						actions += "," + declare + `,ALTER CONSTRAINT t_a_fkey1 ENFORCED,ALTER CONSTRAINT t_a_fkey1 NOT ENFORCED`
					}
					toggle := `ALTER CONSTRAINT "` + name + `" ENFORCED,ALTER CONSTRAINT "` + name + `" NOT ENFORCED`
					if tc.name == "add_generated_misc_first" {
						actions = toggle + "," + actions
					} else if tc.name != "add_generated_drop_not_null_bare" {
						actions += "," + toggle
					}
				} else {
					sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES "+parent+"(id)"+tc.declaration)
				}
				beforeCatalog, beforeStore := e.Catalog.State, e.Storage.Relations[target.Address]
				scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE "+tableSQL+" "+actions)
				if tc.name == "add_generated_check" {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Message != `constraint "t_a_fkey" for relation "t" already exists` {
						t.Fatal("expected PostgreSQL duplicate-name boundary", err)
					}
					if e.Catalog.State != beforeCatalog || e.Storage.Relations[target.Address] != beforeStore {
						t.Fatal("duplicate-name failure published a prefix")
					}
					if finish := scope.Finish(err); !errors.As(finish, &diagnostic) || diagnostic.Code != "42710" {
						t.Fatal(finish)
					}
					scope.Op.Done()
					lockEmpty(t, e)
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "add_generated_drop_not_null_bare" {
					candidate := scope.Catalog.Descriptors[target.Address].(*catalog.Relation)
					if len(candidate.Constraints.ForeignKeys) != 1 || candidate.Constraints.ForeignKeys[0].Name != "t_a_fkey" || len(candidate.Constraints.NotNull) != 1 {
						// The unrelated id primary key retains its own NN fact.
						t.Fatal("bare ADD retained stale NN name reservation", candidate.Constraints)
					}
				}
				t.Cleanup(func() {
					if !scope.Finished {
						_ = scope.Finish(context.Canceled)
						scope.Op.Done()
					}
				})
				foreignKeyHeldMode(t, e, target, 0, scope.LockTicket, lock.AccessExclusive)
				if !self {
					foreignKeyHeldMode(t, e, catalogRef(t, e, e.CatalogRead(), "p"), 0, scope.LockTicket, tc.parentMode)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				scope.Op.Done()
				lockEmpty(t, e)
			})
		}
	}
}

func TestConstraintEnforcementPublication(t *testing.T) {
	for _, terminal := range []string{"success", "cancel", "late-error", "provider-replaced", "storage-replaced"} {
		t.Run(terminal, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)"} {
				sessionSQL(t, s, q)
			}
			old := alterConstraintDescriptor(t, e, "t")
			before, store := e.Catalog.State, e.Storage.Relations[old.Address()]
			q := "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED"
			if terminal == "late-error" {
				q += ",VALIDATE CONSTRAINT ck,ADD COLUMN companion INT"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			raw, err := parser.Parse(q)
			if err == nil {
				_, err = scope.Execute(raw[0], 0, nil)
			}
			if terminal == "late-error" {
				if err == nil {
					t.Fatal("late action failure suppressed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if !scope.Finished {
					_ = scope.Finish(context.Canceled)
					scope.Op.Done()
				}
			})
			if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || !old.Constraints.ForeignKeys[0].NotEnforced || old.Constraints.ForeignKeys[0].TriggerOIDs != ([4]ids.OID{}) {
				t.Fatal("execution published prefix")
			}
			if terminal != "late-error" {
				candidate := scope.Catalog.Descriptors[old.Address()].(*catalog.Relation)
				if candidate.Constraints.ForeignKeys[0].NotEnforced || candidate.Revision() != old.Revision()+1 {
					t.Fatal("missing private transition", candidate)
				}
			}
			// Exercise actual final-publication fences with authentic successor
			// owners. These structural adversarial states are not public SQL claims.
			e.Mu.Lock()
			if terminal == "provider-replaced" {
				provider := *before.MustRelation(old.Constraints.ForeignKeys[0].Index)
				provider.ObjectRevision++
				replaced := catalog.CloneState(before)
				replaced.SetDescriptor(provider.Address(), &provider)
				e.Catalog.State = replaced
			}
			if terminal == "storage-replaced" {
				replaced := *store
				e.Storage.Relations[old.Address()] = &replaced
			}
			published, publishedStore := e.Catalog.State, e.Storage.Relations[old.Address()]
			e.Mu.Unlock()
			if terminal == "cancel" {
				cancel()
			}
			finish := scope.Finish(err)
			if terminal == "success" {
				if finish != nil {
					t.Fatal(finish)
				}
				if e.Storage.Relations[old.Address()] != store {
					t.Fatal("metadata toggle replaced heap")
				}
			} else {
				if finish == nil || terminal == "cancel" && !errors.Is(finish, context.Canceled) || e.Catalog.State != published || e.Storage.Relations[old.Address()] != publishedStore {
					t.Fatal("publication failure leaked", finish)
				}
			}
			scope.Op.Done()
			lockEmpty(t, e)
		})
	}
}

func TestConstraintEnforcementInheritanceMerge(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED)", "CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0) ENFORCED)", "CREATE TABLE child() INHERITS(t,p)"} {
		sessionSQL(t, s, q)
	}
	falseParent := alterConstraintDescriptor(t, e, "t")
	trueParent := alterConstraintDescriptor(t, e, "p")
	child := alterConstraintDescriptor(t, e, "child")
	check := child.Constraints.Check[0]
	if check.NotEnforced || check.NotValid || check.OID == falseParent.Constraints.Check[0].OID || check.OID == trueParent.Constraints.Check[0].OID {
		t.Fatal("merge did not own authentic enforced identity", check)
	}
	origin := child.Inheritance.Checks["ck"]
	if origin.Local || origin.Count() != 2 || len(origin.Origins) != 2 || origin.Origins[0].Parent != falseParent.Address() || origin.Origins[1].Parent != trueParent.Address() {
		t.Fatal(origin)
	}
	if !falseParent.Constraints.Check[0].NotEnforced || trueParent.Constraints.Check[0].NotEnforced {
		t.Fatal("merge mutated parent")
	}
	before := e.Catalog.State
	scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE child ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED")
	if err == nil {
		t.Fatal("false local declaration over enforced parents accepted")
	}
	if finish := scope.Finish(err); finish == nil {
		t.Fatal("failed merge published")
	}
	scope.Op.Done()
	if e.Catalog.State != before || alterConstraintDescriptor(t, e, "child") != child {
		t.Fatal("failed merge detached predecessor")
	}
}

func TestConstraintEnforcementNameProjection(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE p(id INT PRIMARY KEY)",
		"CREATE TABLE t(a INT,CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED)",
		"CREATE TABLE other(v INT,CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED)",
	} {
		sessionSQL(t, s, q)
	}
	old := alterConstraintDescriptor(t, e, "t")
	original := *old
	original.Constraints.Check = slices.Clone(old.Constraints.Check)
	original.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
	view := e.CatalogRead()
	before, store := e.Catalog.State, e.Storage.Relations[old.Address()]
	statements, err := parser.Parse("ALTER TABLE t DROP CONSTRAINT t_a_fkey,ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED,ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED")
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*parser.AlterTable)
	names := table.AlterForeignKeyNames(view, old, raw.Actions)
	if !slices.Equal(names, []string{"t_a_fkey1", "t_a_fkey2"}) {
		t.Fatal("child DROP erased another relation's namespace reservation", names)
	}
	for _, action := range raw.Actions {
		if action.ForeignKey != nil && action.ForeignKey.Name.Text != "" {
			t.Fatal("name projection mutated held parser syntax")
		}
	}
	if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || !reflect.DeepEqual(old, &original) {
		t.Fatal("name projection mutated published state")
	}
	sessionSQL(t, s, "ALTER TABLE other DROP CONSTRAINT t_a_fkey")
	if current := table.AlterForeignKeyNames(e.CatalogRead(), old, raw.Actions); !slices.Equal(current, []string{"t_a_fkey", "t_a_fkey1"}) {
		t.Fatal("name schedule failed to observe genuine namespace change", current)
	}
	if retained := table.AlterForeignKeyNames(view, old, raw.Actions); !slices.Equal(retained, names) {
		t.Fatal("retained naming authority followed live catalog", retained, names)
	}
}

func TestConstraintEnforcementDropNotNullProjection(t *testing.T) {
	for _, tc := range []struct {
		name, definition, dropped, expected string
		removes                             bool
	}{
		{"remove", "a INT CONSTRAINT t_a_fkey NOT NULL,b INT", "a", "t_a_fkey", true},
		{"namespace", "a INT CONSTRAINT t_a_fkey NOT NULL,b INT", "a", "t_a_fkey1", true},
		{"domain", "a INT CONSTRAINT t_a_fkey NOT NULL,b INT", "a", "t_a_fkey1", true},
		{"nullable_noop", "a INT,b INT", "a", "t_a_fkey", false},
		{"noop_check", "a INT,b INT,CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED", "a", "t_a_fkey1", false},
		{"noop_other_column", "a INT CONSTRAINT t_a_fkey NOT NULL,b INT", "b", "t_a_fkey1", false},
		{"remove_other_column", "a INT,b INT CONSTRAINT t_a_fkey NOT NULL", "b", "t_a_fkey", true},
		{"retain_other_nn", "a INT CONSTRAINT t_a_fkey NOT NULL,b INT CONSTRAINT kept NOT NULL", "a", "t_a_fkey", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
			sessionSQL(t, s, "CREATE TABLE t("+tc.definition+")")
			if tc.name == "namespace" {
				sessionSQL(t, s, "CREATE TABLE other(v INT,CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED)")
			}
			if tc.name == "domain" {
				sessionSQL(t, s, "CREATE DOMAIN reserved_domain AS INT CONSTRAINT t_a_fkey CHECK(VALUE>0)")
			}
			old := alterConstraintDescriptor(t, e, "t")
			original := *old
			original.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
			original.Constraints.Check = slices.Clone(old.Constraints.Check)
			view, before, store := e.CatalogRead(), e.Catalog.State, e.Storage.Relations[old.Address()]
			statements, err := parser.Parse("ALTER TABLE t ALTER " + tc.dropped + " DROP NOT NULL,ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED")
			if err != nil {
				t.Fatal(err)
			}
			raw := statements[0].(*parser.AlterTable)
			op := alterConstraintOperation(t, s)
			defer op.Done()
			candidate, err := table.NotNullCandidate(op, view, old, raw.Actions[0], table.NotNullDirect)
			if err != nil {
				t.Fatal(err)
			}
			if (candidate != old) != tc.removes {
				t.Fatal("fixture did not exercise authentic removal/noop", candidate, tc.removes)
			}
			bound := table.ForeignKeyConstraintName(candidate, *raw.Actions[1].ForeignKey, catalog.ConstraintNames(view, candidate.Namespace(), candidate))
			if bound != tc.expected {
				t.Fatal("candidate naming differs from source-grounded expectation", bound, tc.expected)
			}
			names := table.AlterForeignKeyNames(view, old, raw.Actions)
			if !slices.Equal(names, []string{tc.expected}) {
				t.Fatal("admission naming diverged from authentic NN candidate", names, tc.expected)
			}
			if tc.removes {
				namedSQL, err := parser.Parse("ALTER TABLE t DROP CONSTRAINT t_a_fkey,ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED")
				if err != nil {
					t.Fatal(err)
				}
				named := namedSQL[0].(*parser.AlterTable)
				namedCandidate, err := table.NotNullCandidate(op, view, old, named.Actions[0], table.NotNullDirect)
				if err != nil || !reflect.DeepEqual(namedCandidate, candidate) {
					t.Fatal("named and column NN removal disagree", namedCandidate, candidate, err)
				}
				if namedNames := table.AlterForeignKeyNames(view, old, named.Actions); !slices.Equal(namedNames, names) {
					t.Fatal("named and column removal naming disagree", namedNames, names)
				}
			}
			if tc.name == "retain_other_nn" {
				kept := slices.IndexFunc(old.Constraints.NotNull, func(fact catalog.NotNullConstraint) bool { return fact.Name == "kept" })
				if kept < 0 || len(candidate.Constraints.NotNull) != 1 || !reflect.DeepEqual(candidate.Constraints.NotNull[0], old.Constraints.NotNull[kept]) {
					t.Fatal("removal changed another NN identity/definition")
				}
			}
			if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || !reflect.DeepEqual(old, &original) || raw.Actions[1].ForeignKey.Name.Text != "" {
				t.Fatal("pure naming/removal mutated published state or held syntax")
			}
		})
	}
}
