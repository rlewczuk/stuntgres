package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestSerialIncomingDefaultDependencyFence(t *testing.T) {
	e := New()
	run(t,e,`CREATE SEQUENCE q`)
	base := e.CatalogRead().state
	target := roleTableRef(t,e,"q").Address
	run(t,e,`CREATE TABLE t(id INT DEFAULT nextval('q'))`)
	changes := newCatalogChanges()
	changes.dependencies = append(changes.dependencies,catalogDependencyObservation{base,[]ids.ObjectAddress{target}})
	e.mu.Lock()
	err := e.catalogPreflightLocked(changes)
	e.mu.Unlock()
	var conflict CatalogConflict
	if !errors.As(err,&conflict) { t.Fatal("late ordinary default failed to fence DROP",err) }
	view := e.CatalogRead()
	d := view.MustRelation(roleTableRef(t,e,"t").Address)
	bad := *d
	bad.Defaults = map[int32]columnDefault{1:d.Defaults[1]}
	definition := bad.Defaults[1]
	definition.dependencies = append([]ids.ObjectRef(nil),definition.dependencies...)
	for i,ref := range definition.dependencies { if ref.Address == target { definition.dependencies = append(definition.dependencies[:i],definition.dependencies[i+1:]...); break } }
	bad.Defaults[1] = definition
	if err := (relationProvider{}).Validate(view,&bad); err == nil { t.Fatal("untracked default edge admitted") }
	if _,err := defaultSQL(context.Background(),view,d.Defaults[1].expression); err != nil { t.Fatal("source expression mutated",err) }
}
