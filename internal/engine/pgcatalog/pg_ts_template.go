package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// capturePGTSTemplate admits installed template metadata before projection,
// including when the captured catalog contains no executable templates.
func capturePGTSTemplate(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source, ref := in.Catalog, in.Source, in.Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3764}
	if view.Owner() == nil || view.State() == nil || in.Owner != view.Owner() || source == nil || source.Address() != address || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address != address || view.Get(address) != source || len(source.Columns) != 5 {
		return nil, fmt.Errorf("stuntgres: foreign template source authority")
	}
	names := [...]string{"oid", "tmplname", "tmplnamespace", "tmplinit", "tmpllexize"}
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Regproc, sqltype.Regproc} {
		if source.Columns[i].Name != names[i] {
			return nil, fmt.Errorf("stuntgres: invalid template source column")
		}
		typ, ok := view.LookupType(id)
		if !ok || typ != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign template source type")
		}
	}
	return captureExtensionCatalog(in)
}
