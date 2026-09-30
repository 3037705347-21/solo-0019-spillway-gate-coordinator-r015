package httpapi

import (
	"time"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func gateView(gate domain.Gate) contracts.GateView {
	view := contracts.GateView{
		ID:              gate.ID,
		Name:            gate.Name,
		Status:          string(gate.Status),
		Aperture:        gate.Aperture,
		MaxAperture:     gate.MaxAperture,
		ActiveCommandID: gate.ActiveCommandID,
		UpdatedAt:       formatTime(gate.UpdatedAt),
		Version:         gate.Version,
	}
	if gate.AdjustmentWindow.Enabled() {
		view.AdjustmentWindowSeconds = int64(gate.AdjustmentWindow.Duration / time.Second)
		view.AdjustmentWindowMaxTravel = gate.AdjustmentWindow.MaxAdjustment
	}
	return view
}

func commandView(command domain.GateCommand, includeToken bool) contracts.CommandView {
	view := contracts.CommandView{
		ID:               command.ID,
		GateID:           command.GateID,
		TargetAperture:   command.TargetAperture,
		StartingAperture: command.StartingAperture,
		Reason:           command.Reason,
		RequestedBy:      command.RequestedBy,
		Status:           string(command.Status),
		ExecutedBy:       command.ExecutedBy,
		TerminalReason:   command.TerminalReason,
		CreatedAt:        formatTime(command.CreatedAt),
		UpdatedAt:        formatTime(command.UpdatedAt),
		Version:          command.Version,
	}
	if !command.CompletedAt.IsZero() {
		view.CompletedAt = formatTime(command.CompletedAt)
	}

	if includeToken && command.Status == domain.CommandApproved {
		view.ExecutionToken = command.ExecutionToken
	}
	if command.Interlock != nil {
		view.Interlock = &contracts.InterlockDecisionView{
			Verdict:    string(command.Interlock.Verdict),
			Verifier:   command.Interlock.Verifier,
			Note:       command.Interlock.Note,
			ReviewedAt: formatTime(command.Interlock.ReviewedAt),
		}
	}
	return view
}

func adjustmentWindowView(status domain.AdjustmentWindowStatus) contracts.AdjustmentWindowView {
	view := contracts.AdjustmentWindowView{
		GateID:      status.GateID,
		Enabled:     status.Window.Enabled(),
		EvaluatedAt: formatTime(status.EvaluatedAt),
		UsedTravel:  status.Used,
		Facts:       []contracts.AdjustmentFactView{},
	}
	if status.Window.Enabled() {
		view.WindowSeconds = int64(status.Window.Duration / time.Second)
		view.MaxTravel = status.Window.MaxAdjustment
		view.RemainingTravel = status.Headroom()
		view.WindowStart = formatTime(status.WindowStart)
		for _, fact := range status.Facts {
			view.Facts = append(view.Facts, contracts.AdjustmentFactView{
				CommandID:        fact.CommandID,
				StartingAperture: fact.StartingAperture,
				TargetAperture:   fact.TargetAperture,
				ApertureDelta:    fact.ApertureDelta,
				CompletedAt:      formatTime(fact.CompletedAt),
			})
		}
	}
	return view
}
