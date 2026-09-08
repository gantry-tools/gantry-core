package agent

import (
	"net/url"
	"strings"
	"sync"
)

type Cause string

const (
	CauseNone                        Cause = ""
	CauseUserStop                    Cause = "user_stop"
	CauseRequestCanceled             Cause = "request_cancelled"
	CauseOutputLimit                 Cause = "output_limit"
	CauseServiceShutdown             Cause = "service_shutdown"
	CauseProviderInsufficientBalance Cause = "provider_insufficient_balance"
)

type Outcome string

const (
	OutcomeCompleted                 Outcome = "completed"
	OutcomeCompletedWithProcessError Outcome = "completed_with_process_error"
	OutcomeFailed                    Outcome = "failed"
	OutcomeCancelled                 Outcome = "cancelled"
	OutcomeTruncated                 Outcome = "truncated"
	OutcomeInterrupted               Outcome = "interrupted"
	OutcomeIncomplete                Outcome = "incomplete"
)

type Snapshot struct {
	Cause                 Cause
	ErrorSequence         uint64
	CauseSequence         uint64
	StopSequence          uint64
	ProviderErrorSequence uint64
	Sealed                bool
}

type ExitStatus struct {
	Exited   bool
	ExitCode int
	Signaled bool
	Signal   string
}

type RunState struct {
	mu       sync.Mutex
	snapshot Snapshot
	sequence uint64
}

func NewRunState() *RunState { return &RunState{} }
func (s *RunState) NextSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	return s.sequence
}
func (s *RunState) RecordError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	if s.snapshot.ErrorSequence == 0 {
		s.snapshot.ErrorSequence = s.sequence
	}
}

func (s *RunState) RecordProviderFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	if s.snapshot.ProviderErrorSequence == 0 {
		s.snapshot.ProviderErrorSequence = s.sequence
	}
}
func (s *RunState) RecordErrorAt(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > 0 && s.snapshot.ErrorSequence == 0 {
		s.snapshot.ErrorSequence = n
	}
}
func (s *RunState) RecordStopAt(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > 0 && s.snapshot.StopSequence == 0 {
		s.snapshot.StopSequence = n
	}
}
func (s *RunState) RecordProviderFailureAt(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > 0 && s.snapshot.ProviderErrorSequence == 0 {
		s.snapshot.ProviderErrorSequence = n
	}
}
func (s *RunState) RecordCause(c Cause) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Sealed {
		return false
	}
	switch c {
	case CauseRequestCanceled:
		if s.snapshot.Cause != CauseNone {
			return false
		}
	case CauseUserStop:
		if s.snapshot.Cause != CauseNone && s.snapshot.Cause != CauseRequestCanceled {
			return false
		}
	case CauseOutputLimit:
		if s.snapshot.Cause != CauseNone && s.snapshot.Cause != CauseRequestCanceled && s.snapshot.Cause != CauseUserStop {
			return false
		}
	case CauseServiceShutdown:
		if s.snapshot.Cause != CauseNone && s.snapshot.Cause != CauseRequestCanceled {
			return false
		}
	default:
		return false
	}
	s.sequence++
	s.snapshot.Cause, s.snapshot.CauseSequence = c, s.sequence
	return true
}
func (s *RunState) Seal()              { s.mu.Lock(); s.snapshot.Sealed = true; s.mu.Unlock() }
func (s *RunState) Snapshot() Snapshot { s.mu.Lock(); defer s.mu.Unlock(); return s.snapshot }

func Classify(state Snapshot, stdoutError, validStop bool, exit ExitStatus, providerCause Cause) Outcome {
	if providerCause != CauseNone && (state.StopSequence == 0 || state.ProviderErrorSequence < state.StopSequence) && (state.Cause == CauseNone || state.CauseSequence > 0 && state.ProviderErrorSequence < state.CauseSequence) {
		return OutcomeFailed
	}
	if stdoutError && (state.StopSequence == 0 || state.ErrorSequence > 0 && state.ErrorSequence < state.StopSequence) && (state.Cause == CauseNone || state.ErrorSequence > 0 && state.CauseSequence > 0 && state.ErrorSequence < state.CauseSequence) {
		return OutcomeFailed
	}
	switch state.Cause {
	case CauseOutputLimit:
		return OutcomeTruncated
	case CauseUserStop, CauseRequestCanceled:
		return OutcomeCancelled
	case CauseServiceShutdown:
		return OutcomeInterrupted
	}
	if exit.Signaled {
		return OutcomeFailed
	}
	if exit.Exited && exit.ExitCode != 0 && validStop {
		return OutcomeCompletedWithProcessError
	}
	if exit.Exited && exit.ExitCode != 0 {
		return OutcomeFailed
	}
	if exit.Exited && exit.ExitCode == 0 && validStop {
		return OutcomeCompleted
	}
	return OutcomeFailed
}

func ClassifyProviderError(message, code string, statusCode int) bool {
	v := strings.ToLower(strings.TrimSpace(message))
	c := strings.ToLower(strings.TrimSpace(code))
	return statusCode == 402 || c == "insufficient_balance" || c == "insufficient balance" || c == "account_balance_insufficient" || c == "billing_insufficient_balance" || strings.Contains(v, "insufficient balance")
}

func SanitizeBillingURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 {
		return ""
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() != "opencode.ai" || !strings.HasPrefix(u.Path, "/workspace/") {
		return ""
	}
	return u.String()
}

func ReconcileRecovered(streamed, recovered string) (string, bool, bool) {
	streamed, recovered = strings.TrimSpace(streamed), strings.TrimSpace(recovered)
	if recovered == "" {
		return "", true, false
	}
	if streamed == "" {
		return recovered, false, false
	}
	if streamed == recovered || strings.Contains(streamed, recovered) {
		return "", true, false
	}
	if strings.HasPrefix(recovered, streamed) {
		return strings.TrimSpace(strings.TrimPrefix(recovered, streamed)), false, false
	}
	if strings.HasSuffix(recovered, streamed) {
		return recovered, false, true
	}
	for i := len(streamed); i > 0; i-- {
		if strings.HasPrefix(recovered, streamed[len(streamed)-i:]) {
			return strings.TrimSpace(recovered[i:]), false, false
		}
	}
	return recovered, false, false
}

func RunArgs(workspace, modelRef, session string, files []string, prompt string) []string {
	args := []string{"--print-logs", "--log-level", "WARN", "run", "--format", "json", "--auto", "--dir", workspace, "--model", modelRef}
	if session = strings.TrimSpace(session); session != "" {
		args = append(args, "--session", session)
	}
	for _, file := range files {
		args = append(args, "--file", file)
	}
	return append(args, "--", prompt)
}
