package tablecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
)

func TestSerialIncomingDefaultDependencyFence(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE SEQUENCE q`)
	base := e.CatalogRead().State()
	target := roleTableRef(t, e, "q").Address
	enginetest.Run(t, e, `CREATE TABLE t(id INT DEFAULT nextval('q'))`)
	changes := session.NewJournal()
	changes.Dependencies = append(changes.Dependencies, session.DependencyObservation{base, []ids.ObjectAddress{target}})
	e.Mu.Lock()
	err := e.CatalogPreflightLocked(changes)
	e.Mu.Unlock()
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("late ordinary default failed to fence DROP", err)
	}
	view := e.CatalogRead()
	d := view.MustRelation(roleTableRef(t, e, "t").Address)
	bad := *d
	bad.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: d.ColumnMeta.Defaults[1]}
	definition := bad.ColumnMeta.Defaults[1]
	definition.Dependencies = append([]ids.ObjectRef(nil), definition.Dependencies...)
	for i, ref := range definition.Dependencies {
		if ref.Address == target {
			definition.Dependencies = append(definition.Dependencies[:i], definition.Dependencies[i+1:]...)
			break
		}
	}
	bad.ColumnMeta.Defaults[1] = definition
	if err := (core.RelationProvider).Validate(view, &bad); err == nil {
		t.Fatal("untracked default edge admitted")
	}
	if _, err := stored.DefaultSQL(context.Background(), view, d.ColumnMeta.Defaults[1].Expression); err != nil {
		t.Fatal("source expression mutated", err)
	}
}
