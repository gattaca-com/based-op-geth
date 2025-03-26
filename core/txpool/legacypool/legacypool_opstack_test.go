// Copyright 2025 The op-geth Authors
// This file is part of the op-geth library.
//
// The op-geth library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The op-geth library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the op-geth library. If not, see <http://www.gnu.org/licenses/>.

package legacypool

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func setupOPStackPool() (*LegacyPool, *ecdsa.PrivateKey) {
	return setupPoolWithConfig(params.OptimismTestConfig)
}

func TestInvalidRollupTransactions(t *testing.T) {
	t.Run("zero-rollup-cost", func(t *testing.T) {
		testInvalidRollupTransactions(t, nil)
	})

	t.Run("l1-cost", func(t *testing.T) {
		testInvalidRollupTransactions(t, func(t *testing.T, pool *LegacyPool) {
			l1FeeScalars := common.Hash{19: 1} // smallest possible base fee scalar
			// sanity check
			l1BaseFeeScalar, l1BlobBaseFeeScalar := types.ExtractEcotoneFeeParams(l1FeeScalars[:])
			require.EqualValues(t, 1, l1BaseFeeScalar.Uint64())
			require.Zero(t, l1BlobBaseFeeScalar.Sign())
			pool.currentState.SetState(types.L1BlockAddr, types.L1FeeScalarsSlot, l1FeeScalars)
			l1BaseFee := big.NewInt(1e6) // to account for division by 1e12 in L1 cost
			pool.currentState.SetState(types.L1BlockAddr, types.L1BaseFeeSlot, common.BigToHash(l1BaseFee))
			// sanity checks
			require.Equal(t, l1BaseFee, pool.currentState.GetState(types.L1BlockAddr, types.L1BaseFeeSlot).Big())
		})
	})

	t.Run("operator-cost", func(t *testing.T) {
		testInvalidRollupTransactions(t, func(t *testing.T, pool *LegacyPool) {
			const opFeeConst = 1                       // smallest possible operator fee constant
			opFeeParams := common.Hash{31: opFeeConst} // const of 1, scalar of 0
			// sanity check
			s, c := types.ExtractOperatorFeeParams(opFeeParams)
			require.Zero(t, s.Sign())
			require.EqualValues(t, opFeeConst, c.Uint64())
			pool.currentState.SetState(types.L1BlockAddr, types.OperatorFeeParamsSlot, opFeeParams)
		})
	})
}

func testInvalidRollupTransactions(t *testing.T, stateMod func(t *testing.T, pool *LegacyPool)) {
	t.Parallel()

	pool, key := setupOPStackPool()
	defer pool.Close()

	const gasLimit = 100_000
	tx := transaction(0, gasLimit, key)
	from, _ := deriveSender(tx)

	// base fee is 1
	testAddBalance(pool, from, new(big.Int).Add(big.NewInt(gasLimit), tx.Value()))
	pool.reset(nil, nil)
	// we add the test variant with zero rollup cost as a sanity check that the tx would indeed be valid
	if stateMod == nil {
		require.NoError(t, pool.addRemote(tx))
		return
	}

	// Now we cause insufficient funds error due to rollup cost
	stateMod(t, pool)

	rcost := pool.rollupCostFn(tx)
	require.Equal(t, 1, rcost.Sign(), "rollup cost must be >0")

	require.ErrorIs(t, pool.addRemote(tx), core.ErrInsufficientFunds)
}
