package settings

// PgTrgmDefinitions borrows the immutable bounded released library definitions.
// They are not registry builtins: only a physical session that loads pg_trgm
// admits them. The C library initializes doubles with float literals, not their
// SHOW text. Callers must not mutate the slice or its definitions.
func PgTrgmDefinitions() []Definition { return pgTrgmDefinitions[:] }

var pgTrgmDefinitions = [...]Definition{
	pgTrgmDefinition("pg_trgm.similarity_threshold", "%", float64(float32(0.3))),
	pgTrgmDefinition("pg_trgm.word_similarity_threshold", "<%", float64(float32(0.6))),
	pgTrgmDefinition("pg_trgm.strict_word_similarity_threshold", "<<%", float64(float32(0.5))),
}

func pgTrgmDefinition(name, operator string, boot float64) Definition {
	return Definition{
		Name: name, Description: "Sets the threshold used by the " + operator + " operator.",
		Category: "Customized Options", ExtraDescription: "Valid range is 0.0 .. 1.0.",
		Type: Floating, Access: Session, FloatMin: 0, FloatMax: 1,
		BootDefault: realText(boot), bootValue: Value{Number: boot},
	}
}
