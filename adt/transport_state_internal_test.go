package adt

import "testing"

// TestTransportStatusLetters_AllKnown keeps the letter list that feeds the
// E070 condition in step with TransportStateOf: a letter the list carries but
// the mapping does not know would never be selected by any filter.
func TestTransportStatusLetters_AllKnown(t *testing.T) {
	for _, letter := range transportStatusLetters {
		if TransportStateOf(letter) == TransportStateUnknown {
			t.Errorf("letter %q is listed but maps to TransportStateUnknown", letter)
		}
	}
}

func TestTransportStatusCondition(t *testing.T) {
	for filter, want := range map[TransportFilter]string{
		TransportFilterAll:        "",
		TransportFilterModifiable: "( TRSTATUS = 'D' OR TRSTATUS = 'L' )",
		TransportFilterReleased:   "( TRSTATUS = 'R' OR TRSTATUS = 'N' )",
	} {
		if got := transportStatusCondition(filter); got != want {
			t.Errorf("transportStatusCondition(%v) = %q, want %q", filter, got, want)
		}
	}
}

// TestIsReleasedStatus covers the deprecated wrapper for as long as it ships.
func TestIsReleasedStatus(t *testing.T) {
	for status, want := range map[string]bool{
		TransportStatusReleased:            true,
		TransportStatusReleasedProtected:   true,
		TransportStatusModifiable:          false,
		TransportStatusModifiableProtected: false,
		TransportStatusReleaseStarted:      false,
		"":                                 false,
	} {
		if got := IsReleasedStatus(status); got != want {
			t.Errorf("IsReleasedStatus(%q) = %v, want %v", status, got, want)
		}
	}
}
