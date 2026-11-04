package pgcatalog_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/hbarules"
	"stuntgres/internal/parser"
	"testing"
)

func TestPGHBAFileRulesHolderLifecycle(t *testing.T) {
	destination := engine.New()
	holder := new(hbarules.Source)
	destination.HBARules = holder
	holder.Set([]hbarules.Rule{{FileName: new("destination A"), Options: []string{"secret A"}}})
	retained := holder.Capture()
	stage := destination.RestoreStage()
	if stage.HBARules != holder {
		t.Fatal("staging lost destination holder")
	}
	// A completed Set after staging must survive replacement: stage holds the
	// source, not a frozen copy of the former diagnostic snapshot.
	holder.Set([]hbarules.Rule{{FileName: new("destination B")}})
	latest := holder.Capture()
	if _, err := destination.ReplaceCatalog(t.Context(), stage, stage.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if destination.HBARules != holder || stage.HBARules.Capture() != latest || destination.HBARules.Capture() != latest {
		t.Fatal("staged replacement froze diagnostic facts")
	}
	imported := engine.New()
	imported.HBARules = new(hbarules.Source)
	imported.HBARules.Set([]hbarules.Rule{{FileName: new("source must never import")}})
	if _, err := destination.ReplaceCatalog(t.Context(), imported, imported.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if destination.HBARules != holder || holder.Capture() != latest {
		t.Fatal("replacement imported source holder/facts")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := destination.ReplaceCatalog(ctx, imported, imported.CatalogRead()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled replacement", err)
	}
	if destination.HBARules != holder || holder.Capture() != latest {
		t.Fatal("cancelled replacement changed facts")
	}
	if err := engine.Reset(t.Context(), destination); err != nil {
		t.Fatal(err)
	}
	if destination.HBARules != holder || holder.Capture() != latest {
		t.Fatal("Reset lost destination holder/latest facts")
	}
	for _, name := range []string{"hba_unit_database", "hba_future_database"} {
		if _, err := enginetest.Execute(destination, t.Context(), &parser.CreateDatabaseStmt{Name: name}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if destination.Cluster.Seed == nil {
		t.Fatal("missing template seed")
	}
	targets := []*core.Engine{destination, stage, destination.Cluster.Seed, destination.Cluster.Targets["hba_unit_database"], destination.Cluster.Targets["hba_future_database"]}
	holder.Set([]hbarules.Rule{{FileName: new("destination C")}})
	current := holder.Capture()
	for _, target := range targets {
		if target == nil || target.HBARules != holder || target.HBARules.Capture() != current {
			t.Fatal("seed/logical target lost live destination holder")
		}
	}
	holder.Set(nil)
	for _, target := range targets {
		if len(target.HBARules.Capture().Rules()) != 0 {
			t.Fatal("clear did not reach existing targets")
		}
	}
	if *retained.Rules()[0].FileName != "destination A" || retained.Rules()[0].Options[0] != "secret A" || *latest.Rules()[0].FileName != "destination B" || *current.Rules()[0].FileName != "destination C" {
		t.Fatal("controls mutated retained diagnostic backing")
	}
}
