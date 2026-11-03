package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const StatGSSAPIOID ids.OID = 12257

// GSSAPISession is a detached, currently authorized real client identity.
// Core classifies raw transaction membership with execution-time privileges.
// No native connection has performed GSS authentication or encryption.
type GSSAPISession struct {
	PID int32
}

var statGSSAPIColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"gss_authenticated", sqltype.Bool, true, 0, 0, -1},
	{"principal", sqltype.Text, true, 0, 0, -1},
	{"encrypted", sqltype.Bool, true, 0, 0, -1},
	{"credentials_delegated", sqltype.Bool, true, 0, 0, -1},
}

func captureStatGSSAPI(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Source == nil || in.Ref.Address != in.Source.Address() ||
		in.Catalog.Get(in.Ref.Address) != in.Source || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatGSSAPIOID || len(in.Source.Columns) != len(statGSSAPIColumns) {
		return nil, fmt.Errorf("stuntgres: invalid GSSAPI source capture")
	}
	columns := in.Source.Columns
	for i, column := range statGSSAPIColumns {
		owned, ok := in.Catalog.LookupType(column.Type)
		if !ok || columns[i].Usage.Type != owned || columns[i].Name != column.Name {
			return nil, fmt.Errorf("stuntgres: invalid GSSAPI source shape")
		}
	}
	rows := make([][]sqltype.Value, 0, len(in.GSSAPISessions))
	for _, client := range in.GSSAPISessions {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if client.PID <= 0 {
			return nil, fmt.Errorf("stuntgres: session identity is not a positive int4")
		}
		pid, err := sqltype.Integer(columns[0].Usage.Type, int64(client.PID))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{
			pid,
			sqltype.Boolean(columns[1].Usage.Type, false),
			sqltype.Null(columns[2].Usage.Type),
			sqltype.Boolean(columns[3].Usage.Type, false),
			sqltype.Boolean(columns[4].Usage.Type, false),
		})
	}
	return rows, in.Context.Err()
}

var statGSSAPIViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	inputs := [...]string{"pid", "gss_auth", "gss_princ", "gss_enc", "gss_creds_delegated"}
	for i, column := range statGSSAPIColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier("s"), viewText("."), viewIdentifier(inputs[i]))
		if inputs[i] != column.Name {
			parts = append(parts, viewText(" AS "), viewIdentifier(column.Name))
		}
	}
	return append(parts, viewText("\n   FROM "), viewRoutine("pg_stat_get_activity", "23"),
		viewText("(NULL::"), viewType("integer"), viewText(") "), viewIdentifier("s"),
		viewText("\n  WHERE ("), viewIdentifier("s"), viewText("."), viewIdentifier("client_port"), viewText(" IS NOT NULL);"))
}()
