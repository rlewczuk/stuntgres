package partition

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Host is what the CREATE ... PARTITION OF and PARTITION BY decisions need
// from the engine for one statement: the CREATE TABLE host (and through it
// the index and shared DDL hosts) and the partition key binding. The engine
// builds binders; this package never does.
//
// Every method runs with Engine.mu held, never takes or releases it, and
// never waits.
type Host interface {
	table.Host

	// BindKeyExpression binds one partition key expression of relation with
	// the "partition key expression" consumer and no parameters, and
	// preflights it. It returns the key with Definition, Execution and
	// Attributes set, and the relation's column slots the expression uses.
	// The volatility, constant and generated-column checks are the caller's.
	BindKeyExpression(auth ddl.Authority, relation *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (*catalog.DerivedIndexKey, []int, error)
}

// BoundBinder binds the bound datums of one pending definition. It is
// self-locking: callers hold nothing, and BindBound takes Engine.mu for one
// window (the authorization recheck, the binder and the storage coercion),
// releases it and never waits. Callers evaluate the result after it returns.
type BoundBinder interface {
	BindBound(datum parser.PartitionDatum, key catalog.PartitionKeyEntry) (expr.Expr, error)
}
