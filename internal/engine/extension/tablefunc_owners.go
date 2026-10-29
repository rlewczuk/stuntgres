package extension

func tablefuncTypeRole(slot int) string {
	slot -= tablefuncCrosstabStart
	if slot < 1 || slot > 6 {
		return ""
	}
	return tablefuncTypeRoles[slot]
}
