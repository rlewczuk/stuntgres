package extension

func tablefuncTypeRole(slot int) string {
	slot -= tablefuncCrosstabStart
	if slot < 1 || slot > 6 {
		return ""
	}
	return tablefuncTypeRoles[slot]
}

func validTablefuncOwnerMember(member MemberOwner) bool {
	if role := tablefuncTypeRole(member.Slot); role != "" {
		return member.Kind == "type" && member.Name == role && member.Signature == "" && member.ACL == nil
	}
	signature, ok := ownerProcedureSignature("tablefunc", "1.0", member.Slot)
	return ok && member.Kind == "procedure" && member.Signature == signature
}

func validTablefuncOwnerRoles(record OwnerRecord) bool {
	if record.Version != "1.0" || len(record.Members) != tablefuncMemberCount {
		return false
	}
	var slots [tablefuncMemberCount]*MemberOwner
	for i := range record.Members {
		member := &record.Members[i]
		if member.Slot < 0 || member.Slot >= len(slots) || slots[member.Slot] != nil || !validTablefuncOwnerMember(*member) {
			return false
		}
		slots[member.Slot] = member
	}
	for slot := tablefuncCrosstabStart + 1; slot <= tablefuncCrosstabStart+5; slot += 2 {
		if slots[slot].Owner != slots[slot+1].Owner || slots[slot].Schema != slots[slot+1].Schema {
			return false
		}
	}
	return true
}
