package fixture

func ConversionCreate(name string, def bool) string {
	marker := ""
	if def {
		marker = "DEFAULT "
	}
	return "CREATE " + marker + "CONVERSION " + name + " FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1"
}
