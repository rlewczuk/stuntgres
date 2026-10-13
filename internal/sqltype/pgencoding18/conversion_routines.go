package pgencoding18

// ConversionRoutine is the release conversion provider association, not a SQL
// default lookup. SQL defaults belong to the engine's statement-owned catalog.
func ConversionRoutine(source, destination int) string {
	if source < 0 || source >= len(conversionRoutines) || destination < 0 || destination >= len(conversionRoutines) {
		return ""
	}
	return conversionRoutines[source][destination]
}
