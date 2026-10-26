package fts

// DocumentTypes returns the default parser's fixed token-ID ordered inventory.
func DocumentTypes() [23]struct{ Alias, Description string } {
	return [23]struct{ Alias, Description string }{
		{"asciiword", "Word, all ASCII"}, {"word", "Word, all letters"}, {"numword", "Word, letters and digits"},
		{"email", "Email address"}, {"url", "URL"}, {"host", "Host"}, {"sfloat", "Scientific notation"}, {"version", "Version number"},
		{"hword_numpart", "Hyphenated word part, letters and digits"}, {"hword_part", "Hyphenated word part, all letters"}, {"hword_asciipart", "Hyphenated word part, all ASCII"},
		{"blank", "Space symbols"}, {"tag", "XML tag"}, {"protocol", "Protocol head"}, {"numhword", "Hyphenated word, letters and digits"}, {"asciihword", "Hyphenated word, all ASCII"}, {"hword", "Hyphenated word, all letters"},
		{"url_path", "URL path"}, {"file", "File or path name"}, {"float", "Decimal notation"}, {"int", "Signed integer"}, {"uint", "Unsigned integer"}, {"entity", "XML entity"},
	}
}
