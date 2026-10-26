package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const WaitEventsOID ids.OID = 12351

// Process-initialized definition text, never mutated. These are implemented
// blocking mechanisms, not observations of currently waiting sessions.
var waitEventDefinitions = [...][3]string{
	{"IPC", "SafeSnapshot", "Waiting to obtain a valid snapshot for a READ ONLY DEFERRABLE transaction"},
	{"Lock", "advisory", "Waiting to acquire an advisory user lock"},
	{"Lock", "object", "Waiting to acquire a lock on a non-relation database object"},
	{"Lock", "relation", "Waiting to acquire a lock on a relation"},
	{"Lock", "transactionid", "Waiting for a transaction to finish"},
	{"Lock", "virtualxid", "Waiting to acquire a virtual transaction ID lock"},
	{"Timeout", "PgSleep", "Waiting due to a call to pg_sleep or a sibling function"},
}

func capturePGWaitEvents(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source := in.Catalog, in.Source
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil ||
		source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: WaitEventsOID}) ||
		source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) ||
		in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() ||
		in.Ref.Address != source.Address() || view.Get(in.Ref.Address) != source ||
		len(source.Columns) != 3 || !source.VirtualView {
		return nil, fmt.Errorf("stuntgres: invalid wait-event source authority")
	}
	if in.Roles != (authorization.Read{}) && !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: foreign wait-event role authority")
	}
	text, ok := view.LookupType(sqltype.Text)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing wait-event source type")
	}
	for i, name := range [...]string{"type", "name", "description"} {
		if source.Columns[i].Name != name || source.Columns[i].Usage.Type != text {
			return nil, fmt.Errorf("stuntgres: invalid wait-event source column")
		}
	}
	rows := make([][]sqltype.Value, len(waitEventDefinitions))
	values := make([]sqltype.Value, len(waitEventDefinitions)*3)
	for i := range waitEventDefinitions {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		row := values[i*3 : (i+1)*3 : (i+1)*3]
		for j, value := range waitEventDefinitions[i] {
			row[j] = sqltype.String(source.Columns[j].Usage.Type, value)
		}
		rows[i] = row
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
