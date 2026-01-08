// Package state provides a thread-safe state machine for the TrustBridge Sentinel.
//
// The state machine tracks the sentinel's lifecycle from boot through to ready,
// with proper handling of error states and suspension.
package state

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State represents the current state of the sentinel.
type State int

// Sentinel states in order of progression.
const (
	StateBoot State = iota
	StateAuthorize
	StateHydrate
	StateDecrypt
	StateReady
	StateSuspended
)

// String returns the string representation of a state.
func (s State) String() string {
	switch s {
	case StateBoot:
		return "Boot"
	case StateAuthorize:
		return "Authorize"
	case StateHydrate:
		return "Hydrate"
	case StateDecrypt:
		return "Decrypt"
	case StateReady:
		return "Ready"
	case StateSuspended:
		return "Suspended"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// ParseState converts a string to a State.
func ParseState(s string) (State, error) {
	switch s {
	case "Boot":
		return StateBoot, nil
	case "Authorize":
		return StateAuthorize, nil
	case "Hydrate":
		return StateHydrate, nil
	case "Decrypt":
		return StateDecrypt, nil
	case "Ready":
		return StateReady, nil
	case "Suspended":
		return StateSuspended, nil
	default:
		return StateBoot, fmt.Errorf("unknown state: %q", s)
	}
}

// Errors returned by the state machine.
var (
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrAlreadySuspended  = errors.New("already in suspended state")
)

// TransitionEvent represents a state transition that occurred.
type TransitionEvent struct {
	From      State
	To        State
	Timestamp time.Time
	Reason    string
}

// Logger defines the interface for state machine logging.
type Logger interface {
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// defaultLogger is a no-op logger used when no logger is provided.
type defaultLogger struct{}

func (defaultLogger) Info(msg string, keysAndValues ...interface{})  {}
func (defaultLogger) Error(msg string, keysAndValues ...interface{}) {}

// Machine is a thread-safe state machine for the sentinel lifecycle.
type Machine struct {
	mu        sync.RWMutex
	state     State
	startTime time.Time
	assetID   string
	logger    Logger
	history   []TransitionEvent
}

// MachineOption is a functional option for configuring the Machine.
type MachineOption func(*Machine)

// WithLogger sets a custom logger for the state machine.
func WithLogger(logger Logger) MachineOption {
	return func(m *Machine) {
		m.logger = logger
	}
}

// WithAssetID sets the asset ID for status reporting.
func WithAssetID(assetID string) MachineOption {
	return func(m *Machine) {
		m.assetID = assetID
	}
}

// New creates a new state machine starting in the Boot state.
func New(opts ...MachineOption) *Machine {
	m := &Machine{
		state:     StateBoot,
		startTime: time.Now(),
		logger:    defaultLogger{},
		history:   make([]TransitionEvent, 0, 10),
	}

	for _, opt := range opts {
		opt(m)
	}

	// Record initial state
	m.history = append(m.history, TransitionEvent{
		From:      StateBoot,
		To:        StateBoot,
		Timestamp: m.startTime,
		Reason:    "initial state",
	})

	m.logger.Info("State machine initialized",
		"state", StateBoot.String(),
		"time", m.startTime.Format(time.RFC3339),
	)

	return m
}

// CurrentState returns the current state (thread-safe).
func (m *Machine) CurrentState() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// AssetID returns the configured asset ID.
func (m *Machine) AssetID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.assetID
}

// SetAssetID updates the asset ID (thread-safe).
func (m *Machine) SetAssetID(assetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assetID = assetID
}

// Uptime returns the duration since the state machine was created.
func (m *Machine) Uptime() time.Duration {
	return time.Since(m.startTime)
}

// StartTime returns when the state machine was created.
func (m *Machine) StartTime() time.Time {
	return m.startTime
}

// History returns a copy of the transition history.
func (m *Machine) History() []TransitionEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]TransitionEvent, len(m.history))
	copy(result, m.history)
	return result
}

// Transition attempts to move the state machine to a new state.
// Returns an error if the transition is not valid.
func (m *Machine) Transition(newState State) error {
	return m.TransitionWithReason(newState, "")
}

// TransitionWithReason attempts to move the state machine to a new state with a reason.
func (m *Machine) TransitionWithReason(newState State, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldState := m.state

	// Validate transition
	if err := m.validateTransition(oldState, newState); err != nil {
		m.logger.Error("Invalid state transition",
			"from", oldState.String(),
			"to", newState.String(),
			"error", err.Error(),
		)
		return fmt.Errorf("%w: %s -> %s", err, oldState.String(), newState.String())
	}

	// Perform transition
	m.state = newState
	now := time.Now()

	event := TransitionEvent{
		From:      oldState,
		To:        newState,
		Timestamp: now,
		Reason:    reason,
	}
	m.history = append(m.history, event)

	m.logger.Info("State transition",
		"from", oldState.String(),
		"to", newState.String(),
		"reason", reason,
		"time", now.Format(time.RFC3339),
		"uptime", m.Uptime().String(),
	)

	return nil
}

// Suspend transitions the state machine to the Suspended state.
// This is always allowed from any state except if already suspended.
func (m *Machine) Suspend(reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == StateSuspended {
		return ErrAlreadySuspended
	}

	oldState := m.state
	m.state = StateSuspended
	now := time.Now()

	event := TransitionEvent{
		From:      oldState,
		To:        StateSuspended,
		Timestamp: now,
		Reason:    reason,
	}
	m.history = append(m.history, event)

	m.logger.Error("State suspended",
		"from", oldState.String(),
		"reason", reason,
		"time", now.Format(time.RFC3339),
		"uptime", m.Uptime().String(),
	)

	return nil
}

// IsReady returns true if the state machine is in the Ready state.
func (m *Machine) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state == StateReady
}

// IsSuspended returns true if the state machine is in the Suspended state.
func (m *Machine) IsSuspended() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state == StateSuspended
}

// IsAtLeast returns true if the current state is at least the given state.
// Suspended state returns false for any comparison except with itself.
func (m *Machine) IsAtLeast(s State) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.state == StateSuspended {
		return s == StateSuspended
	}
	return m.state >= s
}

// validateTransition checks if a state transition is valid.
// Valid transitions:
// - Boot -> Authorize
// - Authorize -> Hydrate
// - Hydrate -> Decrypt
// - Decrypt -> Ready
// - Any -> Suspended (handled by Suspend method)
func (m *Machine) validateTransition(from, to State) error {
	// Can't transition from Suspended
	if from == StateSuspended {
		return ErrAlreadySuspended
	}

	// Suspended transitions are handled by Suspend method
	if to == StateSuspended {
		return nil
	}

	// Check valid forward transitions
	validTransitions := map[State]State{
		StateBoot:      StateAuthorize,
		StateAuthorize: StateHydrate,
		StateHydrate:   StateDecrypt,
		StateDecrypt:   StateReady,
	}

	expected, ok := validTransitions[from]
	if !ok {
		return ErrInvalidTransition
	}

	if to != expected {
		return ErrInvalidTransition
	}

	return nil
}

// Status returns the current status of the state machine as a struct.
type Status struct {
	State     string        `json:"state"`
	AssetID   string        `json:"asset_id,omitempty"`
	Uptime    time.Duration `json:"uptime_ns"`
	UptimeStr string        `json:"uptime"`
	StartTime time.Time     `json:"start_time"`
}

// Status returns the current status of the state machine.
func (m *Machine) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	uptime := time.Since(m.startTime)
	return Status{
		State:     m.state.String(),
		AssetID:   m.assetID,
		Uptime:    uptime,
		UptimeStr: uptime.Truncate(time.Second).String(),
		StartTime: m.startTime,
	}
}
