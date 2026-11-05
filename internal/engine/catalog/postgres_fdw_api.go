package catalog

import (
	"context"

	"stuntgres/internal/sqltype"
)

// PostgresFDWSession is granted only to the authentic installed stock provider.
// Physical membership and leases remain owned by the existing session owner;
// this does not add a public transport or transaction coordinator.
type PostgresFDWSession interface {
	ExternalSQLSession
	AcquirePostgresFDW(context.Context) (PostgresFDWLease, error)
	// LocalColumnBaseType reads immutable admitted local attribute facts, not
	// remote result metadata. It performs no input or domain constraint checks.
	LocalColumnBaseType(attribute int32) (sqltype.ID, error)
}

type PostgresFDWLease interface {
	Connection() ExternalSQLConnection
	ByteaDescriptor() *sqltype.Descriptor
	Release(context.Context, bool) error
}
