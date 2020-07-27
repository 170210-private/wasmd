package keeper

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type Recurse struct {
	Depth    uint32         `json:"depth"`
	Work     uint32         `json:"work"`
	Contract sdk.AccAddress `json:"contract"`
}

type recurseWrapper struct {
	Recurse Recurse `json:"recurse"`
}

func buildQuery(t *testing.T, msg Recurse) []byte {
	wrapper := recurseWrapper{Recurse: msg}
	bz, err := json.Marshal(wrapper)
	require.NoError(t, err)
	return bz
}

type recurseResponse struct {
	Hashed []byte `json:"hashed"`
}

func TestGasCostOnQuery(t *testing.T) {
	cases := map[string]struct {
		gasLimit uint64
		msg      Recurse
	}{
		"no recursion, no work": {
			gasLimit: 400_000,
			msg:      Recurse{},
		},
		"no recursion, some work": {
			gasLimit: 400_000,
			msg: Recurse{
				Work: 5, // 5 rounds of sha256 inside the contract
			},
		},
	}

	// we do one basic setup before all test cases (which are read-only and don't change state)
	tempDir, err := ioutil.TempDir("", "wasm")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	ctx, keepers := CreateTestInput(t, false, tempDir, SupportedFeatures, nil, nil)
	accKeeper, keeper := keepers.AccountKeeper, keepers.WasmKeeper
	deposit := sdk.NewCoins(sdk.NewInt64Coin("denom", 100000))
	creator := createFakeFundedAccount(ctx, accKeeper, deposit.Add(deposit...))

	// store the code
	wasmCode, err := ioutil.ReadFile("./testdata/contract.wasm")
	require.NoError(t, err)
	codeID, err := keeper.Create(ctx, creator, wasmCode, "", "", nil)
	require.NoError(t, err)

	// instantiate the contract
	_, _, bob := keyPubAddr()
	_, _, fred := keyPubAddr()
	initMsg := InitMsg{
		Verifier:    fred,
		Beneficiary: bob,
	}
	initMsgBz, err := json.Marshal(initMsg)
	require.NoError(t, err)
	contractAddr, err := keeper.Instantiate(ctx, codeID, creator, nil, initMsgBz, "recursive contract", deposit)
	require.NoError(t, err)

	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			// make sure we set a limit before calling
			ctx = ctx.WithGasMeter(sdk.NewGasMeter(tc.gasLimit))
			require.Equal(t, uint64(0), ctx.GasMeter().GasConsumed())

			recurse := tc.msg
			recurse.Contract = contractAddr
			msg := buildQuery(t, recurse)

			// this should throw out of gas exception (panic)
			data, err := keeper.QuerySmart(ctx, contractAddr, msg)
			require.NoError(t, err)
			var resp recurseResponse
			err = json.Unmarshal(data, &resp)
			require.NoError(t, err)

			// TODO: assert result? - now just that it is 32 byte sha256 hash (or contractAddr if no hash)
			if recurse.Work == 0 {
				assert.Equal(t, len(resp.Hashed), len(creator.String()))
			} else {
				assert.Equal(t, len(resp.Hashed), 32)
			}
		})
	}
}
