package Blockchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	pbftconsensus "pbftnode/proto" // Your generated protobuf package
)

// Input defines the interface for different types of transaction data.
type Input interface {
	// inputToByte serializes the input data into bytes, typically for hashing or network transmission.
	inputToByte() ([]byte, error)
}

// transactCore holds the fundamental data of a transaction, excluding the signature and final hash.
type transactCore struct {
	Ids       uuid.UUID         `json:"ids"`       // Unique identifier for the transaction
	From      ed25519.PublicKey `json:"from"`      // Public key of the sender/proposer
	Input     Input             `json:"Data"`      // The actual payload (BrutData, Commande, SdnControlInput, etc.)
	Timestamp int64             `json:"timestamp"` // Timestamp of creation (nanoseconds)
}

// BrutData represents arbitrary byte data as transaction input.
type BrutData struct {
	Data []byte `json:"data"`
}

// inputToByte serializes BrutData using JSON.
func (in BrutData) inputToByte() ([]byte, error) {
	return json.Marshal(in)
}

// Transaction represents a complete transaction including its hash and signature.
type Transaction struct {
	Hash       []byte       `json:"hash"`       // Hash of the transactCore data
	Signature  []byte       `json:"signature"`  // Signature of the Hash by the 'From' key
	TransaCore transactCore `json:"transaCore"` // The core transaction data
}

// NewTransaction creates a new signed transaction.
func NewTransaction(in Input, wallet Wallet) (transac *Transaction) {
	transacCore := transactCore{
		Ids:       Ids(),
		From:      wallet.PublicKey(),
		Input:     in,
		Timestamp: time.Now().UnixNano(),
	}
	inputByte, err := json.Marshal(transacCore)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to marshal transactCore")
		return nil
	}

	hash := Hash(inputByte)
	transac = &Transaction{
		Hash:       hash,
		Signature:  wallet.Sign(hash),
		TransaCore: transacCore,
	}
	return
}

// NewBruteTransaction is a helper to create a transaction with simple byte data.
func NewBruteTransaction(data []byte, wallet Wallet) *Transaction {
	return NewTransaction(BrutData{Data: data}, wallet)
}

// VerifyTransaction checks if the transaction's signature is valid for its content and sender.
func (transaction Transaction) VerifyTransaction() bool {
	hashedDataBytes, err := json.Marshal(transaction.TransaCore)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal transactCore for verification")
		return false
	}
	expectedHash := Hash(hashedDataBytes)

	if !bytes.Equal(transaction.Hash, expectedHash) {
		log.Warn().Str("storedHash", base64.StdEncoding.EncodeToString(transaction.Hash)).Str("calculatedHash", base64.StdEncoding.EncodeToString(expectedHash)).Msg("Transaction hash mismatch")
		return false
	}
	return VerifySignature(transaction.TransaCore.From, transaction.Hash, transaction.Signature)
}

// ToByte serializes the transactCore struct to JSON bytes.
func (transacCore transactCore) ToByte() []byte {
	byted, err := json.Marshal(transacCore)
	if err != nil {
		log.Error().Err(err).Msg("Json Parsing error in transactCore.ToByte")
		return nil
	}
	return byted
}

// ToByte serializes the entire Transaction struct to JSON bytes.
func (transaction Transaction) ToByte() []byte {
	byted, err := json.Marshal(transaction)
	if err != nil {
		log.Error().Err(err).Msg("Json Parsing error in Transaction.ToByte")
		return nil
	}
	return byted
}

// CommandeType defines the type of system command within a transaction.
type CommandeType int8

const (
	VarieValid CommandeType = iota
	ChangeDelay
)

// Commande represents a system command embedded in a transaction.
type Commande struct {
	Order           CommandeType        `json:"order"`
	Variation       int                 `json:"variation"`
	NewValidatorSet []ed25519.PublicKey `json:"newValidatorSet"`
}

// inputToByte serializes Commande using JSON.
func (c Commande) inputToByte() ([]byte, error) {
	return json.Marshal(c)
}

// IsCommand checks if the transaction's input is of type Commande.
func (transaction Transaction) IsCommand() bool {
	_, ok := transaction.TransaCore.Input.(Commande)
	return ok
}

func (transac Transaction) verifyAsCommand(validators ValidatorInterf) (bool, error) {
	commande, ok := transac.TransaCore.Input.(Commande)
	if !ok {
		return false, errors.New("transaction input is not a Commande")
	}
	if !validators.IsActiveValidator(transac.TransaCore.From) {
		if !validators.IsValidator(transac.TransaCore.From) {
			return false, errors.New("command sender is not a known validator")
		}
		return false, errors.New("command sender is not an active validator")
	}
	switch commande.Order {
	case VarieValid:
		if !validators.CheckIfValidatorsAreNodes(commande.NewValidatorSet) {
			return false, fmt.Errorf("VarieValid command: new proposed validator list contains unknown nodes")
		}
		if !validators.IsSizeValid(len(commande.NewValidatorSet)) {
			return false, fmt.Errorf("VarieValid command: proposed validator set size %d is invalid", len(commande.NewValidatorSet))
		}
	case ChangeDelay:
		if commande.Variation < 0 {
			return false, errors.New("ChangeDelay command: negative delay variation is not allowed")
		}
	default:
		return false, fmt.Errorf("unknown command order type: %d", commande.Order)
	}
	return true, nil
}

func (transac Transaction) VerifyAsCommandShort(validators ValidatorInterf) bool {
	ok, err := transac.verifyAsCommand(validators)
	if err != nil {
		log.Warn().Err(err).Str("txHash", transac.GetHashPayload()).Msg("Command verification failed")
	}
	return ok
}

// --- SDN Control Input ---
type SdnControlInput struct {
	PacketInfo         *pbftconsensus.PacketInfo `json:"packet_info"`
	ProposedActionJson string                    `json:"proposed_action_json"`
}

func (in SdnControlInput) inputToByte() ([]byte, error) {
	return json.Marshal(in)
}

// --- Link Event Input ---
// LinkEventInput represents a link status change reported by the SDN primary.
type LinkEventInput struct {
	Dpid1       string `json:"dpid1"`
	Port1       uint32 `json:"port1"`
	Dpid2       string `json:"dpid2"`
	Port2       uint32 `json:"port2"`
	Status      string `json:"status"`       // "LINK_UP" or "LINK_DOWN" (matches enum names in proto)
	TimestampNs int64  `json:"timestamp_ns"` // Nanosecond timestamp from primary
}

// inputToByte serializes LinkEventInput using JSON.
func (in LinkEventInput) inputToByte() ([]byte, error) {
	return json.Marshal(in)
}

// --- /Link Event Input ---

func (transaction Transaction) GetHashPayload() string {
	return base64.StdEncoding.EncodeToString(transaction.Hash)
}

func (transac Transaction) GetProposer() ed25519.PublicKey {
	return transac.TransaCore.From
}

type ConsensusResult struct {
	TxHash        []byte                `json:"tx_hash"`
	Success       bool                  `json:"success"`
	FinalAction   *pbftconsensus.Action `json:"final_action"`
	ResultMessage string                `json:"result_message"`
	ReportingNode ed25519.PublicKey     `json:"reporting_node"`
}

func (cr ConsensusResult) ToByte() []byte {
	byted, err := json.Marshal(cr)
	if err != nil {
		log.Error().Err(err).Msg("Json Parsing error in ConsensusResult.ToByte")
		return nil
	}
	return byted
}

func (cr ConsensusResult) GetHashPayload() string {
	return base64.StdEncoding.EncodeToString(cr.TxHash)
}

func (cr ConsensusResult) GetProposer() ed25519.PublicKey {
	return cr.ReportingNode
}
