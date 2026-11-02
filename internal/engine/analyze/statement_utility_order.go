package analyze

import "stuntgres/internal/parser"

// REL_18_6 gram.y builds CreateFunctionStmt.options by lappend. The native
// parser separates LANGUAGE/AS from attributes, but retains their source order.
// Merge those existing coordinates without retaining/hashing source text or
// allocating a second option list.
func (j *StatementJumble) utilityFunctionOptions(n *parser.CreateFunction, anchor *int) {
	language, body := n.Language.Pos.Character > 0, n.BodyPos.Character > 0
	count := len(n.Attributes)
	if language {
		count++
	}
	if body {
		count++
	}
	j.number(uint64(count))
	attribute := 0
	for attribute < len(n.Attributes) || language || body {
		kind := 0
		var position int
		if attribute < len(n.Attributes) {
			position = n.Attributes[attribute].Pos.Offset
		} else {
			kind = -1
		}
		if language && (kind == -1 || n.Language.Pos.Offset < position) {
			kind = 1
			position = n.Language.Pos.Offset
		}
		if body && (kind == -1 || n.BodyPos.Offset < position) {
			kind = 2
		}
		switch kind {
		case 0:
			j.utilityRoutineAttribute(&n.Attributes[attribute], anchor)
			attribute++
		case 1:
			j.text("DefElem")
			j.text("language")
			j.text(n.Language.Text)
			language = false
		case 2:
			j.text("DefElem")
			j.text("as")
			if n.SymbolSpecified {
				j.number(2)
			} else {
				j.number(1)
			}
			j.text(n.Body)
			if n.SymbolSpecified {
				j.text(n.Symbol)
			}
			body = false
		}
	}
}

func (j *StatementJumble) utilityRoutineAttribute(n *parser.RoutineAttribute, anchor *int) {
	j.utilityPosition(n.Pos, anchor)
	j.text("DefElem")
	switch n.Kind {
	case "strict", "leakproof":
		j.text(n.Kind)
		j.flag(n.Value != "false")
	case "volatile", "stable", "immutable":
		j.text("volatility")
		j.text(n.Kind)
	case "security":
		j.text("security")
		j.flag(n.Value == "definer")
	case "window":
		j.text("window")
		j.flag(true)
	case "set":
		j.text("set")
		j.utilitySyntax(n.Setting, anchor)
	case "transform":
		j.text("transform")
		j.utilitySyntax(n.Transform, anchor)
	default:
		j.text(n.Kind)
		j.text(n.Value)
	}
}

// Role options are another released DefElem list. The fixed-size scalar
// inventory has ten slots; initial membership clauses already retain their
// own ordered slice. Sort only populated scalar indices on stack, then merge.
func (j *StatementJumble) utilityRoleOptions(n *parser.RoleOptions, anchor *int) {
	var order [10]int
	count := 0
	for slot := range len(order) {
		if n.Present&(parser.RoleField(1)<<slot) == 0 {
			continue
		}
		at := count
		for at > 0 && n.Positions[order[at-1]].Offset > n.Positions[slot].Offset {
			order[at] = order[at-1]
			at--
		}
		order[at] = slot
		count++
	}
	j.number(uint64(count + len(n.InitialMemberships)))
	scalar, membership := 0, 0
	for scalar < count || membership < len(n.InitialMemberships) {
		if membership < len(n.InitialMemberships) && (scalar == count || n.InitialMemberships[membership].Pos.Offset < n.Positions[order[scalar]].Offset) {
			j.utilitySyntax(n.InitialMemberships[membership], anchor)
			membership++
			continue
		}
		j.text("DefElem")
		field := parser.RoleField(1) << order[scalar]
		switch field {
		case parser.RoleSuperuser:
			j.text("superuser")
			j.flag(n.Superuser)
		case parser.RoleCreateDB:
			j.text("createdb")
			j.flag(n.CreateDB)
		case parser.RoleCreateRole:
			j.text("createrole")
			j.flag(n.CreateRole)
		case parser.RoleLogin:
			j.text("canlogin")
			j.flag(n.Login)
		case parser.RoleInherit:
			j.text("inherit")
			j.flag(n.Inherit)
		case parser.RoleReplication:
			j.text("isreplication")
			j.flag(n.Replication)
		case parser.RoleBypassRLS:
			j.text("bypassrls")
			j.flag(n.BypassRLS)
		case parser.RoleConnectionLimit:
			j.text("connectionlimit")
			j.number(uint64(n.ConnectionLimit))
		case parser.RolePassword:
			j.text("password")
			if n.PasswordNull {
				j.number(0)
			} else {
				j.text(n.Password)
			}
		case parser.RoleValidUntil:
			j.text("validUntil")
			j.text(n.ValidUntil)
		}
		scalar++
	}
}

// TableLikeClause.options is a bitmask, unlike the ordered option lists above.
// These masks are the released parsenodes.h CREATE_TABLE_LIKE_* definitions;
// ALL includes the reserved bits of PG_INT32_MAX too.
func (j *StatementJumble) utilityLikeOptions(options []parser.LikeOption) {
	var selected uint32
	for _, option := range options {
		var mask uint32
		switch option.Name {
		case "comments":
			mask = 1 << 0
		case "compression":
			mask = 1 << 1
		case "constraints":
			mask = 1 << 2
		case "defaults":
			mask = 1 << 3
		case "generated":
			mask = 1 << 4
		case "identity":
			mask = 1 << 5
		case "indexes":
			mask = 1 << 6
		case "statistics":
			mask = 1 << 7
		case "storage":
			mask = 1 << 8
		case "all":
			mask = 1<<31 - 1
		default:
			panic("stuntgres: unclassified raw LIKE option")
		}
		if option.Including {
			selected |= mask
		} else {
			selected &^= mask
		}
	}
	j.number(uint64(selected))
}
