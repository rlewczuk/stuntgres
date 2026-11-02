package lock

import (
	"errors"

	"stuntgres/internal/engine/ids"
)

// TupleResource names ordinary rows by RowID and repaired lineages by their
// exact physical repair anchor. The anchor is not a newly allocated identity.
func TupleResource(ref ids.ObjectRef, row ids.RowID, anchor ids.VersionID, stamp uint64) (Resource, error) {
	resource := Resource{ObjectRef: ref, Row: row}
	if row == 0 || (anchor == 0) != (stamp == 0) {
		return Resource{}, errors.New("stuntgres: invalid tuple lock identity")
	}
	if anchor != 0 {
		resource.Kind = RepairedTupleResource
		resource.Row = ids.RowID(anchor)
		resource.High, resource.Low = uint32(stamp>>32), uint32(stamp)
	}
	if !validTupleResource(resource) {
		return Resource{}, errors.New("stuntgres: invalid tuple lock resource")
	}
	return resource, nil
}

func validTupleResource(resource Resource) bool {
	kind := resource.Kind == OrdinaryResource && resource.High == 0 && resource.Low == 0 ||
		resource.Kind == RepairedTupleResource && (resource.High != 0 || resource.Low != 0)
	return kind && resource.Domain() != nil && resource.Epoch != 0 &&
		resource.Address.ClassID == ids.RelationClassID && resource.Address.ObjectID != 0 && resource.Address.SubID == 0 &&
		resource.Database == nil && !resource.RoleTuple && resource.DefaultClass == 0 &&
		resource.Name == (ids.NameKey{}) && resource.Row != 0 && resource.key == nil && resource.completion == nil
}
