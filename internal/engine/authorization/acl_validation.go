package authorization

import "fmt"

func ValidateACL(r Read, a ACLValue, mask Privileges) error {
	if !a.Explicit && len(a.Entries) != 0 {
		return fmt.Errorf("stuntgres: entries in absent ACL")
	}
	for _, x := range a.Entries {
		if _, err := r.Descriptor(x.Grantor); err != nil {
			return err
		}
		if x.Grantee.Public {
			if x.Grantee.Role != (PrincipalRef{}) {
				return fmt.Errorf("stuntgres: PUBLIC principal")
			}
			if x.GrantOptions != 0 {
				return fmt.Errorf("stuntgres: PUBLIC grant options")
			}
		} else if _, err := r.Descriptor(x.Grantee.Role); err != nil {
			return err
		}
		if x.Privileges & ^mask != 0 || x.GrantOptions & ^x.Privileges != 0 {
			return fmt.Errorf("stuntgres: invalid ACL mask")
		}
	}
	return nil
}
