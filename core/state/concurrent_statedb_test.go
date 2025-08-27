package state

import (
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/assert"
)

func TestConcurrentStateDB_BasicOperations(t *testing.T) {
	// Create a base state
	baseState, err := New(common.Hash{}, NewDatabaseForTesting())
	assert.NoError(t, err)

	// Set some initial data
	addr := common.HexToAddress("0x123")
	baseState.SetBalance(addr, uint256.NewInt(100), tracing.BalanceChangeUnspecified)

	// Create concurrent StateDB
	cs := NewConcurrentStateDB(baseState)
	defer cs.Close()

	// Test read snapshot
	snapshot := cs.GetReadSnapshot()
	assert.NotNil(t, snapshot)
	assert.Equal(t, uint256.NewInt(100), snapshot.GetBalance(addr))

	// Test write transaction
	workingState, err := cs.BeginWriteTransaction()
	assert.NoError(t, err)
	assert.True(t, cs.IsModifying())

	// Modify the working state
	workingState.SetBalance(addr, uint256.NewInt(200), tracing.BalanceChangeUnspecified)

	// Commit the transaction
	err = cs.CommitWriteTransaction()
	assert.NoError(t, err)
	assert.False(t, cs.IsModifying())

	// Verify the change is visible in new snapshots
	newSnapshot := cs.GetReadSnapshot()
	assert.Equal(t, uint256.NewInt(200), newSnapshot.GetBalance(addr))

	// Original snapshot should still show old value
	assert.Equal(t, uint256.NewInt(100), snapshot.GetBalance(addr))
}

func TestConcurrentStateDB_ConcurrentReads(t *testing.T) {
	// Create a base state
	baseState, err := New(common.Hash{}, NewDatabaseForTesting())
	assert.NoError(t, err)

	// Set some initial data
	addr := common.HexToAddress("0x123")
	baseState.SetBalance(addr, uint256.NewInt(100), tracing.BalanceChangeUnspecified)

	// Create concurrent StateDB
	cs := NewConcurrentStateDB(baseState)
	defer cs.Close()

	// Test concurrent reads
	var wg sync.WaitGroup
	readCount := 100

	for i := 0; i < readCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot := cs.GetReadSnapshot()
			assert.Equal(t, uint256.NewInt(100), snapshot.GetBalance(addr))
		}()
	}

	wg.Wait()
}

func TestConcurrentStateDB_WriteTransactionExclusivity(t *testing.T) {
	// Create a base state
	baseState, err := New(common.Hash{}, NewDatabaseForTesting())
	assert.NoError(t, err)

	// Create concurrent StateDB
	cs := NewConcurrentStateDB(baseState)
	defer cs.Close()

	// Start first write transaction
	_, err = cs.BeginWriteTransaction()
	assert.NoError(t, err)
	assert.True(t, cs.IsModifying())

	// Try to start second write transaction (should fail)
	workingState2, err := cs.BeginWriteTransaction()
	assert.Error(t, err)
	assert.Equal(t, ErrConcurrentModification, err)
	assert.Nil(t, workingState2)

	// Commit first transaction
	err = cs.CommitWriteTransaction()
	assert.NoError(t, err)
	assert.False(t, cs.IsModifying())

	// Now should be able to start second transaction
	_, err = cs.BeginWriteTransaction()
	assert.NoError(t, err)
	assert.True(t, cs.IsModifying())

	// Clean up
	cs.RollbackWriteTransaction()
}

func TestConcurrentStateDB_Rollback(t *testing.T) {
	// Create a base state
	baseState, err := New(common.Hash{}, NewDatabaseForTesting())
	assert.NoError(t, err)

	// Set initial data
	addr := common.HexToAddress("0x123")
	baseState.SetBalance(addr, uint256.NewInt(100), tracing.BalanceChangeUnspecified)

	// Create concurrent StateDB
	cs := NewConcurrentStateDB(baseState)
	defer cs.Close()

	// Start write transaction
	workingState, err := cs.BeginWriteTransaction()
	assert.NoError(t, err)

	// Modify state
	workingState.SetBalance(addr, uint256.NewInt(200), tracing.BalanceChangeUnspecified)

	// Rollback
	cs.RollbackWriteTransaction()
	assert.False(t, cs.IsModifying())

	// Verify rollback worked
	snapshot := cs.GetReadSnapshot()
	assert.Equal(t, uint256.NewInt(100), snapshot.GetBalance(addr))
}

func TestConcurrentStateDB_SnapshotNotifications(t *testing.T) {
	// Create a base state
	baseState, err := New(common.Hash{}, NewDatabaseForTesting())
	assert.NoError(t, err)

	// Create concurrent StateDB
	cs := NewConcurrentStateDB(baseState)
	defer cs.Close()

	// Start write transaction
	workingState, err := cs.BeginWriteTransaction()
	assert.NoError(t, err)

	// Modify state
	addr := common.HexToAddress("0x123")
	workingState.SetBalance(addr, uint256.NewInt(200), tracing.BalanceChangeUnspecified)

	// Start goroutine to wait for snapshot
	var receivedSnapshot *StateDB
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		receivedSnapshot = cs.WaitForSnapshot()
	}()

	// Give some time for goroutine to start
	time.Sleep(10 * time.Millisecond)

	// Commit transaction
	err = cs.CommitWriteTransaction()
	assert.NoError(t, err)

	// Wait for snapshot to be received
	wg.Wait()

	// Verify snapshot was received
	assert.NotNil(t, receivedSnapshot)
	assert.Equal(t, uint256.NewInt(200), receivedSnapshot.GetBalance(addr))
}

func TestConcurrentStateDB_SnapshotCounter(t *testing.T) {
	// Create a base state
	baseState, err := New(common.Hash{}, NewDatabaseForTesting())
	assert.NoError(t, err)

	// Create concurrent StateDB
	cs := NewConcurrentStateDB(baseState)
	defer cs.Close()

	initialCounter := cs.GetSnapshotCounter()

	// Get a few snapshots
	cs.GetReadSnapshot()
	cs.GetReadSnapshot()
	cs.GetReadSnapshot()

	finalCounter := cs.GetSnapshotCounter()
	assert.Equal(t, initialCounter+3, finalCounter)
}
