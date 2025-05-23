package Blockchain

import (
	"crypto/ed25519"
	// Import the generated protobuf package (adjust path as necessary)
	pbftconsensus "pbftnode/proto"
)

// MessageType defines the different types of messages used in the PBFT protocol.
type MessageType int8

// --- MODIFIED CONSTANTS ---
// Added ConsensusResultMess and incremented NbTypeMess
const NbTypeMess = 7 // Total number of distinct message types for array indexing etc.
// --- END MODIFIED CONSTANTS ---

const (
	TransactionMess MessageType = iota + 1 // Message carrying a client transaction or internal command
	PrePrepare                             // PBFT PrePrepare phase message (contains proposed block)
	PrepareMess                            // PBFT Prepare phase message (votes for PrePrepare)
	CommitMess                             // PBFT Commit phase message (votes for Prepare)
	RoundChangeMess                        // Message to initiate a view/round change (not fully implemented in provided code)
	BlocMsg                                // Message carrying a finalized block (used in PoA non-validator mode)
	ConsensusResultMess                    // <<< ADDED: Message carrying consensus result back to Dealer
)

// ConsensusParam holds configuration parameters for the consensus engine.
type ConsensusParam struct {
	Broadcast           bool                     // If true, nodes broadcast received Prepare/Commit messages (less efficient)
	PoANV               bool                     // If true, enables Proof-of-Authority non-validator mode (listen directly to proposer)
	RamOpt              bool                     // If true, enables optimizations to reduce RAM usage (e.g., removing old messages)
	MetricSaveFile      string                   // Path to save collected metrics periodically
	TickerSave          int                      // Interval (in minutes) for saving metrics (0 disables)
	ControlType         ControlType              // Type of feedback control algorithm to use for validator set adjustment
	ControlPeriod       int                      // Period (in # of refreshing periods) for executing the control loop
	RefreshingPeriod    int                      // Period (in seconds) for calculating metrics
	Behavior            OverloadBehavior         // Behavior of the transaction pool when full
	ModelFile           string                   // Path to the CSV model file used by ModelComparison control type
	SelectionType       SelectionValidatorType   // Strategy for selecting new validators (Pivot, Random, Centric)
	SelectorArgs        ArgsSelector             // Arguments specific to the chosen validator selector (e.g., adjacency matrix)
	AcceptTxFromUnknown bool                     // If true, accept transactions from non-validator nodes
	ReplicaClient       pbftconsensus.RyuReplicaLogicClient // gRPC client stub to connect to the Ryu Replica
}

// String returns a human-readable string representation of the MessageType.
func (id MessageType) String() string {
	switch id {
	case TransactionMess:
		return "Transaction"
	case PrePrepare:
		return "PrePrepare"
	case PrepareMess:
		return "Prepare"
	case CommitMess:
		return "Commit"
	case RoundChangeMess:
		return "RoundChange"
	case BlocMsg:
		return "Block"
	// --- ADDED CASE ---
	case ConsensusResultMess:
		return "ConsensusResult"
	// --- END ADDED CASE ---
	default:
		return "unknown"
	}
}

// Consensus defines the interface for the core consensus logic.
type Consensus interface {
	ValidatorGetterInterf // Embeds methods to get validator info
	MakeTransaction(Commande) *Transaction
	IsPoANV() bool
	MessageHandler(message Message)
	GetId() int // Returns the ID of the current node
	GetProposer() ed25519.PublicKey
	GetSeqNb() int // Returns the current sequence number (block height)
	MinApprovals() int // Returns the minimum number of approvals (2f+1) needed
	Close()
	SetHTTPViewer(port string)
	IsProposer() bool
	ReceiveTrustedMess(message Message) // Handles transactions known to be valid (e.g., from control loop)
	SetControlInstruction(instruct bool)
	GetControl() ControlType
	GenerateNewValidatorListProposition(newSize int) []ed25519.PublicKey
	IsActiveValidator(key ed25519.PublicKey) bool
	GetPubKeyofId(int) ed25519.PublicKey // Gets the public key for a given node ID
}

// writeChainInterf defines an interface for components that can provide access to the blockchain object.
// Used primarily for the Saver component.
type writeChainInterf interface {
	GetBlockchain() *Blockchain
}

// TestConsensus extends the Consensus interface with methods useful for testing.
type TestConsensus interface {
	Consensus
	GetBlockchain() *Blockchain
	GetValidator() ValidatorInterf
	GetTransactionPool() TransactionPoolInterf
	GetBlockPool() *BlockPool
	// Add other getter methods if needed for specific tests
}

// Message represents a generic message exchanged between nodes in the PBFT network.
type Message struct {
	Priority    bool          // Indicates if the message should be processed with higher priority
	Flag        MessageType   `json:"flag"` // The type of the message
	Data        Payload       `json:"data"` // The actual content/payload of the message
	ToBroadcast BroadcastType // Instruction on how this message should be relayed
}

// Payload defines the interface for data embedded within a Message.
type Payload interface {
	ToByte() []byte                 // Serializes the payload to bytes
	GetHashPayload() string         // Returns a hash or identifier string for the payload (often base64 of a primary hash)
	GetProposer() ed25519.PublicKey // Returns the public key of the originator of the payload
}

// BroadcastType specifies how a received message should be handled regarding re-transmission.
type BroadcastType uint8

const (
	// DefaultBehaviour: Typically means forward to the current proposer (for transactions).
	DefaultBehaviour BroadcastType = iota
	// AskToBroadcast: The receiving node should broadcast this message to all other relevant nodes.
	AskToBroadcast
	// DontBroadcast: The receiving node should not re-broadcast this message.
	DontBroadcast
)