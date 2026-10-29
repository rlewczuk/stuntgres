package fixture

import (
	"bytes"
	"embed"

	"github.com/testcontainers/testcontainers-go"
)

// thesaurus_sample.ths is PostgreSQL REL_18_6
// src/backend/tsearch/dicts/thesaurus_sample.ths, verbatim.
// Portions Copyright (c) 1996-2026, PostgreSQL Global Development Group.
// Portions Copyright (c) 1994, Regents of the University of California.
// PostgreSQL License: https://www.postgresql.org/about/licence/
// stuntgres_thes_*.ths are project-owned deterministic resources. These copies
// must stay byte-identical to internal/engine/functions/*.ths; the reference
// fixture never imports a production internal package to obtain oracle inputs.
//
//go:embed tsearch_data/*.ths
var thesaurusOracleResources embed.FS

func thesaurusOracleFiles() []testcontainers.ContainerFile {
	entries, err := thesaurusOracleResources.ReadDir("tsearch_data")
	if err != nil {
		panic(err)
	}
	files := make([]testcontainers.ContainerFile, 0, len(entries))
	for _, entry := range entries {
		data, err := thesaurusOracleResources.ReadFile("tsearch_data/" + entry.Name())
		if err != nil {
			panic(err)
		}
		files = append(files, testcontainers.ContainerFile{
			Reader:            bytes.NewReader(data),
			ContainerFilePath: "/usr/share/postgresql/18/tsearch_data/" + entry.Name(),
			FileMode:          0o644,
		})
	}
	return files
}
