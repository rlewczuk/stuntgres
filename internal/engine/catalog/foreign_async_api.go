package catalog

import "context"

// ForeignAsyncScanProvider opts a selected Append path into independent I/O.
// Capability discovery must not acquire scan resources or start requests.
type ForeignAsyncScanProvider interface {
	IsForeignPathAsyncCapable(context.Context, ForeignRequest, ForeignPath) (bool, error)
}

// ForeignAsyncScan starts at most one request at a time. Begin and all callbacks
// other than the request's own I/O execute on the query leader.
type ForeignAsyncScan interface {
	RequestForeignScan(context.Context) (ForeignAsyncRequest, error)
}

// ForeignAsyncRequest owns one result, error or EOF. Ready is stable and closes
// exactly once; pending is not EOF. Complete is called once after Ready and starts
// no further I/O. Returned cells remain stable until Close; the leader imports
// them before closing. Cancel is idempotent and nonblocking. Close is idempotent,
// releases result storage and joins all request workers. A nonnil request returned
// with an error still transfers ownership. Neither requests nor their contexts
// may outlive a successful finite cursor motion.
type ForeignAsyncRequest interface {
	Ready() <-chan struct{}
	Complete(context.Context) (ForeignRow, bool, error)
	Cancel()
	Close(context.Context) error
}
