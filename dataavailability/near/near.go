// Package near provides a backend implementation for interacting with the NEAR DA.
//
// The NEAR DA backend allows posting and retrieving sequences of batches using the Near blockchain.
// It utilizes the NEAR DA Sidecar service for submitting and retrieving data blobs.
//
// Usage:
//
//	backend, err := near.New("http://localhost:5888", &sidecar.ConfigureClientRequest{...})
//	if err != nil {
//	    // Handle error
//	}
//
//	// Post a sequence of batches
//	batchesData := [][]byte{...}
//	transactionID, err := backend.PostSequence(context.Background(), batchesData)
//	if err != nil {
//	    // Handle error
//	}
//
//	// Get a sequence of batches
//	batchHashes := []common.Hash{...}
//	dataAvailabilityMessage := []byte{...}
//	batchData, err := backend.GetSequence(context.Background(), batchHashes, dataAvailabilityMessage)
//	if err != nil {
//	    // Handle error
//	}
//
// The package also provides logging functionality using the "github.com/0xPolygonHermez/zkevm-node/log" package.
//
// Dependencies:
//
//	github.com/near/rollup-data-availability/gopkg/sidecar
package near

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/0xPolygonHermez/zkevm-node/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/near/rollup-data-availability/gopkg/sidecar"
)

// NearProtocolBackend represents a backend for interacting with NEAR DA.
type NearProtocolBackend struct {
	host   string
	Client *sidecar.Client
	Config *sidecar.ConfigureClientRequest
}

// New creates a new instance of the NearProtocolBackend.
// It takes the host and configuration as parameters and returns a pointer to the backend.
func New(host string, config *sidecar.ConfigureClientRequest) (*NearProtocolBackend, error) {
	if config == nil {
		log.Debug("no near config, assuming sidecar setup outside of CDK")
	}
	return &NearProtocolBackend{
		host:   host,
		Config: config,
	}, nil
}

// Init loads the DAC to be cached when needed
func (d *NearProtocolBackend) Init() error {
	client, err := sidecar.NewClient(d.host, d.Config)
	if err != nil {
		return fmt.Errorf("error connecting to NEAR: %s", err)
	}

	d.Client = client
	return nil
}

func ParseConfig(accountId, secretKey, contractId, network string) sidecar.ConfigureClientRequest {
	sidecarConfig := sidecar.ConfigureClientRequest{
		AccountID:  accountId,
		SecretKey:  secretKey,
		ContractID: contractId,
		Network:    sidecar.Network(network),
	}
	return sidecarConfig
}

type Sequence struct {
	Batches [][]byte
}

func hexEncode(b [][]byte) string {
	var str string = "0x"
	for _, batch := range b {
		str += fmt.Sprintf("%x", batch)
	}
	return str
}

func hexEncode2(b []byte) string {
	return fmt.Sprintf("0x%x", b)
}

// PostSequence posts a sequence of batches to the Near blockchain.
// It takes a context and a slice of byte slices representing the batches data.
// It returns the transaction ID of the submitted sequence and any error encountered.
func (n *NearProtocolBackend) PostSequence(ctx context.Context, batches [][]byte) ([]byte, error) {
	const maxBatchSize = 4 * 1024 * 1024 // Max batch size is 4MB
	log.Debugf("submitting batches %s", hexEncode(batches))

	// count the size of all batches, overflowing batches into multiple sequences
	var sequences []Sequence
	size := 0
	seqIndex := 0
	for _, batch := range batches {
		if len(sequences) == 0 {
			sequences = append(sequences, Sequence{})
		}

		size += len(batch)
		if size > maxBatchSize {
			size = len(batch)
			seqIndex++
			var seq Sequence
			seq.Batches = append(seq.Batches, batch)
			sequences = append(sequences, seq)
		} else {
			sequences[seqIndex].Batches = append(sequences[seqIndex].Batches, batch)
		}
		log.Debugf("Sequence %s", hexEncode(sequences[seqIndex].Batches))
	}
	log.Debugf("Submitting %d sequences", len(sequences))

	var blobRefs []byte
	for _, seq := range sequences {
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		err := enc.Encode(seq)
		if err != nil {
			return nil, fmt.Errorf("error encoding sequence for NEAR: %s", err)
		}

		blob := sidecar.Blob{
			Data: buf.Bytes(),
		}
		log.Debugf("Blob: %s", hexEncode2(blob.Data))

		blobRef, err := n.Client.SubmitBlob(blob)
		log.Debugf("Blob ref: %s", hexEncode2(blobRef.Deref()))
		if err != nil {
			return nil, fmt.Errorf("error submitting data to NEAR: %s", err)
		}
		blobRefs = append(blobRefs, blobRef.Deref()...)
	}
	return blobRefs, nil
}

// GetSequence retrieves a sequence of batches from the Near blockchain.
// It takes a context, a slice of batch hashes, and a data availability message.
// It returns a slice of byte slices representing the retrieved batches data and any error encountered.
func (n *NearProtocolBackend) GetSequence(ctx context.Context, batchHashes []common.Hash, dataAvailabilityMessage []byte) ([][]byte, error) {
	var batchData [][]byte

	log.Debugf("Retrieving %d batches from dataAvailabilityMessage %s", len(batchHashes), hexEncode2(dataAvailabilityMessage))

	// FIXME: define the size of the ref in the library
	// Chunk the da message into references
	for _, ref := range chunks(dataAvailabilityMessage, 32) {
		blobRef, err := sidecar.NewBlobRef(ref)
		if err != nil {
			return nil, fmt.Errorf("error reading blob: %s", err)
		}

		log.Debugf("Retrieving %s from %s", hexEncode2(blobRef.Deref()), n.host)
		blob, err := n.Client.GetBlob(*blobRef)
		if err != nil {
			return nil, fmt.Errorf("error getting data from NEAR: %s", err)
		}
		log.Debugf("Retrieved blob %s", hexEncode2(blob.Data))

		buf := bytes.NewReader(blob.Data)
		codec := gob.NewDecoder(buf)

		var seq Sequence
		err = codec.Decode(&seq)
		if err != nil {
			return nil, fmt.Errorf("error encoding sequence for NEAR: %s", err)
		}
		log.Debugf("Decoded sequence %s", seq)

		batchData = append(batchData, seq.Batches...)
	}

	log.Debugf("Retrieved batches %s", batchData)
	return batchData, nil
}

func chunks(message []byte, chunkSize int) [][]byte {
	var chunks [][]byte
	for {
		if len(message) == 0 {
			break
		}

		// necessary check to avoid slicing beyond
		// slice capacity
		if len(message) < chunkSize {
			chunkSize = len(message)
		}

		chunks = append(chunks, message[0:chunkSize])
		message = message[chunkSize:]
	}

	return chunks
}
