package githubreview

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
)

// VMTestAttempt retains the original execution binding even before delivery is acknowledged.
type VMTestAttempt struct {
	TestID           string           `json:"testId,omitempty"`
	TestName         string           `json:"testName,omitempty"`
	EntryPath        string           `json:"entryPath,omitempty"`
	Selection        string           `json:"selection,omitempty"`
	SelectionReason  string           `json:"selectionReason,omitempty"`
	SelectionBaseSHA string           `json:"selectionBaseSha,omitempty"`
	PlanHash         string           `json:"planHash,omitempty"`
	ReceiptBaseSHA   string           `json:"receiptBaseSha,omitempty"`
	ExactComparison  bool             `json:"exactComparison,omitempty"`
	ReusedFrom       *QuickTestOrigin `json:"reusedFrom,omitempty"`

	Command           []string                `json:"command,omitempty"`
	ObserveOnly       bool                    `json:"-"`
	Attempt           int                     `json:"attempt"`
	Status            string                  `json:"status"`
	RequestID         string                  `json:"requestId,omitempty"`
	TaskID            string                  `json:"taskId,omitempty"`
	AttemptID         string                  `json:"attemptId,omitempty"`
	VMID              string                  `json:"vmId,omitempty"`
	ProjectID         string                  `json:"projectId,omitempty"`
	Binding           *compute.ProjectBinding `json:"binding,omitempty"`
	Log               string                  `json:"log,omitempty"`
	Error             string                  `json:"error,omitempty"`
	ExitCode          *int                    `json:"exitCode,omitempty"`
	CheckoutSHA       string                  `json:"checkoutSha,omitempty"`
	AssertionExitCode *int                    `json:"assertionExitCode,omitempty"`
	Truncated         bool                    `json:"truncated,omitempty"`
	UpdatedAt         time.Time               `json:"updatedAt"`
}

type QuickTestOrigin struct {
	BaseSHA string `json:"baseSha"`
	Attempt int    `json:"attempt"`
}

type VMTestRunner interface {
	Poll(context.Context, Target, []string, string, VMTestAttempt) (VMTestAttempt, error)
}

type ComputeVMTests struct {
	Gateway                      *gateway.Gateway
	HostID, ProjectID, MachineID string
	RepositoryTargets            map[string]ReviewVMTarget
}

const reviewLogLimit = 16 << 10

var reviewTaskLimits = compute.TaskLimits{TimeoutSeconds: 600, CPUs: 1, MemoryMiB: 768, MaxProcesses: 128, MaxLogBytes: 1 << 20}

func verifiedVMAssertion(t VMTestAttempt) bool {
	if t.ExitCode == nil || t.AssertionExitCode == nil || t.Error != "" {
		return false
	}
	return (t.Status == "passed" && *t.ExitCode == 0 && *t.AssertionExitCode == 0) ||
		(t.Status == "failed" && *t.ExitCode == 10 && *t.AssertionExitCode != 0 && *t.AssertionExitCode >= -127 && *t.AssertionExitCode <= 255 && !t.Truncated)
}

// The installed guest supervisor emits these two lines before the reviewed
// checkout can execute. Read the original log prefix, never a rolling tail
// that could begin with text supplied by the assertion itself.
func verifiedVMCheckout(prefix []byte, target Target) string {
	for len(prefix) != 0 {
		line, rest, complete := bytes.Cut(prefix, []byte("\n"))
		if !complete {
			break
		}
		prefix = rest
		if bytes.HasPrefix(line, []byte("sudo:")) {
			continue
		}
		if string(line) == "Preparing "+target.Repository+" at "+target.HeadSHA &&
			bytes.HasPrefix(prefix, []byte("Testing commit "+target.HeadSHA+"\n")) {
			return target.HeadSHA
		}
		break
	}
	return ""
}

// The outer helper maps any nonzero command status to 10. An intact log from
// that completed helper contains its final command status. Duplicate markers
// (which the assertion itself can print) or truncated logs are ambiguous.
func failedAssertionExit(log []byte) *int {
	const marker = "Test command exited with status "
	if bytes.Count(log, []byte(marker)) != 1 || len(log) == 0 || log[len(log)-1] != '\n' {
		return nil
	}
	lines := log[:len(log)-1]
	last := lines[bytes.LastIndexByte(lines, '\n')+1:]
	raw, ok := strings.CutPrefix(string(last), marker)
	if !ok {
		return nil
	}
	code, err := strconv.Atoi(raw)
	if err != nil || code == 0 || code < -127 || code > 255 || strconv.Itoa(code) != raw {
		return nil
	}
	return &code
}

func reviewTaskSpec(t Target, argv []string) compute.TaskSpec {
	return compute.TaskSpec{
		Argv:   append([]string{"/usr/bin/sudo", "-n", "--", "/usr/local/lib/axv/review-test", t.Repository, t.HeadSHA}, argv...),
		Limits: reviewTaskLimits,
	}
}

func boundReviewRequestID(key string, binding compute.ProjectBinding) string {
	return "review-" + reviewDigest("review-vm-request-v2", key, binding)[:48]
}

func bindVMTestAttempt(a *VMTestAttempt, binding compute.ProjectBinding, key string) {
	a.Binding, a.VMID, a.ProjectID = &binding, binding.MachineID, binding.ProjectID
	a.RequestID = boundReviewRequestID(key, binding)
}

func vmError(a VMTestAttempt, message string) (VMTestAttempt, error) {
	a.Status, a.Error = "error", message
	return a, nil
}

// Poll captures the VM identity before the first write. After a lost response
// the original request receipt, even after a Compute Task retry, owns this result.
func (v ComputeVMTests) Poll(ctx context.Context, t Target, argv []string, key string, a VMTestAttempt) (VMTestAttempt, error) {
	a.UpdatedAt = time.Now().UTC()
	if !t.valid() || key == "" || len(argv) == 0 || argv[0] == "" {
		return vmError(a, "Invalid review target or test command.")
	}
	hostID, projectID := v.HostID, v.ProjectID
	if a.Binding != nil {
		hostID, projectID = a.Binding.HostID, a.Binding.ProjectID
	}
	if v.Gateway == nil || hostID == "" || projectID == "" || (a.Binding == nil && v.MachineID == "") {
		return vmError(a, "Configure a dedicated review VM host, machine and project on this workstation.")
	}
	client, err := v.Gateway.ConnectHost(ctx, hostID)
	if err != nil {
		if a.Binding == nil {
			return vmError(a, "The configured review VM host is unavailable.")
		}
		a.Status, a.Error = "running", "Waiting to reconnect to the original VM host."
		return a, nil
	}
	defer client.Close()
	if a.Binding == nil {
		if a.ObserveOnly {
			a.Status, a.Error = "canceled", "The review changed before the test was submitted."
			return a, nil
		}
		p, err := client.ProjectBinding(ctx, projectID)
		if err != nil || p.Binding.HostID != hostID || p.Binding.ProjectID != projectID || p.Binding.MachineID != v.MachineID {
			return vmError(a, "The dedicated review project is unavailable on its configured VM.")
		}
		bindVMTestAttempt(&a, p.Binding, key)
		a.Status, a.Error = "running", ""
		return a, nil
	}
	requestID := a.RequestID
	if requestID == "" {
		return vmError(a, "The bound VM test attempt is missing its request identity.")
	}
	result, err := client.ProjectTaskByRequest(ctx, projectID, requestID)
	if missingReceipt(err) {
		if a.ObserveOnly && a.TaskID == "" {
			a.Status, a.Error = "canceled", "The review changed before Compute accepted this test."
			return a, nil
		}
		if a.TaskID != "" || hostID != v.HostID || projectID != v.ProjectID || a.Binding.MachineID != v.MachineID {
			return vmError(a, "The original task receipt or its dedicated VM selection changed.")
		}
		result, err = client.SubmitProjectTask(ctx, projectID, compute.SubmitProjectTaskRequest{
			RequestID: requestID, Binding: *a.Binding, Spec: reviewTaskSpec(t, argv),
		})
	}
	if err != nil {
		if terminalVMRequestError(err) {
			return vmError(a, "Compute declined this VM test: "+err.Error())
		}
		a.Status, a.Error = "running", "Waiting for the original Compute task receipt: "+err.Error()
		return a, nil
	}
	attempt := result.Attempt
	expected := reviewTaskSpec(t, argv)
	if result.Task.HostID != hostID || result.Task.ProjectID != projectID || result.Task.RequestID != requestID ||
		attempt.RequestID != requestID || attempt.Binding != *a.Binding || result.Task.ID == "" || attempt.ID == "" ||
		attempt.TaskID != result.Task.ID || !slices.Equal(result.Task.Spec.Argv, expected.Argv) ||
		result.Task.Spec.Directory != expected.Directory || result.Task.Spec.Initialization != nil || result.Task.Spec.Limits != expected.Limits ||
		(a.TaskID != "" && (a.TaskID != result.Task.ID || a.AttemptID != attempt.ID)) {
		return vmError(a, "The Compute task receipt does not match the original review attempt.")
	}
	a.RequestID, a.TaskID, a.AttemptID, a.ExitCode = requestID, result.Task.ID, attempt.ID, attempt.ExitCode
	a.Status, a.Error = "running", attempt.SyncError
	var completeLog []byte
	// The cached log can remain available after its guest is stopped or replaced.
	if attempt.CachedBytes > 0 {
		offset := max(int64(0), attempt.CachedBytes-reviewLogLimit)
		log, err := client.ProjectTaskLogs(ctx, projectID, a.TaskID, a.AttemptID, offset, reviewLogLimit)
		if err != nil || !matchesVMLog(log, a, offset, reviewLogLimit) {
			a.Error = "The test log could not be read from the original attempt."
		} else {
			if offset == 0 {
				a.CheckoutSHA = verifiedVMCheckout(log.Data, t)
				if !attempt.LogLimited && attempt.CachedBytes == attempt.LogBytes && int64(len(log.Data)) == attempt.LogBytes {
					completeLog = log.Data
				}
			} else if a.CheckoutSHA == "" {
				const prefixLimit = 4096
				prefix, readErr := client.ProjectTaskLogs(ctx, projectID, a.TaskID, a.AttemptID, 0, prefixLimit)
				if readErr == nil && matchesVMLog(prefix, a, 0, prefixLimit) {
					a.CheckoutSHA = verifiedVMCheckout(prefix.Data, t)
				}
			}
			a.Log = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) && r != '\n' && r != '\t' {
					return -1
				}
				return r
			}, strings.ToValidUTF8(string(log.Data), "�"))
		}
	}
	a.Truncated = attempt.LogLimited || attempt.CachedBytes < attempt.LogBytes || attempt.CachedBytes > reviewLogLimit
	switch attempt.Status {
	case "succeeded":
		if !attempt.GuestFinal || attempt.ExitCode == nil || *attempt.ExitCode != 0 {
			return vmError(a, "The original guest did not confirm a successful test exit.")
		}
		a.Status = "passed"
		code := 0
		a.AssertionExitCode = &code
	case "failed":
		// Only the guest wrapper returns 10 for a command that actually ran and failed.
		if attempt.GuestFinal && attempt.ExitCode != nil && *attempt.ExitCode == 10 && (attempt.Reason == "" || attempt.Reason == "exit status 10") {
			a.Status = "failed"
			a.AssertionExitCode = failedAssertionExit(completeLog)
		} else {
			a.Status = "error"
			a.Error = vmSetupFailure(a, attempt)
		}
	case "cancelled":
		a.Status, a.Error = "canceled", attempt.Reason
	case "interrupted":
		a.Status, a.Error = "error", "The original VM execution was interrupted. "+attempt.Reason
	case "unknown":
		a.Error = "The original task outcome is unknown. " + attempt.Reason
	}
	return a, nil
}

func vmSetupFailure(a VMTestAttempt, attempt compute.TaskAttempt) string {
	if attempt.GuestFinal && attempt.ExitCode != nil && *attempt.ExitCode == 1 && a.Error == "" &&
		(attempt.Reason == "" || attempt.Reason == "exit status 1") {
		if a.CheckoutSHA != "" {
			return "The VM checked out the commit, but the test command could not start. Check the executable and its dependencies."
		}
		if a.Log != "" && !a.Truncated {
			return "The VM could not prepare the checkout. Check the review VM helper, GitHub access or the pinned source mirror."
		}
	}
	return strings.TrimSpace("The VM could not complete test setup or execution. " + attempt.Reason)
}

func matchesVMLog(log compute.ProjectTaskLog, a VMTestAttempt, offset int64, limit int) bool {
	return log.TaskID == a.TaskID && log.AttemptID == a.AttemptID && log.Offset == offset &&
		len(log.Data) <= limit && log.NextOffset == offset+int64(len(log.Data))
}

func missingReceipt(err error) bool {
	var problem *compute.Error
	return errors.As(err, &problem) && problem.Code == "not_found"
}

func terminalVMRequestError(err error) bool {
	var response *computeclient.ResponseError
	return errors.As(err, &response) && ((response.StatusCode < 500 &&
		response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests) ||
		compute.ErrorCode(err) == "unsupported" || response.StatusCode == http.StatusInsufficientStorage)
}
