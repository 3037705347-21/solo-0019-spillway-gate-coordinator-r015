package contracts

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type InterlockDecisionView struct {
	Verdict    string `json:"verdict"`
	Verifier   string `json:"verifier"`
	Note       string `json:"note"`
	ReviewedAt string `json:"reviewed_at"`
}

type GateView struct {
	ID                        string  `json:"id"`
	Name                      string  `json:"name"`
	Status                    string  `json:"status"`
	Aperture                  float64 `json:"aperture"`
	MaxAperture               float64 `json:"max_aperture"`
	ActiveCommandID           string  `json:"active_command_id,omitempty"`
	AdjustmentWindowSeconds   int64   `json:"adjustment_window_seconds,omitempty"`
	AdjustmentWindowMaxTravel float64 `json:"adjustment_window_max_travel,omitempty"`
	UpdatedAt                 string  `json:"updated_at"`
	Version                   int64   `json:"version"`
}

type CommandView struct {
	ID               string                 `json:"id"`
	GateID           string                 `json:"gate_id"`
	TargetAperture   float64                `json:"target_aperture"`
	StartingAperture float64                `json:"starting_aperture"`
	Reason           string                 `json:"reason"`
	RequestedBy      string                 `json:"requested_by"`
	Status           string                 `json:"status"`
	Interlock        *InterlockDecisionView `json:"interlock,omitempty"`
	ExecutionToken   string                 `json:"execution_token,omitempty"`
	ExecutedBy       string                 `json:"executed_by,omitempty"`
	TerminalReason   string                 `json:"terminal_reason,omitempty"`
	CreatedAt        string                 `json:"created_at"`
	UpdatedAt        string                 `json:"updated_at"`
	CompletedAt      string                 `json:"completed_at,omitempty"`
	Version          int64                  `json:"version"`
}

type AdjustmentFactView struct {
	CommandID        string  `json:"command_id"`
	StartingAperture float64 `json:"starting_aperture"`
	TargetAperture   float64 `json:"target_aperture"`
	ApertureDelta    float64 `json:"aperture_delta"`
	CompletedAt      string  `json:"completed_at"`
}

type AdjustmentWindowView struct {
	GateID          string               `json:"gate_id"`
	Enabled         bool                 `json:"enabled"`
	WindowSeconds   int64                `json:"window_seconds,omitempty"`
	MaxTravel       float64              `json:"max_travel,omitempty"`
	WindowStart     string               `json:"window_start,omitempty"`
	EvaluatedAt     string               `json:"evaluated_at"`
	UsedTravel      float64              `json:"used_travel"`
	RemainingTravel float64              `json:"remaining_travel"`
	Facts           []AdjustmentFactView `json:"facts"`
}

type RegisterGateResponse struct {
	Gate GateView `json:"gate"`
}

type GateResponse struct {
	Gate GateView `json:"gate"`
}

type CommandResponse struct {
	Command CommandView `json:"command"`
}

type ReviewCommandResponse struct {
	Command CommandView `json:"command"`
	Gate    GateView    `json:"gate"`
}

type ExecuteCommandResponse struct {
	Command CommandView `json:"command"`
	Gate    GateView    `json:"gate"`
}

type AdjustmentWindowResponse struct {
	Window AdjustmentWindowView `json:"window"`
}
