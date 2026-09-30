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
	return contracts.GateView{
		ID:                  gate.ID,
		Name:                gate.Name,
		Status:              string(gate.Status),
		Aperture:            gate.Aperture,
		MaxAperture:         gate.MaxAperture,
		ActiveCommandID:     gate.ActiveCommandID,
		AdjustmentWindow:    int64(gate.AdjustmentWindow / time.Second),
		MaxWindowAdjustment: gate.MaxWindowAdjustment,
		UpdatedAt:           formatTime(gate.UpdatedAt),
		Version:             gate.Version,
	}
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
		CompletedAt:      formatTime(command.CompletedAt),
		Version:          command.Version,
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
	if !command.CompletedAt.IsZero() {
		view.CompletedAt = formatTime(command.CompletedAt)
	}
	return view
}

func adjustmentWindowView(window domain.AdjustmentWindow) contracts.AdjustmentWindowView {
	view := contracts.AdjustmentWindowView{
		GateID:        window.GateID,
		Enabled:       window.Enabled,
		WindowSeconds: int64(window.Window / time.Second),
		MaxAdjustment: window.MaxAdjustment,
		At:            formatTime(window.At),
		Used:          window.Used,
		Remaining:     window.Remaining,
		Facts:         make([]contracts.AdjustmentFactView, 0, len(window.Facts)),
	}
	if window.Enabled {
		view.WindowStart = formatTime(window.WindowStart)
	}
	if !window.NextReleaseAt.IsZero() {
		view.NextReleaseAt = formatTime(window.NextReleaseAt)
		view.NextReleaseAmount = window.NextReleaseAmount
	}
	for _, fact := range window.Facts {
		view.Facts = append(view.Facts, contracts.AdjustmentFactView{
			CommandID:   fact.CommandID,
			Starting:    fact.StartingAt,
			Target:      fact.Target,
			Amount:      fact.Amount,
			CompletedAt: formatTime(fact.CompletedAt),
			ReleasedAt:  formatTime(fact.ReleasedAt),
		})
	}
	return view
}
