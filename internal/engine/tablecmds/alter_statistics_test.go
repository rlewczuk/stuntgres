package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterStatisticsConfiguration(t *testing.T) {
	e := engine.New()
	old := &catalog.Relation{ObjectName: "t", ObjectRevision: 5, Columns: []catalog.Column{{Name: "a", Usage: enginetest.Usage(e, sqltype.Int4)}, {Name: "b", Usage: enginetest.Usage(e, sqltype.Int4)}}, Attributes: []int32{3, 9}, Index: map[string]int{"a": 0, "b": 1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 9, Statistics: map[int32]catalog.ColumnStatisticsConfig{3: {TargetSet: true, Target: 3, Options: []catalog.ForeignOption{{Name: "n_distinct", Value: "2"}}}, 9: {TargetSet: true, Target: 4, Options: []catalog.ForeignOption{{Name: "n_distinct", Value: "7"}}}}}}
	action := parser.AlterTableAction{Action: parser.SetColumnOptions, Column: parser.Identifier{Text: "a"}, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "n_distinct"}, HasValue: true, Value: parser.Value{Text: "-0.5"}}}}
	next, err := table.ColumnStatisticsCandidate(old, action)
	if err != nil || next == old || next.Revision() != 5 || &next.Columns[0] != &old.Columns[0] || &next.Attributes[0] != &old.Attributes[0] {
		t.Fatal(next, err)
	}
	if old.ColumnMeta.Statistics[3].Options[0].Value != "2" || next.ColumnMeta.Statistics[3].Options[0].Value != "-0.5" || &old.ColumnMeta.Statistics[9].Options[0] != &next.ColumnMeta.Statistics[9].Options[0] {
		t.Fatal("touched metadata not detached or untouched payload copied")
	}
	for _, bad := range []catalog.ColumnStatisticsConfig{{Target: 1}, {TargetSet: true, Target: -1}, {TargetSet: true, Target: 10001}, {Options: []catalog.ForeignOption{{Name: "bogus", Value: "2"}}}, {Options: []catalog.ForeignOption{{Name: "n_distinct", Value: "NaN"}}}} {
		if bad.Valid() {
			t.Fatal("accepted invalid import", bad)
		}
	}
	for _, name := range []string{"n_distinct", "n_distinct_inherited"} {
		for _, tc := range []struct {
			text  string
			value float64
		}{
			{"0x2.0", 2}, {"0X2", 2}, {"+0x2.0", 2}, {"-0x0.8", -.5},
			{"0x2p0", 2}, {"0x0.0", 0},
			{"0x1p-1023", 0x1p-1023}, {"0x1p-1074", 0x1p-1074},
			{"-0x1p-1023", -0x1p-1023}, {"-0x1p-1074", -0x1p-1074},
			{"0x0.fffffffffffffp-1022", 0x0.fffffffffffffp-1022}, {"0x1p-1022", 0x1p-1022},
		} {
			value, ok := catalog.ParseColumnDistinct(tc.text)
			config := catalog.ColumnStatisticsConfig{Options: []catalog.ForeignOption{{Name: name, Value: tc.text}}}
			if !ok || value != tc.value || !config.Valid() {
				t.Fatal("valid hexadecimal real/import rejected", name, tc, value, ok)
			}
			action := parser.AlterTableAction{Action: parser.SetColumnOptions, Column: parser.Identifier{Text: "a"}, Options: []parser.IndexOption{{Name: parser.Identifier{Text: name}, HasValue: true, Value: parser.Value{Text: tc.text}}}}
			candidate, err := table.ColumnStatisticsCandidate(old, action)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, option := range candidate.ColumnMeta.Statistics[3].Options {
				if option.Name == name {
					found = true
					if option.Value != tc.text {
						t.Fatal("conversion rewrote option spelling", option, tc.text)
					}
				}
			}
			if !found || old.ColumnMeta.Statistics[3].Options[0].Value != "2" {
				t.Fatal("candidate lost text or changed old configuration")
			}
		}
	}
	for _, text := range []string{
		"0x", "0x2.0junk", "0x2p", "0x2_0", "0x1p1024", "0x1p-2000",
		"0x1.0000000000001p-1023", "-0x1.0000000000001p-1023", "5e-324", "-5e-324", "0x1p-1075", "-0x1p-1075", "1e-400", "-1e-400",
	} {
		if value, ok := catalog.ParseColumnDistinct(text); ok {
			t.Fatal("invalid hexadecimal real accepted", text, value)
		}
		for _, name := range []string{"n_distinct", "n_distinct_inherited"} {
			config := catalog.ColumnStatisticsConfig{Options: []catalog.ForeignOption{{Name: name, Value: text}}}
			if config.Valid() {
				t.Fatal("invalid/ERANGE input accepted by imported configuration", name, text)
			}
		}
	}
	for _, name := range []string{"n_distinct", "n_distinct_inherited"} {
		for _, tc := range []struct{ input, text string }{
			{"02", "2"}, {"+2", "2"}, {"1_0", "10"}, {"0o2", "2"},
			{"'02'", "02"}, {"'+2'", "+2"}, {"2.0", "2.0"},
		} {
			statements, err := parser.Parse("ALTER TABLE t ALTER a SET(" + name + "=" + tc.input + ")")
			if err != nil {
				t.Fatal(err)
			}
			action := statements[0].(*parser.AlterTable).Actions[0]
			candidate, err := table.ColumnStatisticsCandidate(old, action)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, option := range candidate.ColumnMeta.Statistics[3].Options {
				if option.Name == name {
					found = true
					if option.Value != tc.text {
						t.Fatal("SQL integer semantic coercion or quoted/Float spelling changed", name, tc, option)
					}
				}
			}
			if !found || old.ColumnMeta.Statistics[3].Options[0].Value != "2" {
				t.Fatal("coercion lost option or mutated original configuration")
			}
		}
		for _, padding := range []string{" ", "\t", "\n", "\r", "\f", "\v"} {
			text := padding + "2" + padding
			value, ok := catalog.ParseColumnDistinct(text)
			config := catalog.ColumnStatisticsConfig{Options: []catalog.ForeignOption{{Name: name, Value: text}}}
			if !ok || value != 2 || !config.Valid() {
				t.Fatal("C whitespace rejected", name, text, value, ok)
			}
		}
		for _, text := range []string{"\u00a02", "2\u00a0"} {
			config := catalog.ColumnStatisticsConfig{Options: []catalog.ForeignOption{{Name: name, Value: text}}}
			if value, ok := catalog.ParseColumnDistinct(text); ok || config.Valid() {
				t.Fatal("non-C whitespace accepted in conversion/import", name, text, value, ok)
			}
		}
	}
	for _, tc := range []struct{ options, message string }{
		{`n_distinct='bad',foo.n_distinct_inherited=2`, `unrecognized parameter namespace "foo"`},
		{`foo.n_distinct_inherited=2,n_distinct='bad'`, `unrecognized parameter namespace "foo"`},
		{`n_distinct_inherited='bad',foo.n_distinct=2`, `unrecognized parameter namespace "foo"`},
		{`bogus=1,foo.n_distinct=2`, `unrecognized parameter namespace "foo"`},
		{`n_distinct=1,n_distinct=2,foo.n_distinct=2`, `unrecognized parameter namespace "foo"`},
		{`n_distinct=-2,foo.n_distinct=2`, `unrecognized parameter namespace "foo"`},
		{`"bad=name"=2,foo.n_distinct=2`, `invalid option name "bad=name": must not contain "="`},
		{`foo.n_distinct=2,"bad=name"=2`, `unrecognized parameter namespace "foo"`},
		{`n_distinct='bad',"bad=name"=2`, `invalid option name "bad=name": must not contain "="`},
	} {
		statements, err := parser.Parse("ALTER TABLE t ALTER a SET(" + tc.options + ")")
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := table.ColumnStatisticsCandidate(old, statements[0].(*parser.AlterTable).Actions[0])
		var diagnostic *sqlstate.Error
		if candidate != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != tc.message {
			t.Fatal("SET transformation/semantic order", tc, candidate, err)
		}
		if old.Revision() != 5 || !reflect.DeepEqual(old.ColumnMeta.Statistics[3], catalog.ColumnStatisticsConfig{TargetSet: true, Target: 3, Options: []catalog.ForeignOption{{Name: "n_distinct", Value: "2"}}}) || old.ColumnMeta.Statistics[9].Options[0].Value != "7" {
			t.Fatal("failed candidate mutated owned or untouched metadata")
		}
	}
	for _, kind := range []parser.AlterTableActionKind{parser.SetColumnStatistics, parser.SetColumnOptions, parser.ResetColumnOptions} {
		a := parser.AlterTableAction{Action: kind, Column: parser.Identifier{Text: "a"}}
		if table.AlterActionPhase(kind) != 10 || table.InheritanceRecursiveAction(old, a, false) != (kind == parser.SetColumnStatistics) || table.InheritanceRecursiveAction(old, a, true) {
			t.Fatal("phase/recursion", kind)
		}
		if !table.MetadataOnlyActions([]parser.AlterTableAction{a}) || table.MetadataOnlyActions([]parser.AlterTableAction{a, {Action: parser.AddColumn}}) {
			t.Fatal("statistics metadata-only classification swallowed physical work", kind)
		}
	}
	statements, err := parser.Parse(`ALTER TABLE IF EXISTS ONLY public.t ALTER COLUMN a SET STATISTICS -1, ALTER a SET(n_distinct=-0.5), ALTER a RESET(n_distinct)`)
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*parser.AlterTable)
	if !raw.Only || !raw.IfExists || len(raw.Actions) != 3 || raw.Actions[0].StatisticsTarget != -1 || raw.Actions[1].Options[0].Value.Text != "-0.5" || raw.Actions[0].Column.Pos.Character == 0 {
		t.Fatal(raw)
	}
	if _, err := parser.Parse(`ALTER TABLE t ALTER a SET STATISTICS 3 trailing`); err == nil {
		t.Fatal("valid prefix accepted")
	}
	t.Run("invalid_import", func(t *testing.T) {
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE statistics_import(a integer)")
		base := e.CatalogRead()
		ref := enginetest.CatalogRef(t, e, base, "statistics_import")
		original := base.MustRelation(ref.Address)
		invalid := *original
		invalid.ObjectRevision++
		invalid.ColumnMeta.Statistics = map[int32]catalog.ColumnStatisticsConfig{9: {TargetSet: true, Target: 3}}
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		if err := stage.Replace(&invalid); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
			t.Fatal("provider accepted foreign stable-attribute configuration")
		}
		if e.CatalogRead().MustRelation(ref.Address) != original {
			t.Fatal("invalid import published")
		}
	})
}

func TestAlterStatisticsPublication(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "cancel"}[cancelled], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE statistics_owned(a integer,b integer)")
			enginetest.SessionSQL(t, s, "INSERT INTO statistics_owned VALUES(1,1),(2,2)")
			enginetest.SessionSQL(t, s, "ANALYZE statistics_owned")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "statistics_owned")
			old := e.CatalogRead().MustRelation(ref.Address)
			root := e.Catalog.State
			store := e.Storage.Relations[ref.Address]
			metadata := e.Storage.RelationMetadata[ref.Address]
			rows, ends, generation := store.Versions, store.Ends, store.Generation
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, cleanup, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished unit"))
			statement := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "statistics_owned"}}}, Actions: []parser.AlterTableAction{{Action: parser.SetColumnStatistics, Column: parser.Identifier{Text: "a"}, StatisticsTarget: 7}, {Action: parser.SetColumnOptions, Column: parser.Identifier{Text: "a"}, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "n_distinct"}, HasValue: true, Value: parser.Value{Text: "2"}}}}}}
			if _, err := scope.Execute(statement, 0, nil); err != nil {
				t.Fatal(err)
			}
			candidate := scope.Catalog.Descriptors[ref.Address].(*catalog.Relation)
			if candidate == old || candidate.Revision() != old.Revision()+1 || candidate.ColumnMeta.Statistics[1].Target != 7 || e.Catalog.State != root || len(old.ColumnMeta.Statistics) != 0 {
				t.Fatal("candidate published before Finish")
			}
			if cancelled {
				cancel()
			}
			err = scope.Finish(nil)
			if cancelled {
				if !errors.Is(err, context.Canceled) || e.Catalog.State != root || e.CatalogRead().MustRelation(ref.Address) != old {
					t.Fatal("canceled stage published", err)
				}
			} else {
				if err != nil || e.CatalogRead().MustRelation(ref.Address) != candidate {
					t.Fatal(err)
				}
			}
			if e.Storage.Relations[ref.Address] != store || e.Storage.RelationMetadata[ref.Address] != metadata || !storage.SameVersionRoot(rows, store.Versions) || store.Ends != ends || store.Generation != generation || root.MustRelation(ref.Address) != old {
				t.Fatal("metadata-only publication changed retained row/statistics backing")
			}
		})
	}
	t.Run("disabled_retains_fact", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE statistics_skip(a integer,b integer)", "INSERT INTO statistics_skip VALUES(1,1),(2,2)", "ANALYZE statistics_skip", "ALTER TABLE statistics_skip ALTER a SET STATISTICS 0"} {
			enginetest.SessionSQL(t, s, q)
		}
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "statistics_skip")
		before := e.Storage.RelationMetadata[ref.Address].Statistics
		enginetest.SessionSQL(t, s, "INSERT INTO statistics_skip VALUES(3,3)")
		enginetest.SessionSQL(t, s, "ANALYZE statistics_skip")
		after := e.Storage.RelationMetadata[ref.Address]
		if e.Storage.Relations[ref.Address].Maintenance.Tuples != 3 {
			t.Fatal("heap ANALYZE population did not refresh", e.Storage.Relations[ref.Address].Maintenance.Tuples)
		}
		if after.Statistics.Columns[storage.StatisticsKey{Attribute: 1}] != before.Columns[storage.StatisticsKey{Attribute: 1}] || after.Statistics.Columns[storage.StatisticsKey{Attribute: 1}].Rows != 2 {
			t.Fatal("disabled column did not retain the exact stale fact/backing")
		}
		if after.Statistics.Columns[storage.StatisticsKey{Attribute: 2}] == before.Columns[storage.StatisticsKey{Attribute: 2}] || after.Statistics.Columns[storage.StatisticsKey{Attribute: 2}].Rows != 3 {
			t.Fatal("enabled sibling did not publish a fresh full-population fact")
		}
		if !reflect.DeepEqual(before.Columns[storage.StatisticsKey{Attribute: 1}].Histogram, after.Statistics.Columns[storage.StatisticsKey{Attribute: 1}].Histogram) {
			t.Fatal("retained distribution mutated")
		}
		enginetest.SessionSQL(t, s, "ALTER TABLE statistics_skip ALTER a TYPE bigint")
		changed := e.CatalogRead().MustRelation(ref.Address)
		compatible := storage.CompatibleStatistics(after.Statistics, changed)
		if !changed.ColumnMeta.Statistics[1].TargetSet || changed.ColumnMeta.Statistics[1].Target != 0 || compatible.Columns[storage.StatisticsKey{Attribute: 1}] != nil || compatible.Columns[storage.StatisticsKey{Attribute: 2}] != after.Statistics.Columns[storage.StatisticsKey{Attribute: 2}] {
			t.Fatal("type transition lost configuration or retained incompatible fact")
		}
	})
	t.Run("targets_and_flavors", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{
			"CREATE TABLE statistics_flavor(a integer,b integer,c integer)",
			"CREATE TABLE statistics_flavor_child() INHERITS(statistics_flavor)",
			"INSERT INTO statistics_flavor VALUES(1,1,1),(2,2,2),(3,3,3),(4,4,4),(5,5,5),(6,6,6),(7,7,7),(8,8,8),(9,9,9),(10,10,10)",
			"INSERT INTO statistics_flavor_child VALUES(11,11,11),(12,12,12)",
			"SET default_statistics_target=1",
			"ALTER TABLE statistics_flavor ALTER a SET(n_distinct=2,n_distinct_inherited=-0.5), ALTER b SET STATISTICS 4, ALTER c SET STATISTICS 0",
		} {
			enginetest.SessionSQL(t, s, q)
		}
		// SessionSQL uses plain Acquire for engine setup; collect through the
		// genuine command-admission seam to capture the session's target.
		func() {
			op, _, err := s.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			if op.StatisticsTarget != 1 {
				t.Fatal("command omitted admitted statistics target", op.StatisticsTarget)
			}
			if _, err := e.Execute(op, enginetest.Statement(t, "ANALYZE statistics_flavor"), 0, nil); err != nil {
				t.Fatal(err)
			}
		}()
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "statistics_flavor")
		facts := e.Storage.RelationMetadata[ref.Address].Statistics
		local := facts.Columns[storage.StatisticsKey{Attribute: 1}]
		inherited := facts.Columns[storage.StatisticsKey{Attribute: 1, Inherited: true}]
		explicit := facts.Columns[storage.StatisticsKey{Attribute: 2}]
		if local.DistinctCount != 10 || local.DistinctOverride != 2 || len(local.Histogram) != 2 || inherited.DistinctCount != 12 || inherited.DistinctOverride != -.5 || len(explicit.Histogram) != 5 || facts.Columns[storage.StatisticsKey{Attribute: 3}] != nil {
			t.Fatal("captured targets/flavor override changed measured summaries", local, inherited, explicit)
		}
	})
}
