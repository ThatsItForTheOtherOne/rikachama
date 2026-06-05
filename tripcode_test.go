package main

import (
	"errors"
	"testing"
)

func TestRegularTripIsDeterministic(t *testing.T) {
	a, _ := regularTrip("hunter2")
	b, _ := regularTrip("hunter2")
	if a != b {
		t.Errorf("same password gave different tripcodes: %q vs %q", a, b)
	}
}

func TestSecureTripDiffersBySiteSecret(t *testing.T) {
	a, _ := secureTrip("hunter2", "site-a-secret")
	b, _ := secureTrip("hunter2", "site-b-secret")
	if a == b {
		t.Errorf("different site secrets produced same tripcodes: %q", a)
	}
}

func TestEmptyPasswordRejected(t *testing.T) {
	if _, err := regularTrip(""); !errors.Is(err, ErrInvalidTripPassword) {
		t.Errorf("expected ErrInvalidTripPassword, got %v", err)
	}
}

func TestMissingSiteSecretRejected(t *testing.T) {
	if _, err := secureTrip("hunter2", ""); !errors.Is(err, ErrMissingSiteSecret) {
		t.Errorf("expected ErrMissingSiteSecret, got %v", err)
	}
}

func TestTripIsTenChars(t *testing.T) {
	trip, _ := regularTrip("hunter2")
	if len(trip) != 10 {
		t.Errorf("regular trip not 10 chars: %q (len=%d)", trip, len(trip))
	}
}

func TestSecureTripIsTenChars(t *testing.T) {
	trip, _ := secureTrip("hunter2", "site-a-secret")
	if len(trip) != 10 {
		t.Errorf("regular trip not 10 chars: %q (len=%d)", trip, len(trip))
	}
}
