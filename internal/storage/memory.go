package storage

import (
	"context"
	"sync"
	"time"

	"example.com/spillway-gate-coordinator/internal/domain"
)

type MemoryRepository struct {
	mu       sync.RWMutex
	gates    map[string]domain.Gate
	commands map[string]domain.GateCommand
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		gates:    make(map[string]domain.Gate),
		commands: make(map[string]domain.GateCommand),
	}
}

func (m *MemoryRepository) CreateGate(_ context.Context, gate domain.Gate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.gates[gate.ID]; exists {
		return domain.Conflict("duplicate_gate_id", "gate id already exists")
	}
	m.gates[gate.ID] = gate
	return nil
}

func (m *MemoryRepository) Gate(_ context.Context, id string) (domain.Gate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	gate, exists := m.gates[id]
	if !exists {
		return domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}
	return gate, nil
}

func (m *MemoryRepository) CreateCommand(_ context.Context, command domain.GateCommand, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.NotFound("gate_not_found", "gate id was not found")
	}
	if gate.Status == domain.GateFaulted {
		return domain.Conflict("gate_faulted", "faulted gates cannot receive new commands")
	}
	if command.StartingAperture != gate.Aperture {
		return domain.Conflict("gate_aperture_changed", "gate aperture changed before the command was stored")
	}
	if command.TargetAperture > gate.MaxAperture {
		return domain.Invalid("invalid_target_aperture", "target aperture is outside the gate limit")
	}
	if _, exists := m.commands[command.ID]; exists {
		return domain.Conflict("duplicate_command_id", "command id already exists")
	}

	// Window admission runs inside the same critical section that stores the
	// command, so concurrent requests can only see each other after commit.
	if err := m.admitAdjustment(gate, command, now); err != nil {
		return err
	}

	m.commands[command.ID] = command
	return nil
}

func (m *MemoryRepository) Command(_ context.Context, id string) (domain.GateCommand, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	command, exists := m.commands[id]
	if !exists {
		return domain.GateCommand{}, domain.NotFound("command_not_found", "command id was not found")
	}
	return cloneCommand(command), nil
}

func (m *MemoryRepository) AdjustmentWindowStatus(
	_ context.Context,
	gateID string,
	now time.Time,
) (domain.AdjustmentWindowStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	gate, exists := m.gates[gateID]
	if !exists {
		return domain.AdjustmentWindowStatus{}, domain.NotFound("gate_not_found", "gate id was not found")
	}
	return domain.NewAdjustmentWindowStatus(gate, m.gateCommandsLocked(gateID), now), nil
}

func (m *MemoryRepository) ReviewCommand(
	_ context.Context,
	commandID string,
	decision domain.InterlockDecision,
	issueToken func() (string, error),
	now time.Time,
) (domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	command, exists := m.commands[commandID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("command_not_found", "command id was not found")
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	// Guard first: a terminal command cannot be reviewed and must not trigger
	// credential issuance, reservation, or window admission.
	nextCommand := cloneCommand(command)
	if err := nextCommand.EnsureReviewable(); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	nextGate := gate
	if decision.IsClear() {
		// Reservation and window admission both succeed on local copies before
		// any credential is issued or anything is written; either failure
		// leaves the stored command pending and the gate untouched.
		if err := nextGate.Reserve(nextCommand.ID, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		if err := m.admitAdjustment(gate, command, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	}

	token := ""
	if decision.IsClear() {
		issued, err := issueToken()
		if err != nil {
			return domain.GateCommand{}, domain.Gate{}, domain.Internal(
				"execution_token_generation_failed",
				"execution token could not be generated",
			)
		}
		token = issued
	}

	if err := nextCommand.Review(decision, token, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	return cloneCommand(nextCommand), nextGate, nil
}

func (m *MemoryRepository) ExecuteCommand(
	_ context.Context,
	commandID string,
	token string,
	operator string,
	simulateFault bool,
	now time.Time,
) (domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	command, exists := m.commands[commandID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("command_not_found", "command id was not found")
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	nextCommand := cloneCommand(command)
	if err := nextCommand.BeginExecution(token, operator, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	nextGate := gate
	if err := nextGate.StartMovement(nextCommand.ID, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	if simulateFault {
		if err := nextGate.Settle(nextCommand.TargetAperture, true, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		if err := nextCommand.Abort("simulated_gate_fault", now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	} else {
		if err := nextGate.Settle(nextCommand.TargetAperture, false, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		if err := nextCommand.Complete(now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	}

	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	return cloneCommand(nextCommand), nextGate, nil
}

// admitAdjustment evaluates the rolling window purely from completed commands
// and rejects the candidate when its delta would cross the safety limit.
// Callers must hold m.mu.
func (m *MemoryRepository) admitAdjustment(
	gate domain.Gate,
	candidate domain.GateCommand,
	now time.Time,
) error {
	if !gate.AdjustmentWindow.Enabled() {
		return nil
	}
	status := domain.NewAdjustmentWindowStatus(gate, m.gateCommandsLocked(gate.ID), now)
	delta := domain.AdjustmentDelta(candidate.StartingAperture, candidate.TargetAperture)
	if gate.AdjustmentWindow.Admits(delta, status.Used) {
		return nil
	}
	return gate.AdjustmentWindow.Exceeded(delta, status.Used)
}

// gateCommandsLocked snapshots the commands of one gate for window
// evaluation. Callers must hold m.mu (read or write).
func (m *MemoryRepository) gateCommandsLocked(gateID string) []domain.GateCommand {
	commands := make([]domain.GateCommand, 0, len(m.commands))
	for _, command := range m.commands {
		if command.GateID == gateID {
			commands = append(commands, command)
		}
	}
	return commands
}

func cloneCommand(command domain.GateCommand) domain.GateCommand {
	if command.Interlock == nil {
		return command
	}
	decision := *command.Interlock
	command.Interlock = &decision
	return command
}
