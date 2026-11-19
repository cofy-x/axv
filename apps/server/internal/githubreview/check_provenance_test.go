package githubreview

import (
	"strings"
	"testing"
)

func TestCheckoutReceiptRequiresPrimaryCheckoutWithinStep(t *testing.T) {
	target := workerTestTarget("org/project", 174, sourceRepository)
	sha := strings.Repeat("c", 40)
	step := actionsStep{Number: 2, Started: "2026-11-19T01:00:00Z", Finished: "2026-11-19T01:00:03Z"}
	const ts = "2026-11-19T01:00:02.2Z "
	raw := strings.Join([]string{"##[group]Run actions/checkout@v5", "  repository: org/project", "##[endgroup]", "Syncing repository: org/project", "[command]/usr/bin/git fetch origin +" + sha + ":refs/remotes/pull/174/merge", "[command]/usr/bin/git log -1 --format=%H", sha}, "\n"+ts)
	raw = ts + raw + "\n"
	got, ref := checkoutReceipt(strings.NewReader(raw), target, step)
	if got != sha || ref != "refs/pull/174/merge" {
		t.Fatalf("got %s %s", got, ref)
	}
	for _, in := range []string{
		strings.Replace(raw, "actions/checkout@v5", "echo a shell command", 1),
		strings.Replace(raw, "Syncing repository: org/project", "Syncing repository: other/repo", 1),
		strings.Replace(raw, "  repository: org/project", "  path: tools", 1),
		strings.ReplaceAll(raw, "01:00:02.2", "01:00:08.1"),
		strings.Replace(raw, "[command]/usr/bin/git log -1 --format=%H", "##[group]Run arbitrary-script\n"+ts+"[command]/usr/bin/git log -1 --format=%H", 1),
	} {
		if got, _ := checkoutReceipt(strings.NewReader(in), target, step); got != "" {
			t.Errorf("unexpected verified checkout %q", got)
		}
	}
}
