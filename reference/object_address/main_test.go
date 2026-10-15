package reference_test

import (
	"os"
	"testing"

	"stuntgres/reference/fixture"
)

func TestMain(m *testing.M) { os.Exit(fixture.Main(m)) }
