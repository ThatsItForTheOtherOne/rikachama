package main

import (
	"errors"
	"testing"
)

// --- No tripcode case ---

func TestNoTripCleanName(t *testing.T) {
	cleanName, _, _, _ := parseTripFromName("Alice", "")
	if cleanName != "Alice" {
		t.Errorf("clean name for Alice was %q", cleanName)
	}
}

func TestNoTripBlankTripcode(t *testing.T) {
	_, tripcode, _, _ := parseTripFromName("Alice", "")
	if tripcode != "" {
		t.Errorf("trip code for Alice was %q", tripcode)
	}
}

func TestNoTripSecureFalse(t *testing.T) {
	_, _, secure, _ := parseTripFromName("Alice", "")
	if secure {
		t.Error("secure flag should be false when no trip")
	}
}

// --- Regular tripcode case ---

func TestRegularTripCleanName(t *testing.T) {
	cleanName, _, _, _ := parseTripFromName("Alice#Game", "")
	if cleanName != "Alice" {
		t.Errorf("clean name for Alice#Game was %q", cleanName)
	}
}

func TestRegularTripNonEmpty(t *testing.T) {
	_, tripcode, _, _ := parseTripFromName("Alice#Game", "")
	if tripcode == "" {
		t.Error("trip code for Alice#Game was blank")
	}
}

func TestRegularTripSecureFlagFalse(t *testing.T) {
	_, _, secure, _ := parseTripFromName("Alice#Game", "")
	if secure {
		t.Error("secure flag should be false for #")
	}
}

// --- Secure tripcode case ---

func TestSecureTripCleanName(t *testing.T) {
	cleanName, _, _, _ := parseTripFromName("Alice##Game", "desu")
	if cleanName != "Alice" {
		t.Errorf("clean name for Alice##Game was %q", cleanName)
	}
}

func TestSecureTripNonEmpty(t *testing.T) {
	_, tripcode, _, _ := parseTripFromName("Alice##Game", "desu")
	if tripcode == "" {
		t.Error("trip code for Alice##Game was blank")
	}
}

func TestSecureTripSecureFlagTrue(t *testing.T) {
	_, _, secure, _ := parseTripFromName("Alice##Game", "desu")
	if !secure {
		t.Error("secure flag should be true for ##")
	}
}

func TestSecureTripDiffersFromRegular(t *testing.T) {
	_, regular, _, _ := parseTripFromName("Alice#Game", "desu")
	_, secure, _, _ := parseTripFromName("Alice##Game", "desu")
	if regular == secure {
		t.Errorf("regular and secure trips matched for same password: %q", regular)
	}
}

// --- Malformed input ---

func TestMalformedTrip(t *testing.T) {
	_, _, _, err := parseTripFromName("Alice#", "")
	if !errors.Is(err, ErrInvalidTripPassword) {
		t.Errorf("expected ErrInvalidTripPassword, got %v", err)
	}
}

func TestMalformedSecureTrip(t *testing.T) {
	_, _, _, err := parseTripFromName("Alice##", "desu")
	if !errors.Is(err, ErrInvalidTripPassword) {
		t.Errorf("expected ErrInvalidTripPassword, got %v", err)
	}
}

func TestSecureTripWithoutSecret(t *testing.T) {
	_, _, _, err := parseTripFromName("Alice##Game", "")
	if !errors.Is(err, ErrMissingSiteSecret) {
		t.Errorf("expected ErrMissingSiteSecret, got %v", err)
	}
}

// --- Embedded hash in password (weird but valid) ---

func TestEmbeddedHashInRegularPasswordCleanName(t *testing.T) {
	cleanName, _, _, _ := parseTripFromName("Alice#G#me", "")
	if cleanName != "Alice" {
		t.Errorf("clean name for Alice#G#me was %q", cleanName)
	}
}

func TestEmbeddedHashInRegularPasswordNonEmpty(t *testing.T) {
	_, tripcode, _, _ := parseTripFromName("Alice#G#me", "")
	if tripcode == "" {
		t.Error("trip code for Alice#G#me was blank")
	}
}

func TestEmbeddedHashInSecurePasswordCleanName(t *testing.T) {
	cleanName, _, _, _ := parseTripFromName("Alice##G#me", "desu")
	if cleanName != "Alice" {
		t.Errorf("clean name for Alice##G#me was %q", cleanName)
	}
}

func TestEmbeddedHashInSecurePasswordNonEmpty(t *testing.T) {
	_, tripcode, _, _ := parseTripFromName("Alice##G#me", "desu")
	if tripcode == "" {
		t.Error("trip code for Alice##G#me was blank")
	}
}

// --- Semantic properties ---

func TestRegularTripDeterministic(t *testing.T) {
	_, a, _, _ := parseTripFromName("Alice#Game", "")
	_, b, _, _ := parseTripFromName("Alice#Game", "")
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
}

func TestSecureTripDeterministic(t *testing.T) {
	_, a, _, _ := parseTripFromName("Alice##Game", "desu")
	_, b, _, _ := parseTripFromName("Alice##Game", "desu")
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
}

func TestSecureTripDiffersBySecret(t *testing.T) {
	_, a, _, _ := parseTripFromName("Alice##Game", "secret-a")
	_, b, _, _ := parseTripFromName("Alice##Game", "secret-b")
	if a == b {
		t.Error("same password produced same secure trip with different secrets")
	}
}

func TestRegularTripIgnoresSecret(t *testing.T) {
	_, a, _, _ := parseTripFromName("Alice#Game", "secret-a")
	_, b, _, _ := parseTripFromName("Alice#Game", "secret-b")
	if a != b {
		t.Errorf("regular trip changed with secret: %q vs %q", a, b)
	}
}
