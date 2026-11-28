package content

import "testing"

// TestScrubGate pins the ISI-7970 C3 gate contract: every unwaived finding
// blocks publish regardless of severity; an explicit waiver clears exactly that
// finding; a clear slice (empty or all-waived) does not block.
func TestScrubGate(t *testing.T) {
	cases := []struct {
		name      string
		findings  []ScrubFinding
		wantBlock int // number of blocking findings
	}{
		{"nil", nil, 0},
		{"empty", []ScrubFinding{}, 0},
		{"single warn blocks until waived", []ScrubFinding{{Rule: "pii-email", Severity: "warn"}}, 1},
		{"block severity blocks", []ScrubFinding{{Rule: "aws-key", Severity: "block"}}, 1},
		{"waived clears", []ScrubFinding{{Rule: "aws-key", Severity: "block", Waived: true, WaivedBy: "henrik"}}, 0},
		{"mixed: one waived one live", []ScrubFinding{
			{Rule: "aws-key", Severity: "block", Waived: true, WaivedBy: "admin"},
			{Rule: "pii-email", Severity: "warn"},
		}, 1},
		{"all waived", []ScrubFinding{
			{Rule: "a", Waived: true},
			{Rule: "b", Waived: true},
		}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UnresolvedFindings(tc.findings)
			if len(got) != tc.wantBlock {
				t.Fatalf("UnresolvedFindings: got %d blocking, want %d (%+v)", len(got), tc.wantBlock, got)
			}
			if (len(got) > 0) != (len(UnresolvedFindings(tc.findings)) > 0) {
				t.Fatal("inconsistent gate")
			}
		})
	}
}
