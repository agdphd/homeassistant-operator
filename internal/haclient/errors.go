package haclient

import "fmt"

// ErrorType represents different error categories
type ErrorType string

const (
	ErrorTypeNotReady        ErrorType = "NotReady"        // HA not responding
	ErrorTypeOnboardingDone  ErrorType = "OnboardingDone"  // Already completed
	ErrorTypeHTTP            ErrorType = "HTTP"            // HTTP error
	ErrorTypeInvalidResponse ErrorType = "InvalidResponse" // Parse error
	ErrorTypeAuth            ErrorType = "Auth"            // Authentication error
)

// Error represents a Home Assistant API error
type Error struct {
	Type       ErrorType
	Message    string
	StatusCode int
	Err        error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

func (e *Error) Unwrap() error {
	return e.Err
}

// IsNotReady returns true if error is NotReady type
func IsNotReady(err error) bool {
	if haErr, ok := err.(*Error); ok {
		return haErr.Type == ErrorTypeNotReady
	}
	return false
}

// IsOnboardingDone returns true if onboarding already completed
func IsOnboardingDone(err error) bool {
	if haErr, ok := err.(*Error); ok {
		return haErr.Type == ErrorTypeOnboardingDone
	}
	return false
}
