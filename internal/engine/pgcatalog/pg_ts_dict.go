package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// capturePGTSDict admits the dictionary source before the shared extension
// projector reads it, including when there are no installed dictionaries.
func capturePGTSDict(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source, ref := in.Catalog, in.Source, in.Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3600}
	if view.Owner() == nil || view.State() == nil || in.Owner != view.Owner() || source == nil || source.Address() != address || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address != address || view.Get(address) != source || len(source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: foreign dictionary source authority")
	}
	names := [...]string{"oid", "dictname", "dictnamespace", "dictowner", "dicttemplate", "dictinitoption"}
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Oid, sqltype.Text} {
		if source.Columns[i].Name != names[i] {
			return nil, fmt.Errorf("stuntgres: invalid dictionary source column")
		}
		typ, ok := view.LookupType(id)
		if !ok || typ != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign dictionary source type")
		}
	}
	return captureExtensionCatalog(in)
}
