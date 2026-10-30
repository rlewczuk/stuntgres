package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// The bounded canonical replay form must resolve one selected-database
// subscription before its typed function can mutate even the isolated engine.
func beforeRestoreReplicationOrigin(r *restoreRun, _ *session.Operation, stmt parser.Statement) (bool, error) {
	selected, ok := stmt.(*parser.Select)
	if !ok || !control.RestoresReplicationOrigin(selected) || selected.From == nil {
		return false, nil
	}
	predicate := selected.Where.(parser.OperatorExpr)
	name := predicate.Args[1].(parser.LiteralExpr).Value.Text
	read := r.stage.CatalogRead()
	address := read.NameAddress(catalog.SubscriptionNameKey(read.Owner().DatabaseAddress(), name))
	subscription, ok := read.Get(address).(*catalog.Subscription)
	if !ok || subscription.Database != read.Owner().DatabaseAddress() {
		return false, core.SQLError("0A000", "restore replication origin state requires exactly one selected subscription")
	}
	count := 0
	for _, d := range read.Subscriptions() {
		if d.Name() == name {
			count++
		}
	}
	if count != 1 {
		return false, core.SQLError("0A000", "restore replication origin state requires exactly one selected subscription")
	}
	return false, nil
}
