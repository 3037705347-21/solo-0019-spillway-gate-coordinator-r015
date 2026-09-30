package coordination

import (
	"context"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func (s *Service) RegisterGate(ctx context.Context, request contracts.RegisterGateRequest) (domain.Gate, error) {
	window, err := domain.NewAdjustmentWindow(
		request.AdjustmentWindowSeconds,
		request.AdjustmentWindowMaxTravel,
	)
	if err != nil {
		return domain.Gate{}, err
	}
	gate, err := domain.NewGate(
		request.ID,
		request.Name,
		request.MaxAperture,
		request.InitialAperture,
		window,
		s.now(),
	)
	if err != nil {
		return domain.Gate{}, err
	}
	if err := s.repository.CreateGate(ctx, gate); err != nil {
		return domain.Gate{}, err
	}
	return gate, nil
}

func (s *Service) Gate(ctx context.Context, id string) (domain.Gate, error) {
	return s.repository.Gate(ctx, id)
}

func (s *Service) AdjustmentWindow(
	ctx context.Context,
	gateID string,
) (domain.AdjustmentWindowStatus, error) {
	return s.repository.AdjustmentWindowStatus(ctx, gateID, s.now())
}
