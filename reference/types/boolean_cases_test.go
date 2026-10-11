package reference_test

// These expectations are literal fixtures, independent of the type-input formatter.
var booleanInvalidInputs = []struct{ source, value, message string }{
	{"'o'", "o", `invalid input syntax for type boolean: "o"`},
	{"'O'", "O", `invalid input syntax for type boolean: "O"`},
	{"'truth'", "truth", `invalid input syntax for type boolean: "truth"`},
	{"''", "", `invalid input syntax for type boolean: ""`},
	{"'   '", "   ", `invalid input syntax for type boolean: "   "`},
	{"' o '", " o ", `invalid input syntax for type boolean: " o "`},
	{"'2'", "2", `invalid input syntax for type boolean: "2"`},
	{"'é'", "é", `invalid input syntax for type boolean: "é"`},
	{`'a"b'`, `a"b`, `invalid input syntax for type boolean: "a"b"`},
	{`'a\b'`, `a\b`, `invalid input syntax for type boolean: "a\b"`},
	{"'a\nb'", "a\nb", "invalid input syntax for type boolean: \"a\nb\""},
}
