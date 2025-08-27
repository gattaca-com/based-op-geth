package state

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// UnsealedBlockState represents the complete state of an unsealed block
// including both the StateDB and metadata, ensuring they stay synchronized
type UnsealedBlockState struct {
	// The StateDB containing account balances, storage, etc.
	StateDB *StateDB

	// Metadata about the unsealed block
	Metadata *types.UnsealedBlock
}

// ConcurrentStateDB provides thread-safe access to a StateDB with copy-on-write semantics
// and integrated unsealed block metadata management
type ConcurrentStateDB struct {
	mu sync.RWMutex

	// The base state that all snapshots are derived from
	baseState *UnsealedBlockState

	// Current working state for writes
	workingState *UnsealedBlockState

	// Snapshot counter for tracking versions
	snapshotCounter uint64

	// Channel for notifying when new snapshots are available
	snapshotChan chan *UnsealedBlockState

	// Flag indicating if the state is being modified
	isModifying atomic.Bool
}

// NewConcurrentStateDB creates a new concurrent StateDB wrapper
func NewConcurrentStateDB(baseState *StateDB, metadata *types.UnsealedBlock) *ConcurrentStateDB {
	return &ConcurrentStateDB{
		baseState: &UnsealedBlockState{
			StateDB:  baseState,
			Metadata: metadata,
		},
		workingState: &UnsealedBlockState{
			StateDB:  baseState.Copy(),
			Metadata: copyUnsealedBlock(metadata),
		},
		snapshotChan: make(chan *UnsealedBlockState, 100), // Buffer for multiple readers
	}
}

// copyUnsealedBlock creates a deep copy of an UnsealedBlock
func copyUnsealedBlock(ub *types.UnsealedBlock) *types.UnsealedBlock {
	if ub == nil {
		return nil
	}

	// Copy the Env
	var envCopy *types.Env
	if ub.Env != nil {
		envCopy = &types.Env{
			Number:                ub.Env.Number,
			ParentHash:            ub.Env.ParentHash,
			Beneficiary:           ub.Env.Beneficiary,
			Timestamp:             ub.Env.Timestamp,
			GasLimit:              ub.Env.GasLimit,
			Basefee:               ub.Env.Basefee,
			Difficulty:            ub.Env.Difficulty,
			Prevrandao:            ub.Env.Prevrandao,
			ExtraData:             append([]byte{}, ub.Env.ExtraData...),
			ParentBeaconBlockRoot: ub.Env.ParentBeaconBlockRoot,
		}
	}

	// Copy Frags
	fragsCopy := make([]types.Frag, len(ub.Frags))
	for i, frag := range ub.Frags {
		fragsCopy[i] = types.Frag{
			BlockNumber: frag.BlockNumber,
			Seq:         frag.Seq,
			IsLast:      frag.IsLast,
			Txs:         append([]*types.Transaction{}, frag.Txs...),
		}
	}

	// Copy LastSequenceNumber
	var lastSeqCopy *uint64
	if ub.LastSequenceNumber != nil {
		seq := *ub.LastSequenceNumber
		lastSeqCopy = &seq
	}

	// Copy Receipts
	receiptsCopy := make(types.Receipts, len(ub.Receipts))
	for i, receipt := range ub.Receipts {
		receiptCopy := *receipt
		receiptsCopy[i] = &receiptCopy
	}

	// Copy Logs
	logsCopy := make([]*types.Log, len(ub.Logs))
	for i, log := range ub.Logs {
		logCopy := *log
		logsCopy[i] = &logCopy
	}

	return &types.UnsealedBlock{
		Env:                   envCopy,
		Frags:                 fragsCopy,
		LastSequenceNumber:    lastSeqCopy,
		Hash:                  ub.Hash,
		Receipts:              receiptsCopy,
		Logs:                  logsCopy,
		CumulativeGasUsed:     ub.CumulativeGasUsed,
		CumulativeBlobGasUsed: ub.CumulativeBlobGasUsed,
	}
}

// GetReadSnapshot returns a read-only snapshot of the current state
// This is safe to call concurrently with writes
func (cs *ConcurrentStateDB) GetReadSnapshot() *UnsealedBlockState {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	// Create a copy of the current working state for reading
	snapshot := &UnsealedBlockState{
		StateDB:  cs.workingState.StateDB.Copy(),
		Metadata: copyUnsealedBlock(cs.workingState.Metadata),
	}

	// Increment snapshot counter
	atomic.AddUint64(&cs.snapshotCounter, 1)

	return snapshot
}

// BeginWriteTransaction starts a write transaction
// Only one write transaction can be active at a time
func (cs *ConcurrentStateDB) BeginWriteTransaction() (*UnsealedBlockState, error) {
	if !cs.isModifying.CompareAndSwap(false, true) {
		return nil, ErrConcurrentModification
	}

	cs.mu.Lock()

	// Create a new working state for this transaction
	cs.workingState = &UnsealedBlockState{
		StateDB:  cs.workingState.StateDB.Copy(),
		Metadata: copyUnsealedBlock(cs.workingState.Metadata),
	}

	return cs.workingState, nil
}

// CommitWriteTransaction commits the write transaction and makes it available to readers
func (cs *ConcurrentStateDB) CommitWriteTransaction() error {
	defer func() {
		cs.mu.Unlock()
		cs.isModifying.Store(false)
	}()

	// Notify readers that a new snapshot is available
	select {
	case cs.snapshotChan <- &UnsealedBlockState{
		StateDB:  cs.workingState.StateDB.Copy(),
		Metadata: copyUnsealedBlock(cs.workingState.Metadata),
	}:
	default:
		// Channel is full, readers will get the latest state on next read
		log.Debug("Snapshot notification channel full, readers will get latest state on next read")
	}

	return nil
}

// RollbackWriteTransaction rolls back the write transaction
func (cs *ConcurrentStateDB) RollbackWriteTransaction() {
	defer func() {
		cs.mu.Unlock()
		cs.isModifying.Store(false)
	}()

	// Restore the working state to the previous version
	cs.workingState = &UnsealedBlockState{
		StateDB:  cs.baseState.StateDB.Copy(),
		Metadata: copyUnsealedBlock(cs.baseState.Metadata),
	}
}

// GetLatestSnapshot returns the most recent snapshot available
func (cs *ConcurrentStateDB) GetLatestSnapshot() *UnsealedBlockState {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	// Try to get a snapshot from the channel first
	select {
	case snapshot := <-cs.snapshotChan:
		return snapshot
	default:
		// No snapshot in channel, return current working state
		return &UnsealedBlockState{
			StateDB:  cs.workingState.StateDB.Copy(),
			Metadata: copyUnsealedBlock(cs.workingState.Metadata),
		}
	}
}

// WaitForSnapshot waits for the next snapshot to become available
func (cs *ConcurrentStateDB) WaitForSnapshot() *UnsealedBlockState {
	snapshot := <-cs.snapshotChan
	return snapshot
}

// GetBaseState returns the base state (for internal use)
func (cs *ConcurrentStateDB) GetBaseState() *UnsealedBlockState {
	return cs.baseState
}

// GetWorkingState returns the current working state (for internal use)
func (cs *ConcurrentStateDB) GetWorkingState() *UnsealedBlockState {
	return cs.workingState
}

// IsModifying returns true if a write transaction is currently active
func (cs *ConcurrentStateDB) IsModifying() bool {
	return cs.isModifying.Load()
}

// GetSnapshotCounter returns the current snapshot counter
func (cs *ConcurrentStateDB) GetSnapshotCounter() uint64 {
	return atomic.LoadUint64(&cs.snapshotCounter)
}

// Close closes the concurrent StateDB and cleans up resources
func (cs *ConcurrentStateDB) Close() error {
	close(cs.snapshotChan)
	return nil
}

// Error definitions
var (
	ErrConcurrentModification = fmt.Errorf("concurrent modification not allowed")
)
