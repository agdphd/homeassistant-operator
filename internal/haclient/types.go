package haclient

// OnboardingStatus represents the response from /api/onboarding
// When onboarding is needed, HA returns an array of steps
// When onboarding is done, HA returns an object with step statuses
type OnboardingStatus struct {
	IsArray bool
	Steps   []OnboardingStep
	Status  map[string]StepStatus
}

// OnboardingStep represents a step in the onboarding process
type OnboardingStep struct {
	Step string `json:"step"`
	Done bool   `json:"done"`
}

// StepStatus represents the status of an onboarding step
type StepStatus struct {
	Step string `json:"step"`
	Done bool   `json:"done"`
}

// CreateUserRequest represents POST /api/onboarding/users request
type CreateUserRequest struct {
	ClientID string `json:"client_id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
	Language string `json:"language"`
}

// CreateUserResponse represents POST /api/onboarding/users response
type CreateUserResponse struct {
	AuthCode string `json:"auth_code"`
}

// TokenRequest represents form data for POST /auth/token
type TokenRequest struct {
	GrantType string
	Code      string
	ClientID  string
}

// TokenResponse represents POST /auth/token response
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// LongLivedTokenRequest represents POST /api/auth/long_lived_access_token request
type LongLivedTokenRequest struct {
	ClientName string `json:"client_name"`
	Lifespan   int    `json:"lifespan"` // days
}

// LongLivedTokenResponse is returned as plain string
type LongLivedTokenResponse struct {
	Token string
}

// CoreConfigRequest represents POST /api/onboarding/core_config request
type CoreConfigRequest struct {
	LocationName string  `json:"location_name,omitempty"`
	Latitude     float64 `json:"latitude,omitempty"`
	Longitude    float64 `json:"longitude,omitempty"`
	Elevation    int     `json:"elevation,omitempty"`
	UnitSystem   string  `json:"unit_system,omitempty"` // "metric" or "us_customary"
	Currency     string  `json:"currency,omitempty"`
	TimeZone     string  `json:"time_zone,omitempty"`
}

// AnalyticsRequest represents POST /api/onboarding/analytics request
type AnalyticsRequest struct {
	// Empty body for disabling analytics, or can include preferences field
	Preferences *AnalyticsPreferences `json:"preferences,omitempty"`
}

// AnalyticsPreferences defines analytics preferences
type AnalyticsPreferences struct {
	Base        bool `json:"base,omitempty"`
	Diagnostics bool `json:"diagnostics,omitempty"`
	Usage       bool `json:"usage,omitempty"`
	Statistics  bool `json:"statistics,omitempty"`
}

// ConfigResponse represents GET /api/config response
// Contains Home Assistant configuration including loaded components
type ConfigResponse struct {
	Components            []string `json:"components"`
	Version               string   `json:"version"`
	LocationName          string   `json:"location_name"`
	TimeZone              string   `json:"time_zone"`
	ConfigDir             string   `json:"config_dir"`
	WhitelistExternalDirs []string `json:"whitelist_external_dirs"`
}

// ConfigEntry represents a config entry from GET /api/config/config_entries/entry
type ConfigEntry struct {
	EntryID string `json:"entry_id"`
	Domain  string `json:"domain"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Source  string `json:"source"`
}

// FlowResponse represents a response from Config Entry Flow API
type FlowResponse struct {
	FlowID  string      `json:"flow_id"`
	Type    string      `json:"type"`
	Title   string      `json:"title"`
	Result  interface{} `json:"result"`
	Version int         `json:"version"`
}

// FlowField represents a field in a config flow step
type FlowField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
}
