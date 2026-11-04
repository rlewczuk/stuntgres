package catalog

import "fmt"

func ValidateSecurityLabels(view Read) error {
	for key, record := range view.State().SecurityLabels() {
		object := key.Object
		object.SubID = 0
		d := view.Get(object)
		policy := view.Owner().Sepgsql
		if key.Provider != "selinux" || record == nil || policy == nil || record.PolicyID != policy.ID() || record.PolicyVersion != policy.Version() || !policy.Valid(record.Label) || d == nil || SepgsqlClass(d, key.Object.SubID) == "" {
			return fmt.Errorf("stuntgres: invalid security label authority")
		}
	}
	return nil
}
