// state.go — experiment lifecycle (F1). Every transition is validated;
// unknown jumps are programming errors, rejected loudly.
package faultlab

import "fmt"

// States per spec §6.3.
const (
	StCreated   = "CREATED"
	StPreflight = "PREFLIGHT"
	StBaseline  = "BASELINE"
	StInjecting = "INJECTING"
	StObserving = "OBSERVING"
	StCleaning  = "CLEANING"
	StVerifying = "VERIFYING"
	StPassed    = "PASSED"
	StFailed    = "FAILED"
	StCleanupF  = "CLEANUP_FAILED"
)

// Terminal states own no further transitions.
func Terminal(s string) bool {
	return s == StPassed || s == StFailed || s == StCleanupF
}

// transitions maps each state to its legal successors. Any active phase
// may go to CLEANING (error, abort, interruption); CLEANING that cannot
// verify restoration goes to CLEANUP_FAILED, never to a success state.
var transitions = map[string][]string{
	StCreated:   {StPreflight},
	StPreflight: {StBaseline, StCleaning},
	StBaseline:  {StInjecting, StCleaning},
	StInjecting: {StObserving, StCleaning},
	StObserving: {StCleaning},
	StCleaning:  {StVerifying, StCleanupF},
	StVerifying: {StPassed, StFailed},
}

// Next validates a transition.
func Next(from, to string) error {
	for _, ok := range transitions[from] {
		if to == ok {
			return nil
		}
	}
	return fmt.Errorf("illegal transition %s -> %s", from, to)
}
