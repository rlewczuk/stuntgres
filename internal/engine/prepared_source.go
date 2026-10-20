package engine

import (
	"stuntgres/internal/sqltype"
)

// Engine.mu protects the short registry snapshot. Rows own scalar datums and
// never retain entries or session attachments.
func capturePreparedSource(in catalogSourceInput) ([][]sqltype.Value, error) {
	source := in.Source
	rows := make([][]sqltype.Value, 0, in.Runtime.PreparedCount())
	err := in.Runtime.VisitPreparedTransactions(in.Context, func(fact catalogPreparedFact) error {
		xid, err := sqltype.XIDValue(source.Columns[0].Usage.Type, fact.XID)
		if err != nil {
			return err
		}
		prepared, err := sqltype.TimestampFromTime(source.Columns[2].Usage.Type, fact.Prepared)
		if err != nil {
			return err
		}
		owner := sqltype.Null(source.Columns[3].Usage.Type)
		if fact.OwnerPresent {
			owner = sqltype.String(source.Columns[3].Usage.Type, fact.Owner)
		}
		rows = append(rows, []sqltype.Value{xid, sqltype.String(source.Columns[1].Usage.Type, fact.GID), prepared, owner, sqltype.String(source.Columns[4].Usage.Type, fact.Database)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}
