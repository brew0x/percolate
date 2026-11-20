package repo

import (
	"flag"
	"testing"
)

// make golden passes -update to every package; accept it here so a model-only
// test package does not turn the golden target into a flag error.
var _ = flag.Bool("update", false, "accepted for repository golden updates")

func TestWorkflowRunStateHelpers(t *testing.T) {
	for _, tc := range []struct {
		conclusion string
		failed     bool
	}{
		{"success", false},
		{"failure", true},
		{"timed_out", true},
		{"startup_failure", true},
		{"cancelled", false},
		{"skipped", false},
		{"neutral", false},
		{"", false},
	} {
		t.Run(tc.conclusion, func(t *testing.T) {
			r := WorkflowRun{Status: "completed", Conclusion: tc.conclusion}
			if got := r.Failed(); got != tc.failed {
				t.Fatalf("Failed() = %v, want %v", got, tc.failed)
			}
			if got := FailedConclusion(tc.conclusion); got != tc.failed {
				t.Fatalf("FailedConclusion(%q) = %v, want %v", tc.conclusion, got, tc.failed)
			}
		})
	}
}

func TestJobStateHelpers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		job     Job
		running bool
		failed  bool
	}{
		{"queued", Job{Status: "queued"}, true, false},
		{"in progress", Job{Status: "in_progress"}, true, false},
		{"completed success", Job{Status: "completed", Conclusion: "success"}, false, false},
		{"completed failure", Job{Status: "completed", Conclusion: "failure"}, false, true},
		{"timed out", Job{Status: "completed", Conclusion: "timed_out"}, false, true},
		{"startup failure", Job{Status: "completed", Conclusion: "startup_failure"}, false, true},
		{"cancelled", Job{Status: "completed", Conclusion: "cancelled"}, false, false},
		{"skipped", Job{Status: "completed", Conclusion: "skipped"}, false, false},
		{"unknown status", Job{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.job.Running(); got != tc.running {
				t.Fatalf("Running() = %v, want %v", got, tc.running)
			}
			if got := tc.job.Failed(); got != tc.failed {
				t.Fatalf("Failed() = %v, want %v", got, tc.failed)
			}
		})
	}
}
