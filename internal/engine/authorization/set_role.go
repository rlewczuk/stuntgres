package authorization

import "fmt"

// CanSetRole follows only SET-authorized membership edges. Valid descriptors
// are required even for self/superuser shortcuts; stale identities fail closed.
func (r Read) CanSetRole(session, target PrincipalRef) (bool, error) {
	actor, err := r.Descriptor(session)
	if err != nil {
		return false, err
	}
	if _, err := r.Descriptor(target); err != nil {
		return false, err
	}
	if session == target || actor.Superuser {
		return true, nil
	}
	if len(r.state.edges) == 0 && r.implicitDatabaseOwner(session) == (PrincipalRef{}) {
		return false, nil
	}
	visited := map[PrincipalRef]bool{session: true}
	queue := []PrincipalRef{session}
	for len(queue) != 0 {
		member := queue[0]
		queue = queue[1:]
		for _, edge := range r.state.edges {
			if !edge.Set || edge.Member != member || visited[edge.Role] {
				continue
			}
			if _, err := r.Descriptor(edge.Role); err != nil {
				return false, err
			}
			if edge.Role == target {
				return true, nil
			}
			visited[edge.Role] = true
			queue = append(queue, edge.Role)
		}
		if next := r.implicitDatabaseOwner(member); next != (PrincipalRef{}) && !visited[next] {
			if _, err := r.Descriptor(next); err != nil {
				return false, err
			}
			if next == target {
				return true, nil
			}
			visited[next] = true
			queue = append(queue, next)
		}
	}
	return false, nil
}

// ValidateInitialRole admits a session's initial or reset role only when the
// authenticated role may SET it.
func ValidateInitialRole(r Read, authenticated, target PrincipalRef) error {
	allowed, err := r.CanSetRole(authenticated, target)
	if err != nil {
		return err
	}
	if !allowed {
		role, _ := r.Descriptor(target)
		return sqlError("42501", fmt.Sprintf("permission denied to set role \"%s\"", role.Name))
	}
	return nil
}
