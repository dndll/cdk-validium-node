package e2e

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	cTypes "github.com/0xPolygon/cdk-data-availability/config/types"
	"github.com/0xPolygonHermez/zkevm-node/etherman/smartcontracts/etrogpolygonzkevm"
	"github.com/0xPolygonHermez/zkevm-node/etherman/smartcontracts/nearda"
	"github.com/0xPolygonHermez/zkevm-node/log"
	"github.com/0xPolygonHermez/zkevm-node/test/operations"
	"github.com/ethereum/go-ethereum"
	eTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/near/rollup-data-availability/gopkg/sidecar"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	NearDaContract = "0x9A9f2CCfdE556A7E9Ff0848998Aa4a0CFD8863AE" // TODO: set a contract to be deployed at genesis
	NearSK         = "ed25519:4dagBsEqCv3Ao5wa4KKFa57xNAH4wuBjh9wdTNYeCqDSeA9zE7fCnHSvWpU8t68jUpcCGqgfYwcH68suPaqmdcgm"
	NearAccount    = "test.net"
)

func TestNearDa(t *testing.T) {
	const (
		ksFile             = "/tmp/pkey"
		ksPass             = "pass"
		cfgFile            = "/tmp/neardanodeconfigfile.json"
		nearDaSidecarImage = "ghcr.io/near/rollup-data-availability/http-api:dev"
	)

	// Setup
	var err error
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	defer func() {
		require.NoError(t, operations.Teardown())
	}()
	err = operations.Teardown()
	require.NoError(t, err)
	opsCfg := operations.GetDefaultOperationsConfig()
	opsCfg.State.MaxCumulativeGasUsed = 80000000000
	opsman, err := operations.NewManager(ctx, opsCfg)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, opsman.StopDACDB())
	}()
	err = opsman.Setup()
	require.NoError(t, err)
	require.NoError(t, opsman.StartDACDB())
	time.Sleep(5 * time.Second)

	authL2, err := operations.GetAuth(operations.DefaultSequencerPrivateKey, operations.DefaultL2ChainID)
	require.NoError(t, err)

	authL1, err := operations.GetAuth(operations.DefaultSequencerPrivateKey, operations.DefaultL1ChainID)
	require.NoError(t, err)

	clientL2, err := ethclient.Dial(operations.DefaultL2NetworkURL)
	require.NoError(t, err)

	clientL1, err := ethclient.Dial(operations.DefaultL1NetworkURL)
	require.NoError(t, err)

	zkEVM, err := etrogpolygonzkevm.NewEtrogpolygonzkevm(
		common.HexToAddress(operations.DefaultL1ZkEVMSmartContract),
		clientL1,
	)
	require.NoError(t, err)

	currentDAPAddr, err := zkEVM.DataAvailabilityProtocol(&bind.CallOpts{Pending: false})
	require.NoError(t, err)
	require.Equal(t, common.HexToAddress(operations.DefaultL1DataCommitteeContract), currentDAPAddr)

	_, err = zkEVM.SetDataAvailabilityProtocol(authL1, "0xNearProtocol")
	require.NoError(t, err)

	// TODO: SetDataAvailabilityProtocol on etrog

	// TODO: Ensure DA sidecar is setup
	// TODO: Ensure contract is setup too

	// TODO: create dummy near accounts, get them funded by workspace node

	defer func() {
		// Remove tmp files
		assert.NoError(t,
			exec.Command("rm", cfgFile).Run(),
		)
		assert.NoError(t,
			exec.Command("rmdir", ksFile+"_").Run(),
		)
		assert.NoError(t,
			exec.Command("rm", ksFile).Run(),
		)
		// TODO: stop near localnet
		assert.NoError(t, exec.Command(
			"docker", "kill", "near-da-sidecar",
		).Run())
		assert.NoError(t, exec.Command(
			"docker", "rm", "near-da-sidecar",
		).Run())
		// Stop permissionless node
		require.NoError(t, opsman.StopPermissionlessNodeForcedToSyncThroughNear())
	}()
	// Start permissionless node
	require.NoError(t, opsman.StartPermissionlessNodeForcedToSyncThroughNear())

	// Write config file
	nearDaConfig := sidecar.ConfigureClientRequest{
		AccountID: NearAccount,
		SecretKey: NearSK,
		Network:   "near-localnet:5888", // TODO: needs the arbitrary networks
		Namespace: nil,
	}

	file, err := json.MarshalIndent(nearDaConfig, "", " ")
	require.NoError(t, err)
	err = os.WriteFile(cfgFile, file, 0644)
	require.NoError(t, err)

	// Run DAC node
	cmd := exec.Command(
		"docker", "run", "-d",
		"--name", "near-da-sidecar",
		"-v", cfgFile+":/var/config.json",
		"--network", "zkevm",
		nearDaSidecarImage,
		"-c", "/var/config.json",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	log.Infof("NEAR DA sidecar started")
	time.Sleep(time.Second * 5)

	// Send txs
	nTxs := 10
	amount := big.NewInt(10000)
	toAddress := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	_, err = clientL2.BalanceAt(ctx, authL2.From, nil)
	require.NoError(t, err)
	_, err = clientL2.PendingNonceAt(ctx, authL2.From)
	require.NoError(t, err)

	gasLimit, err := clientL2.EstimateGas(ctx, ethereum.CallMsg{From: authL2.From, To: &toAddress, Value: amount})
	require.NoError(t, err)

	gasPrice, err := clientL2.SuggestGasPrice(ctx)
	require.NoError(t, err)

	nonce, err := clientL2.PendingNonceAt(ctx, authL2.From)
	require.NoError(t, err)

	txs := make([]*eTypes.Transaction, 0, nTxs)
	for i := 0; i < nTxs; i++ {
		tx := eTypes.NewTransaction(nonce+uint64(i), toAddress, amount, gasLimit, gasPrice, nil)
		log.Infof("generating tx %d / %d: %s", i+1, nTxs, tx.Hash().Hex())
		txs = append(txs, tx)
	}

	// Wait for verification
	_, err = operations.ApplyL2Txs(ctx, txs, authL2, clientL2, operations.VerifiedConfirmationLevel)
	require.NoError(t, err)

	// Assert that he permissionless node is fully synced (through the DAC)
	time.Sleep(30 * time.Second) // Give some time for the permissionless node to get synced
	clientL2Permissionless, err := ethclient.Dial(operations.PermissionlessL2NetworkURL)
	require.NoError(t, err)
	expectedBlock, err := clientL2.BlockByNumber(ctx, nil)
	require.NoError(t, err)
	actualBlock, err := clientL2Permissionless.BlockByNumber(ctx, nil)
	require.NoError(t, err)
	// je, err := expectedBlock.Header().MarshalJSON()
	// require.NoError(t, err)
	// log.Info(string(je))
	// ja, err := actualBlock.Header().MarshalJSON()
	// require.NoError(t, err)
	// log.Info(string(ja))
	// require.Equal(t, string(je), string(ja))
	require.Equal(t, expectedBlock.Root().Hex(), actualBlock.Root().Hex())
}

func createKeyStore(pk *ecdsa.PrivateKey, outputDir, password string) error {
	ks := keystore.NewKeyStore(outputDir+"_", keystore.StandardScryptN, keystore.StandardScryptP)
	_, err := ks.ImportECDSA(pk, password)
	if err != nil {
		return err
	}
	fileNameB, err := exec.Command("ls", outputDir+"_/").CombinedOutput()
	fileName := strings.TrimSuffix(string(fileNameB), "\n")
	if err != nil {
		fmt.Println(fileName)
		return err
	}
	out, err := exec.Command("mv", outputDir+"_/"+fileName, outputDir).CombinedOutput()
	if err != nil {
		fmt.Println(string(out))
		return err
	}
	return nil
}
