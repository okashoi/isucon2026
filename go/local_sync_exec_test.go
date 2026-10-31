package isureal

import (
	"context"
	"testing"
)

func TestExecLocalSyncRejectsMismatchedOIDs(t *testing.T) {
	err := execLocalSync(context.Background(), nil, `SELECT 1`, []uint32{1}, "a", "b")
	if err == nil {
		t.Fatal("expected error")
	}
}
