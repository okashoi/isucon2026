package isureal

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRegionAdvisoryKeysSortedAndDeduped(t *testing.T) {
	keys, err := regionAdvisoryKeys([]string{"asia", "america", "europe", "america"})
	if err != nil {
		t.Fatal(err)
	}
	want := []int32{1, 2, 3}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
}

func TestRegionAdvisoryKeysRejectsUnknown(t *testing.T) {
	if _, err := regionAdvisoryKeys([]string{"asia", "moon"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestUserPairAdvisoryKeyOrderIndependent(t *testing.T) {
	if userPairAdvisoryKey("a", "b") != userPairAdvisoryKey("b", "a") {
		t.Fatal("pair key differs by argument order")
	}
	if userPairAdvisoryKey("a", "b") == userPairAdvisoryKey("a", "c") {
		t.Fatal("different pairs share a key")
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("expected unique violation")
	}
	if isUniqueViolation(&pgconn.PgError{Code: "23503"}) {
		t.Fatal("foreign key violation is not a unique violation")
	}
	if isUniqueViolation(errors.New("insert failed")) {
		t.Fatal("plain error is not a unique violation")
	}
}
