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

	// Import the generated protobuf package (adjust path as necessary based on your Go module structure)
	pbftconsensus "pbftnode/proto"
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
		Ids:       Ids(),                 // Generate a new UUID
		From:      wallet.PublicKey(),    // Set sender's public key
		Input:     in,                    // Set the specific input payload
		Timestamp: time.Now().UnixNano(), // Record creation time
	}
	// Serialize the core transaction data to get bytes for hashing
	inputByte, err := json.Marshal(transacCore) // Use Marshal directly on transacCore
	if err != nil {
		// Handle potential marshaling error, though unlikely for this structure
		log.Fatal().Err(err).Msg("Failed to marshal transactCore") // Consider returning error instead of Fatal
		return nil
	}

	hash := Hash(inputByte) // Hash the serialized core data
	transac = &Transaction{
		Hash:       hash,
		Signature:  wallet.Sign(hash), // Sign the hash
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
	// Recalculate the hash based on the TransaCore data
	hashedDataBytes, err := json.Marshal(transaction.TransaCore)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal transactCore for verification")
		return false
	}
	expectedHash := Hash(hashedDataBytes)

	// 1. Verify the stored hash matches the recalculated hash
	if !bytes.Equal(transaction.Hash, expectedHash) {
		log.Warn().Str("storedHash", base64.StdEncoding.EncodeToString(transaction.Hash)).Str("calculatedHash", base64.StdEncoding.EncodeToString(expectedHash)).Msg("Transaction hash mismatch")
		return false
	}

	// 2. Verify the signature against the stored hash and the public key
	return VerifySignature(transaction.TransaCore.From, transaction.Hash, transaction.Signature)
}

// ToByte serializes the transactCore struct to JSON bytes.
// This is used for hashing the core content of the transaction.
func (transacCore transactCore) ToByte() []byte {
	byted, err := json.Marshal(transacCore)
	if err != nil {
		log.Error().Err(err).Msg("Json Parsing error in transactCore.ToByte")
		return nil
	}
	return byted
}

// ToByte serializes the entire Transaction struct to JSON bytes.
// This is used when the whole transaction (including hash and signature) needs to be serialized.
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
	VarieValid  CommandeType = iota // Command to change the active validator set
	ChangeDelay                     // Command to change simulated network delay
)

// Commande represents a system command embedded in a transaction.
type Commande struct {
	Order           CommandeType        `json:"order"`           // The type of command
	Variation       int                 `json:"variation"`       // Parameter for the command (e.g., new size or delay value)
	NewValidatorSet []ed25519.PublicKey `json:"newValidatorSet"` // The proposed new set of validators (for VarieValid)
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

// verifyAsCommand checks the validity of a command transaction.
// It ensures the sender is an active validator and the command parameters are valid.
func (transac Transaction) verifyAsCommand(validators ValidatorInterf) (bool, error) {
	commande, ok := transac.TransaCore.Input.(Commande)
	if !ok {
		return false, errors.New("transaction input is not a Commande")
	}
	// Check if the sender is currently an active validator
	if !validators.IsActiveValidator(transac.TransaCore.From) {
		if !validators.IsValidator(transac.TransaCore.From) { // Check if known at all
			return false, errors.New("command sender is not a known validator")
		}
		return false, errors.New("command sender is not an active validator")
	}
	// Validate based on the specific command type
	switch commande.Order {
	case VarieValid:
		// Check if the proposed validators are known nodes in the system
		if !validators.CheckIfValidatorsAreNodes(commande.NewValidatorSet) {
			return false, fmt.Errorf("VarieValid command: new proposed validator list contains unknown nodes")
		}
		// Check if the proposed size is within acceptable limits
		if !validators.IsSizeValid(len(commande.NewValidatorSet)) {
			return false, fmt.Errorf("VarieValid command: proposed validator set size %d is invalid", len(commande.NewValidatorSet))
		}
	case ChangeDelay:
		// Ensure delay variation is not negative (or apply other relevant checks)
		if commande.Variation < 0 {
			return false, errors.New("ChangeDelay command: negative delay variation is not allowed")
		}
	default:
		return false, fmt.Errorf("unknown command order type: %d", commande.Order)
	}
	return true, nil // Command is valid
}

// VerifyAsCommandShort provides a boolean check for command validity, logging any errors.
func (transac Transaction) VerifyAsCommandShort(validators ValidatorInterf) bool {
	ok, err := transac.verifyAsCommand(validators)
	if err != nil {
		log.Warn().Err(err).Str("txHash", transac.GetHashPayload()).Msg("Command verification failed")
	}
	return ok
}

// --- SDN Control Input ---

// SdnControlInput represents the data payload for transactions originating from the SDN control plane (Ryu Primary via Dealer).
type SdnControlInput struct {
	PacketInfo         *pbftconsensus.PacketInfo `json:"packet_info"`
	ProposedActionJson string                    `json:"proposed_action_json"` // The action proposed by the primary Ryu instance
}

// inputToByte serializes SdnControlInput using JSON.
func (in SdnControlInput) inputToByte() ([]byte, error) {
	return json.Marshal(in)
}

// --- /SDN Control Input ---

// GetHashPayload returns the transaction hash as a base64 encoded string.
func (transaction Transaction) GetHashPayload() string {
	return base64.StdEncoding.EncodeToString(transaction.Hash)
}

// GetProposer returns the public key of the transaction's sender.
func (transac Transaction) GetProposer() ed25519.PublicKey {
	return transac.TransaCore.From
}

// --- Consensus Result ---

// ConsensusResult carries the outcome of PBFT consensus for a specific transaction back to the Dealer.
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
