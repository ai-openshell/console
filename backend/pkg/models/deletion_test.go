package models

import (
	"testing"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

// The SDK contract (openshell/v1/types/mutations.go) is explicit: only
// Completed and AlreadyAbsent establish completion, and unrecognized numeric
// outcomes must not be treated as completion.
func TestFromSDKDeletion(t *testing.T) {
	tests := []struct {
		res         *openshell.DeletionResult
		name        string
		wantOutcome string
		wantDeleted bool
	}{
		{&openshell.DeletionResult{Outcome: openshell.DeletionCompleted}, "completed", "completed", true},
		{&openshell.DeletionResult{Outcome: openshell.DeletionAlreadyAbsent}, "already absent", "already_absent", true},
		{&openshell.DeletionResult{Outcome: openshell.DeletionAccepted}, "accepted is not completion", "accepted", false},
		{&openshell.DeletionResult{Outcome: openshell.DeletionUnspecified}, "unspecified is not completion", "unspecified", false},
		{&openshell.DeletionResult{Outcome: openshell.DeletionOutcome(99)}, "unknown future value is not completion", "unspecified", false},
		{nil, "nil result stays backward compatible", "completed", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FromSDKDeletion(tc.res)
			if got.Deleted != tc.wantDeleted {
				t.Errorf("Deleted = %v, want %v", got.Deleted, tc.wantDeleted)
			}
			if got.Outcome != tc.wantOutcome {
				t.Errorf("Outcome = %q, want %q", got.Outcome, tc.wantOutcome)
			}
		})
	}
}
