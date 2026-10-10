package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// roleClosure preserves breadth-first order for inferred grantors. All callers
// operate on one immutable authority (or one private mutation candidate).
func (r RoleRead) roleClosure(ctx context.Context, actor PrincipalRef, inherit bool) ([]PrincipalRef, error) {
	if _, err := r.Descriptor(actor); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []PrincipalRef{actor}
	if len(r.state.edges) == 0 {
		return result, nil
	}
	seen := map[PrincipalRef]bool{actor: true}
	for at := 0; at < len(result); at++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for index, edge := range r.state.edges {
			if index&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if edge.Member != result[at] || inherit && !edge.Inherit || seen[edge.Role] {
				continue
			}
			if _, err := r.Descriptor(edge.Role); err != nil {
				return nil, err
			}
			seen[edge.Role] = true
			result = append(result, edge.Role)
		}
	}
	return result, nil
}
func directRoleAdmin(edges []Membership, role, member PrincipalRef) bool {
	for _, edge := range edges {
		if edge.Role == role && edge.Member == member && edge.Admin {
			return true
		}
	}
	return false
}
func membershipRole(r RoleRead, state RoleState, spec parser.RoleSpec) (PrincipalRef, error) {
	var ref PrincipalRef
	switch spec.Identity {
	case "current":
		ref = state.CurrentUser
	case "session":
		ref = state.SessionUser
	default:
		d, ok := r.Lookup(spec.Name)
		if !ok {
			return PrincipalRef{}, sqlError("42704", fmt.Sprintf("role \"%s\" does not exist", spec.Name))
		}
		ref = d.Ref
	}
	_, err := r.Descriptor(ref)
	return ref, err
}

type membershipRevokeAction uint8

const (
	membershipRevokeUnchanged membershipRevokeAction = iota
	membershipRevokeDelete
	membershipRevokeAdmin
	membershipRevokeInherit
	membershipRevokeSet
)

// Plan against original rows: matching and alternate ADMIN support must not
// change merely because an earlier recipient already has a pending action.
func planMembershipRevoke(ctx context.Context, edges []Membership, actions []membershipRevokeAction, role PrincipalRef, index int, adminOnly, cascade bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if actions[index] == membershipRevokeDelete || adminOnly && actions[index] == membershipRevokeAdmin {
		return nil
	}
	edge := edges[index]
	if adminOnly {
		if !edge.Admin {
			return nil
		}
		actions[index] = membershipRevokeAdmin
	} else {
		actions[index] = membershipRevokeDelete
		if !edge.Admin {
			return nil
		}
	}
	for i, other := range edges {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if other.Role == role && other.Member == edge.Member && other.Admin && actions[i] == membershipRevokeUnchanged {
			return nil
		}
	}
	for i, other := range edges {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if other.Role != role || other.Grantor != edge.Member || actions[i] == membershipRevokeDelete {
			continue
		}
		if !cascade {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}
		}
		if err := planMembershipRevoke(ctx, edges, actions, role, i, false, true); err != nil {
			return err
		}
	}
	return nil
}

func membershipGrantor(ctx context.Context, r RoleRead, state RoleState, actor RoleDescriptor, target PrincipalRef, explicit *PrincipalRef, revoke bool) (PrincipalRef, error) {
	bootstrap := r.state.names["postgres"]
	if explicit == nil {
		if actor.Superuser {
			return bootstrap, nil
		}
		roles, err := r.roleClosure(ctx, actor.Ref, true)
		if err != nil {
			return PrincipalRef{}, err
		}
		for _, ref := range roles {
			if directRoleAdmin(r.state.edges, target, ref) {
				return ref, nil
			}
		}
		return PrincipalRef{}, sqlError("XX000", "no possible grantors")
	}
	grantor := *explicit
	g, err := r.Descriptor(grantor)
	if err != nil {
		return PrincipalRef{}, err
	}
	allowed, err := r.inheritsRole(actor.Ref, grantor)
	if err != nil {
		return PrincipalRef{}, err
	}
	verb := "grant privileges as"
	detail := fmt.Sprintf("Only roles with privileges of role \"%s\" may grant privileges as this role.", g.Name)
	if revoke {
		verb = "revoke privileges granted by"
		detail = fmt.Sprintf("Only roles with privileges of role \"%s\" may revoke privileges granted by this role.", g.Name)
	}
	if !allowed {
		return PrincipalRef{}, roleDiagnostic("42501", fmt.Sprintf("permission denied to %s role \"%s\"", verb, g.Name), detail)
	}
	if !revoke && grantor != bootstrap && !directRoleAdmin(r.state.edges, target, grantor) {
		role, _ := r.Descriptor(target)
		return PrincipalRef{}, roleDiagnostic("42501", fmt.Sprintf("permission denied to grant privileges as role \"%s\"", g.Name), fmt.Sprintf("The grantor must have the ADMIN option on role \"%s\".", role.Name))
	}
	return grantor, nil
}

func membershipAuthority(ctx context.Context, r RoleRead, actor RoleDescriptor, target PrincipalRef, revoke, alias bool) error {
	d, err := r.Descriptor(target)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return nil
	}
	verb := "grant"
	if revoke {
		verb = "revoke"
	}
	message := fmt.Sprintf("permission denied to %s role \"%s\"", verb, d.Name)
	detail := fmt.Sprintf("Only roles with the ADMIN option on role \"%s\" may %s this role.", d.Name, verb)
	if alias {
		message = "permission denied to alter role"
		detail = fmt.Sprintf("Only roles with the ADMIN option on role \"%s\" may add or drop members.", d.Name)
	}
	if d.Superuser {
		detail = fmt.Sprintf("Only roles with the SUPERUSER attribute may %s roles with the SUPERUSER attribute.", verb)
		if alias {
			detail = "Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute."
		}
		return roleDiagnostic("42501", message, detail)
	}
	roles, err := r.roleClosure(ctx, actor.Ref, false)
	if err != nil {
		return err
	}
	for _, ref := range roles {
		if directRoleAdmin(r.state.edges, target, ref) {
			return nil
		}
	}
	return roleDiagnostic("42501", message, detail)
}

// planMembership owns its edge slice. No partially checked candidate is ever
// published, while PostgreSQL-compatible notices can precede a later error.
func planMembership(op *Operation, r RoleRead, state RoleState, s *parser.RoleMembership, owned bool) ([]Membership, error) {
	ctx := op.Context()
	actor, err := r.Descriptor(state.CurrentUser)
	if err != nil {
		return nil, err
	}
	var values, specified [3]bool
	for _, option := range s.Options {
		index := 0
		switch option.Name {
		case "admin":
			index = 0
		case "inherit":
			index = 1
		case "set":
			index = 2
		default:
			return nil, insertError("42601", fmt.Sprintf("unrecognized role option \"%s\"", option.Name), option.Pos)
		}
		values[index] = option.Value
		specified[index] = true
	}
	var explicit *PrincipalRef
	if s.Grantor != nil {
		ref, err := membershipRole(r, state, *s.Grantor)
		if err != nil {
			return nil, err
		}
		explicit = &ref
	}
	members := make([]PrincipalRef, len(s.Members))
	for i, spec := range s.Members {
		members[i], err = membershipRole(r, state, spec)
		if err != nil {
			return nil, err
		}
	}
	edges := r.state.edges
	var actions, applied []membershipRevokeAction
	revokeChanged := false
	if s.Revoke {
		// DelRoleMems has no command-counter increment: every target sees
		// entry authority and rows, with a fresh per-target action plan.
		actions = make([]membershipRevokeAction, len(edges))
		applied = make([]membershipRevokeAction, len(edges))
	} else if !owned {
		edges = slices.Clone(edges)
	}
	for _, spec := range s.Targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		targetRead := r
		if !s.Revoke {
			// AddRoleMems exposes preceding GRANT changes to the next target.
			candidate := *r.state
			candidate.edges = edges
			targetRead = RoleRead{r.owner, &candidate}
		}
		role, err := membershipRole(targetRead, state, spec)
		if err != nil {
			return nil, err
		}
		if err := membershipAuthority(ctx, targetRead, actor, role, s.Revoke, s.Alias); err != nil {
			return nil, err
		}
		grantor, err := membershipGrantor(ctx, targetRead, state, actor, role, explicit, s.Revoke)
		if err != nil {
			return nil, err
		}
		roleName := r.state.roles[role].Name
		grantorName := r.state.roles[grantor].Name
		if !s.Revoke {
			ancestors, err := targetRead.roleClosure(ctx, role, false)
			if err != nil {
				return nil, err
			}
			// Validate every recipient before ADMIN support or any notices.
			for _, member := range members {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if slices.Contains(ancestors, member) {
					return nil, sqlError("0LP01", fmt.Sprintf("role \"%s\" is a member of role \"%s\"", roleName, r.state.roles[member].Name))
				}
			}
		}
		if !s.Revoke && values[0] && grantor != r.state.names["postgres"] {
			if slices.Contains(members, r.state.names["postgres"]) {
				return nil, sqlError("0LP01", "ADMIN option cannot be granted back to your own grantor")
			}
			actions := make([]membershipRevokeAction, len(edges))
			for i, edge := range edges {
				if edge.Role == role && slices.Contains(members, edge.Member) {
					if err := planMembershipRevoke(ctx, edges, actions, role, i, false, true); err != nil {
						return nil, err
					}
				}
			}
			supported := false
			for i, edge := range edges {
				if edge.Role == role && edge.Member == grantor && edge.Admin && actions[i] == membershipRevokeUnchanged {
					supported = true
					break
				}
			}
			if !supported {
				return nil, sqlError("0LP01", "ADMIN option cannot be granted back to your own grantor")
			}
		}
		// Duplicate recipients share this target's action plan, but later
		// targets independently match and plan against the same original rows.
		if s.Revoke {
			clear(actions)
		}
		for _, member := range members {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			memberName := r.state.roles[member].Name
			found := -1
			for i, edge := range edges {
				if edge.Role == role && edge.Member == member && edge.Grantor == grantor {
					found = i
					break
				}
			}
			if s.Revoke {
				if found < 0 {
					op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: fmt.Sprintf("role \"%s\" has not been granted membership in role \"%s\" by role \"%s\"", memberName, roleName, grantorName)})
					continue
				}
				switch s.RevokeOption {
				case "inherit":
					actions[found] = membershipRevokeInherit
				case "set":
					actions[found] = membershipRevokeSet
				default:
					if err := planMembershipRevoke(ctx, edges, actions, role, found, s.RevokeOption == "admin", s.Cascade); err != nil {
						return nil, err
					}
				}
				continue
			}
			edge := Membership{Role: role, Member: member, Grantor: grantor, Inherit: true, Set: true}
			if found >= 0 {
				edge = edges[found]
			}
			if specified[0] {
				edge.Admin = values[0]
			}
			if specified[1] {
				edge.Inherit = values[1]
			}
			if specified[2] {
				edge.Set = values[2]
			}
			if found >= 0 {
				if edges[found] == edge {
					op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("role \"%s\" has already been granted membership in role \"%s\" by role \"%s\"", memberName, roleName, grantorName)})
				}
				edges[found] = edge
			} else {
				edges = append(edges, edge)
			}
		}
		if s.Revoke {
			for i, action := range actions {
				if action == membershipRevokeUnchanged {
					continue
				}
				if applied[i] != membershipRevokeUnchanged {
					return nil, sqlError("XX000", "tuple already updated by self")
				}
				applied[i] = action
				revokeChanged = true
			}
		}
	}
	if s.Revoke && revokeChanged {
		if !owned {
			edges = slices.Clone(edges)
		}
		n := 0
		for i, edge := range edges {
			switch applied[i] {
			case membershipRevokeDelete:
				continue
			case membershipRevokeAdmin:
				edge.Admin = false
			case membershipRevokeInherit:
				edge.Inherit = false
			case membershipRevokeSet:
				edge.Set = false
			}
			edges[n] = edge
			n++
		}
		edges = edges[:n]
	}
	return edges, nil
}

func (e *Engine) membershipLocked(op *Operation, v AuthorizationView, s *parser.RoleMembership) (Result, error) {
	edges, err := planMembership(op, v.roles, v.state, s, false)
	if err != nil {
		return Result{}, err
	}
	if slices.Equal(edges, e.roles.edges) {
		return Result{}, op.Context().Err()
	}
	if e.roles.revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	n := advanceRoleRevision(e.roles)
	n.edges = edges
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	e.roles = n
	return Result{}, nil
}
