package state

import (
	"sync"
	"testing"
	"time"
)

// testLogger captures log messages for testing.
type testLogger struct {
	mu       sync.Mutex
	infos    []string
	errors   []string
}

func newTestLogger() *testLogger {
	return &testLogger{
		infos:  make([]string, 0),
		errors: make([]string, 0),
	}
}

func (l *testLogger) Info(msg string, keysAndValues ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, msg)
}

func (l *testLogger) Error(msg string, keysAndValues ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
}

func (l *testLogger) InfoCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.infos)
}

func (l *testLogger) ErrorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.errors)
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateBoot, "Boot"},
		{StateAuthorize, "Authorize"},
		{StateHydrate, "Hydrate"},
		{StateDecrypt, "Decrypt"},
		{StateReady, "Ready"},
		{StateSuspended, "Suspended"},
		{State(99), "Unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("State.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseState(t *testing.T) {
	tests := []struct {
		input    string
		expected State
		wantErr  bool
	}{
		{"Boot", StateBoot, false},
		{"Authorize", StateAuthorize, false},
		{"Hydrate", StateHydrate, false},
		{"Decrypt", StateDecrypt, false},
		{"Ready", StateReady, false},
		{"Suspended", StateSuspended, false},
		{"Invalid", StateBoot, true},
		{"", StateBoot, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseState(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseState(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("ParseState(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNewMachine(t *testing.T) {
	m := New()

	if got := m.CurrentState(); got != StateBoot {
		t.Errorf("New machine state = %v, want %v", got, StateBoot)
	}

	if m.IsReady() {
		t.Error("New machine should not be ready")
	}

	if m.IsSuspended() {
		t.Error("New machine should not be suspended")
	}

	if m.Uptime() < 0 {
		t.Error("Uptime should be non-negative")
	}
}

func TestNewMachineWithOptions(t *testing.T) {
	logger := newTestLogger()
	assetID := "test-asset-123"

	m := New(
		WithLogger(logger),
		WithAssetID(assetID),
	)

	if got := m.AssetID(); got != assetID {
		t.Errorf("AssetID = %q, want %q", got, assetID)
	}

	// Logger should have been called for initial state
	if logger.InfoCount() < 1 {
		t.Error("Logger should have been called on initialization")
	}
}

func TestValidTransitions(t *testing.T) {
	tests := []struct {
		name     string
		from     State
		to       State
	}{
		{"Boot to Authorize", StateBoot, StateAuthorize},
		{"Authorize to Hydrate", StateAuthorize, StateHydrate},
		{"Hydrate to Decrypt", StateHydrate, StateDecrypt},
		{"Decrypt to Ready", StateDecrypt, StateReady},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()

			// Advance to the starting state
			for s := StateBoot; s < tt.from; s++ {
				if err := m.Transition(s + 1); err != nil {
					t.Fatalf("Failed to reach starting state: %v", err)
				}
			}

			// Verify we're at the expected starting state
			if m.CurrentState() != tt.from {
				t.Fatalf("Setup failed: state = %v, want %v", m.CurrentState(), tt.from)
			}

			// Perform the transition under test
			if err := m.Transition(tt.to); err != nil {
				t.Errorf("Transition(%v -> %v) error = %v", tt.from, tt.to, err)
			}

			if m.CurrentState() != tt.to {
				t.Errorf("After transition: state = %v, want %v", m.CurrentState(), tt.to)
			}
		})
	}
}

func TestInvalidTransitions(t *testing.T) {
	tests := []struct {
		name     string
		from     State
		to       State
	}{
		{"Boot to Hydrate (skip)", StateBoot, StateHydrate},
		{"Boot to Decrypt (skip)", StateBoot, StateDecrypt},
		{"Boot to Ready (skip)", StateBoot, StateReady},
		{"Authorize to Boot (backward)", StateAuthorize, StateBoot},
		{"Ready to Boot (backward)", StateReady, StateBoot},
		{"Boot to Boot (same)", StateBoot, StateBoot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()

			// Advance to the starting state
			for s := StateBoot; s < tt.from; s++ {
				if err := m.Transition(s + 1); err != nil {
					t.Fatalf("Failed to reach starting state: %v", err)
				}
			}

			// Attempt invalid transition
			err := m.Transition(tt.to)
			if err == nil {
				t.Errorf("Transition(%v -> %v) should have failed", tt.from, tt.to)
			}

			// State should remain unchanged
			if m.CurrentState() != tt.from {
				t.Errorf("State changed on invalid transition: got %v, want %v", m.CurrentState(), tt.from)
			}
		})
	}
}

func TestFullLifecycle(t *testing.T) {
	logger := newTestLogger()
	m := New(WithLogger(logger), WithAssetID("lifecycle-test"))

	// Boot -> Authorize
	if err := m.Transition(StateAuthorize); err != nil {
		t.Fatalf("Boot -> Authorize failed: %v", err)
	}

	// Authorize -> Hydrate
	if err := m.TransitionWithReason(StateHydrate, "manifest downloaded"); err != nil {
		t.Fatalf("Authorize -> Hydrate failed: %v", err)
	}

	// Hydrate -> Decrypt
	if err := m.TransitionWithReason(StateDecrypt, "starting decryption"); err != nil {
		t.Fatalf("Hydrate -> Decrypt failed: %v", err)
	}

	// At this point, should be "at least Decrypt"
	if !m.IsAtLeast(StateDecrypt) {
		t.Error("Should be at least Decrypt state")
	}
	if !m.IsAtLeast(StateHydrate) {
		t.Error("Should be at least Hydrate state")
	}
	if m.IsAtLeast(StateReady) {
		t.Error("Should not be at least Ready yet")
	}

	// Decrypt -> Ready
	if err := m.TransitionWithReason(StateReady, "decryption complete"); err != nil {
		t.Fatalf("Decrypt -> Ready failed: %v", err)
	}

	if !m.IsReady() {
		t.Error("Should be ready after full lifecycle")
	}

	// Check history
	history := m.History()
	if len(history) != 5 { // initial + 4 transitions
		t.Errorf("History length = %d, want 5", len(history))
	}

	// Verify logging happened
	if logger.InfoCount() < 5 {
		t.Errorf("Expected at least 5 info logs, got %d", logger.InfoCount())
	}
}

func TestSuspend(t *testing.T) {
	tests := []struct {
		name      string
		fromState State
	}{
		{"from Boot", StateBoot},
		{"from Authorize", StateAuthorize},
		{"from Hydrate", StateHydrate},
		{"from Decrypt", StateDecrypt},
		{"from Ready", StateReady},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()

			// Advance to starting state
			for s := StateBoot; s < tt.fromState; s++ {
				if err := m.Transition(s + 1); err != nil {
					t.Fatalf("Failed to reach starting state: %v", err)
				}
			}

			// Suspend
			err := m.Suspend("test error")
			if err != nil {
				t.Errorf("Suspend() error = %v", err)
			}

			if !m.IsSuspended() {
				t.Error("Should be suspended")
			}

			if m.CurrentState() != StateSuspended {
				t.Errorf("State = %v, want Suspended", m.CurrentState())
			}
		})
	}
}

func TestSuspendTwice(t *testing.T) {
	m := New()

	// First suspend should succeed
	if err := m.Suspend("first error"); err != nil {
		t.Errorf("First Suspend() error = %v", err)
	}

	// Second suspend should fail
	err := m.Suspend("second error")
	if err != ErrAlreadySuspended {
		t.Errorf("Second Suspend() error = %v, want ErrAlreadySuspended", err)
	}
}

func TestTransitionFromSuspended(t *testing.T) {
	m := New()

	if err := m.Suspend("error"); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	// Try to transition - should fail
	err := m.Transition(StateAuthorize)
	if err == nil {
		t.Error("Transition from Suspended should fail")
	}

	// State should remain Suspended
	if m.CurrentState() != StateSuspended {
		t.Errorf("State = %v, want Suspended", m.CurrentState())
	}
}

func TestIsAtLeastSuspended(t *testing.T) {
	m := New()

	// Advance to Ready
	for s := StateBoot; s < StateReady; s++ {
		if err := m.Transition(s + 1); err != nil {
			t.Fatalf("Failed to advance: %v", err)
		}
	}

	// Verify IsAtLeast works when Ready
	if !m.IsAtLeast(StateReady) {
		t.Error("Should be at least Ready")
	}

	// Suspend
	if err := m.Suspend("error"); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	// After suspension, IsAtLeast should return false for normal states
	if m.IsAtLeast(StateReady) {
		t.Error("Suspended state should not be 'at least Ready'")
	}
	if m.IsAtLeast(StateBoot) {
		t.Error("Suspended state should not be 'at least Boot'")
	}

	// But should be 'at least Suspended'
	if !m.IsAtLeast(StateSuspended) {
		t.Error("Suspended state should be 'at least Suspended'")
	}
}

func TestStatus(t *testing.T) {
	m := New(WithAssetID("status-test"))

	// Wait a tiny bit to ensure uptime > 0
	time.Sleep(time.Millisecond)

	status := m.Status()

	if status.State != "Boot" {
		t.Errorf("Status.State = %q, want %q", status.State, "Boot")
	}

	if status.AssetID != "status-test" {
		t.Errorf("Status.AssetID = %q, want %q", status.AssetID, "status-test")
	}

	if status.Uptime <= 0 {
		t.Error("Status.Uptime should be positive")
	}

	if status.UptimeStr == "" {
		t.Error("Status.UptimeStr should not be empty")
	}

	if status.StartTime.IsZero() {
		t.Error("Status.StartTime should not be zero")
	}
}

func TestSetAssetID(t *testing.T) {
	m := New()

	if m.AssetID() != "" {
		t.Error("Initial AssetID should be empty")
	}

	m.SetAssetID("new-asset")

	if m.AssetID() != "new-asset" {
		t.Errorf("AssetID = %q, want %q", m.AssetID(), "new-asset")
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := New()

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Concurrent readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = m.CurrentState()
				_ = m.IsReady()
				_ = m.IsSuspended()
				_ = m.Status()
				_ = m.AssetID()
				_ = m.Uptime()
			}
		}()
	}

	// Single writer advancing through states
	wg.Add(1)
	go func() {
		defer wg.Done()
		transitions := []State{StateAuthorize, StateHydrate, StateDecrypt, StateReady}
		for _, s := range transitions {
			time.Sleep(time.Microsecond)
			if err := m.Transition(s); err != nil {
				errors <- err
			}
		}
	}()

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent access error: %v", err)
	}

	if !m.IsReady() {
		t.Error("Machine should be Ready after concurrent test")
	}
}

func TestHistory(t *testing.T) {
	m := New()

	// Perform some transitions
	m.Transition(StateAuthorize)
	m.TransitionWithReason(StateHydrate, "downloaded")
	m.Transition(StateDecrypt)

	history := m.History()

	// Should have initial + 3 transitions
	if len(history) != 4 {
		t.Errorf("History length = %d, want 4", len(history))
	}

	// Verify last transition
	last := history[len(history)-1]
	if last.From != StateHydrate || last.To != StateDecrypt {
		t.Errorf("Last transition = %v -> %v, want Hydrate -> Decrypt", last.From, last.To)
	}

	// History should be a copy (modifying it shouldn't affect machine)
	history[0].Reason = "modified"
	original := m.History()
	if original[0].Reason == "modified" {
		t.Error("History should return a copy, not original slice")
	}
}

func BenchmarkCurrentState(b *testing.B) {
	m := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.CurrentState()
	}
}

func BenchmarkTransition(b *testing.B) {
	for i := 0; i < b.N; i++ {
		m := New()
		m.Transition(StateAuthorize)
		m.Transition(StateHydrate)
		m.Transition(StateDecrypt)
		m.Transition(StateReady)
	}
}
