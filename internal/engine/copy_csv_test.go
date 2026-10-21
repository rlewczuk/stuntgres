package engine

import (
	"stuntgres/internal/engine/copyfmt"
	"testing"
)

func TestCopyCSVLifecycle(t *testing.T) {
	t.Run("OwnerReservation", func(t *testing.T) { copyOwnerReservation(t, copyfmt.CSV) })
	t.Run("OperationExpiry", func(t *testing.T) { copyTimeoutTest(t, true) })
	t.Run("FinalCancellationVeto", func(t *testing.T) { copyFinalCancellationVeto(t, true) })
}
