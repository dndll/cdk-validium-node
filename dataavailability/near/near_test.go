// Tests

package near

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/near/rollup-data-availability/gopkg/sidecar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostSequence(t *testing.T) {
	// Set up a mock server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/blob":
			var blob sidecar.Blob
			err := json.NewDecoder(r.Body).Decode(&blob)
			require.NoError(t, err)
			transactionID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			w.Write([]byte(transactionID))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	backend, err := New(mockServer.URL, nil)
	backend.Init()
	require.NoError(t, err)
	defer backend.Client.Close()

	// Test PostSequence
	batchesData := GenerateRandomBatchesData(3, 10)
	transactionID, err := backend.PostSequence(context.Background(), batchesData)
	require.NoError(t, err)
	expectedTransactionID, err := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	assert.Equal(t, expectedTransactionID, transactionID)
}

func TestGetSequence(t *testing.T) {
	tx := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	data := []byte("test_data")
	// Set up a mock server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/blob":
			transactionID := r.URL.Query().Get("transaction_id")
			assert.Equal(t, tx, transactionID)
			blob := sidecar.Blob{Data: data}
			jsonData, err := blob.MarshalJSON()
			require.NoError(t, err)
			w.Write(jsonData)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	backend, err := New(mockServer.URL, nil)
	require.NoError(t, err)
	defer backend.Client.Close()

	batchHashes := []common.Hash{
		common.HexToHash(tx),
	}
	dataAvailabilityMessage := []byte("data_availability_message")
	batchData, err := backend.GetSequence(context.Background(), batchHashes, dataAvailabilityMessage)
	require.NoError(t, err)
	assert.Equal(t, 1, len(batchData))
	assert.Equal(t, data, batchData[0])

}

// GenerateRandomBlobData generates random blob data of the specified size.
func GenerateRandomBlobData(size int) []byte {
	data := make([]byte, size)
	for i := 0; i < size; i++ {
		data[i] = byte(i % 256)
	}
	return data
}

// GenerateRandomBatchesData generates a slice of random batch data.
func GenerateRandomBatchesData(numBatches, batchSize int) [][]byte {
	batchesData := make([][]byte, numBatches)
	for i := 0; i < numBatches; i++ {
		batchesData[i] = GenerateRandomBlobData(batchSize)
	}
	return batchesData
}
