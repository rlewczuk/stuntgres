package fixture

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/testcontainers/testcontainers-go/exec"

	"stuntgres"
)

var textSearchFiles []stuntgres.TextSearchFile

// TextSearchDataDirectory is the actual oracle shared-data directory discovered
// with pg_config, including tsearch_data. It is populated before scenarios run.
var TextSearchDataDirectory string

// WithTextSearchFiles installs copied, explicit bytes in the package-owned
// oracle before any scenario initializes a dictionary. Native scenarios register
// the same manifest with NewScenarioWithDriverConfig; no SQL installs files.
func WithTextSearchFiles(files ...stuntgres.TextSearchFile) StartupOption {
	owned := make([]stuntgres.TextSearchFile, len(files))
	for i, file := range files {
		owned[i] = stuntgres.TextSearchFile{Name: file.Name, Data: bytes.Clone(file.Data)}
	}
	return func() { textSearchFiles = append(textSearchFiles, owned...) }
}

func referenceInstallTextSearchFiles(ctx context.Context) error {
	if len(textSearchFiles) == 0 {
		return nil
	}
	// Reuse the public naming/duplicate contract before any file is installed.
	validation := stuntgres.NewDriver()
	for _, file := range textSearchFiles {
		if err := validation.RegisterTextSearchFile(file); err != nil {
			return fmt.Errorf("text search manifest %q: %w", file.Name, err)
		}
	}
	code, output, err := Container.Exec(ctx, []string{"pg_config", "--sharedir"}, exec.Multiplexed())
	if err != nil {
		return fmt.Errorf("text search sharedir: %w", err)
	}
	data, err := io.ReadAll(output)
	if err != nil {
		return fmt.Errorf("text search sharedir output: %w", err)
	}
	shared := strings.TrimSpace(string(data))
	if code != 0 || !path.IsAbs(shared) || strings.ContainsAny(shared, "\r\n\x00") {
		return fmt.Errorf("text search pg_config exit=%d output=%q", code, data)
	}
	TextSearchDataDirectory = path.Join(shared, "tsearch_data")
	for _, file := range textSearchFiles {
		if err := Container.CopyToContainer(ctx, file.Data, path.Join(TextSearchDataDirectory, file.Name), 0o644); err != nil {
			return fmt.Errorf("install text search file %q: %w", file.Name, err)
		}
	}
	return nil
}
