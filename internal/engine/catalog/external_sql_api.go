package catalog

import "context"

// ExternalSQLProviderDefinition is a host capability, not an authenticated remote
// connection. Its immutable registration identity is local to one Driver.
type ExternalSQLProviderDefinition struct {
	Name, Version string
	Provider      ExternalSQLProvider
}

// ExternalSQLBindings is the immutable Driver-selected stock transport set.
// It is copied through private construction, never selected by SQL names.
type ExternalSQLBindings struct {
	Dblink, PostgresFDW *ExternalSQLProviderDefinition
}

type ExternalSQLProvider interface {
	Open(context.Context, ExternalSQLConnectRequest) (ExternalSQLConnection, error)
}

// ExternalSQLSession belongs to the executing physical session. Planning and
// Describe never receive it. Open requires the callback context or a derivative
// and authenticates before publishing a connection; completed openers are inert.
type ExternalSQLSession interface {
	Open(context.Context, ExternalSQLConnectRequest) (ExternalSQLConnection, error)
}

// ConninfoPresent selects original text, including explicit empty; otherwise
// structured options apply. The forms are mutually exclusive. Caller identities
// come from admitted foreign requests or installed routine environments, never
// callback-supplied identity. MappingPresent distinguishes an empty mapping.
// This record contains credentials and must not be logged or formatted.
type ExternalSQLConnectRequest struct {
	Conninfo                       string
	ConninfoPresent                bool
	ServerOptions, MappingOptions  []ForeignOption
	MappingPresent                 bool
	Role                           string
	RoleOID, ServerOID, MappingOID uint32
}

// Execute returns owned PostgreSQL output-format text, never borrowed transport
// buffers or driver-decoded bytea. Cancel may run concurrently with Execute; nil
// means cancellation was dispatched, not that a query was canceled. All other
// operations serialize. A nonnil Open result transfers Close even with an error.
type ExternalSQLConnection interface {
	Execute(context.Context, string) ([]ExternalSQLResult, error)
	Cancel(context.Context) error
	DrainNotifications(context.Context) ([]ExternalSQLNotification, error)
	Info() ExternalSQLConnectionInfo
	Close(context.Context) error
}

type ExternalSQLColumn struct {
	Name         string
	TypeOID      uint32
	TypeModifier int32
	Format       int16
}

type ExternalSQLCell struct {
	Text string
	Null bool
}

// Tuple distinguishes a tuple result (even with zero columns/rows) from a
// command. Cells are flat row-major; RowCount*len(Columns) must equal len(Cells).
type ExternalSQLResult struct {
	Tuple        bool
	Columns      []ExternalSQLColumn
	Cells        []ExternalSQLCell
	RowCount     int
	CommandTag   string
	RowsAffected int64
}

type ExternalSQLConnectionInfo struct {
	BackendPID                      int32
	User, Database, ProviderVersion string
	// Unknown is an honest value; a supplied password does not prove its use.
	CredentialMechanism string
}

type ExternalSQLNotification struct {
	Channel    string
	BackendPID int32
	Payload    string
}

// ExternalSQLUnavailableError is an ordinary capability error, not an invented
// PostgreSQL error response. It intentionally does not implement SQLState.
type ExternalSQLUnavailableError struct{ Capability string }

func (e *ExternalSQLUnavailableError) Error() string {
	return "stuntgres: external SQL capability unavailable: " + e.Capability
}

// ExternalSQLConnectError attests a genuine host transport establishment
// failure. It is not a PostgreSQL response and deliberately has no SQLState.
// Authentic dblink/postgres_fdw stock Open boundaries project their released
// connection diagnostics; raw/generic callers retain the ordinary cause.
type ExternalSQLConnectError struct{ Err error }

func (e *ExternalSQLConnectError) Error() string {
	if e.Err == nil {
		return "stuntgres: external SQL connection failure missing cause"
	}
	return e.Err.Error()
}
func (e *ExternalSQLConnectError) Unwrap() error { return e.Err }

// Formatting a connection request must not disclose conninfo/mapping secrets.
func (ExternalSQLConnectRequest) String() string {
	return "ExternalSQLConnectRequest{credentials redacted}"
}

func (r ExternalSQLConnectRequest) GoString() string { return r.String() }
