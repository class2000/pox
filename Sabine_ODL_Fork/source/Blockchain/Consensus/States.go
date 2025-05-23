package Consensus

import (
	"bytes"
	"encoding/base64" // <<< ADDED IMPORT
	"encoding/gob"    // <<< ADDED IMPORT
	"net"             // <<< ADDED IMPORT
	"time"            // <<< ADDED IMPORT

	"github.com/rs/zerolog/log"
	"pbftnode/source/Blockchain"
	pbftconsensus_pb "pbftnode/proto" // Alias for clarity if needed
)

// --- State Constants ---
// consensusState represents the different states in the PBFT state machine.
type consensusState int8

const (
	NewRoundSt consensusState = iota // Waiting for PrePrepare or timeout
	NewRoundProposerSt               // Proposer: Ready to create a block
	// PrePreparedSt is not an explicit state here; handled within NewRoundSt logic
	PreparedSt       // Received valid PrePrepare, waiting for Prepare messages
	CommittedSt      // Received enough Prepare messages, waiting for Commit messages
	FinalCommittedSt // Received enough Commit messages, block is finalized
	RoundChangeSt    // State for handling view changes (not fully implemented)
	NVRoundSt        // State for Non-Validators in PoA mode, waiting for BlockMsg
)

// String provides a human-readable name for each state.
func (id consensusState) String() string {
	switch id {
	case NewRoundSt:
		return "New Round"
	case NewRoundProposerSt:
		return "New Round Proposer"
	case PreparedSt:
		return "Prepared"
	case CommittedSt:
		return "Committed"
	case FinalCommittedSt:
		return "Final Committed"
	case RoundChangeSt:
		return "Round Change"
	case NVRoundSt:
		return "NV Round"
	default:
		return "Unknown state"
	}
}

// --- ADDED INTERFACE DEFINITION ---
// stateInterf defines the methods required by each consensus state handler.
type stateInterf interface {
	testState(Blockchain.Message) bool                      // Checks if a message is relevant to this state
	giveMeNext(*Blockchain.Message) Blockchain.Payload      // Determines if conditions are met to transition *out* of this state
	update(payload Blockchain.Payload) *Blockchain.Message // Performs actions upon transitioning *out* of this state
}

// --- /ADDED INTERFACE DEFINITION ---

// --- State Struct Definitions and Methods ---

// NewRound represents the state where a non-proposer node is waiting for a valid PrePrepare message.
type NewRound struct {
	*PBFTStateConsensus
}

// testState checks if an incoming message is a valid PrePrepare for the *next* expected block.
func (consensus NewRound) testState(message Blockchain.Message) bool {
	preprepared, ok := message.Data.(Blockchain.Block)
	return ok && message.Flag == Blockchain.PrePrepare && consensus.BlockChain.IsValidNewBlock(preprepared)
}

// giveMeNext checks if a valid PrePrepare message for the next block exists.
func (consensus NewRound) giveMeNext(message *Blockchain.Message) Blockchain.Payload {
	if message != nil && message.Flag == Blockchain.PrePrepare {
		if block, ok := message.Data.(Blockchain.Block); ok {
			// Check if the block is in the pool AND it's the next expected sequence number
			if consensus.BlockPool.ExistingBlock(block) && block.SequenceNb == consensus.BlockChain.GetCurrentSeqNb()+1 {
				log.Debug().Int("seqNb", block.SequenceNb).Msg("giveMeNext (NewRound): Triggering message is the valid PrePrepare from BlockPool.")
				return block
			}
		}
	}
	// If triggering message wasn't it, search the pool
	nextSeq := consensus.BlockChain.GetCurrentSeqNb() + 1
	for _, block := range consensus.BlockPool.GetBlocksOfSeqNb(nextSeq) {
		if consensus.BlockChain.IsValidNewBlock(block) { // Ensure it's still valid
			log.Debug().Int("seqNb", block.SequenceNb).Msg("giveMeNext (NewRound): Found valid PrePrepare in BlockPool (pool search).")
			return block
		}
	}
	return nil
}

// update transitions the node from NewRound to PreparedSt.
func (consensus *NewRound) update(payload Blockchain.Payload) *Blockchain.Message {
	block, ok := payload.(Blockchain.Block)
	if !ok {
		log.Error().Msg("Invalid payload type in NewRound.update, expected Block")
		return nil
	}
	consensus.currentHash = block.Hash
	log.Debug().Int("seqNb", block.SequenceNb).Msg("update (NewRound): Transitioning to Prepared state.")
	var prepare = Blockchain.CreatePrepare(&block, &(consensus.Wallet))
	emitMsg := Blockchain.Message{
		Flag: Blockchain.PrepareMess,
		Data: prepare,
	}
	consensus.PreparePool.AddPrepare(prepare) // Add our own prepare to the pool
	consensus.SocketHandler.BroadcastMessage(emitMsg)
	consensus.updateState(PreparedSt) // Update internal state marker
	return &emitMsg
}

// NewRoundProposer represents the state where the current node is the proposer.
type NewRoundProposer struct {
	*PBFTStateConsensus
}

// testState checks if the transaction pool is non-empty.
// For a proposer, the triggering condition is often just being in this state
// and having transactions, or a timeout forcing an empty block.
// For simplicity, we use the transaction pool emptiness.
func (consensus NewRoundProposer) testState(message Blockchain.Message) bool {
	return !consensus.TransactionPool.IsEmpty()
}

// giveMeNext checks if the node should propose a block.
func (consensus NewRoundProposer) giveMeNext(message *Blockchain.Message) Blockchain.Payload {
	if consensus.Control != nil {
		consensus.Control.CheckControl() // Allow control loop to inject commands if needed
	}
	if consensus.testState(Blockchain.Message{}) { // Pass empty message as testState doesn't use it
		newBlock := consensus.BlockChain.CreateBlock(consensus.TransactionPool.GetTxForBloc(), consensus.Wallet)
		log.Debug().Int("seqNb", newBlock.SequenceNb).Int("txCount", len(newBlock.Transactions)).Msg("giveMeNext (NewRoundProposer): Creating new block.")
		return *newBlock
	}
	// TODO: Implement timeout for proposing empty blocks if TransactionPool remains empty
	return nil
}

// update transitions the proposer from NewRoundProposer to NewRound (after broadcasting PrePrepare).
// The proposer itself will then process its own PrePrepare as if it came from another node.
func (consensus *NewRoundProposer) update(payload Blockchain.Payload) *Blockchain.Message {
	block, ok := payload.(Blockchain.Block)
	if !ok {
		log.Error().Msg("Invalid payload type in NewRoundProposer.update, expected Block")
		return nil
	}
	consensus.currentHash = block.Hash      // Set current hash for this round
	consensus.BlockPool.AddBlock(block) // Add to our own block pool
	log.Info().Int("seqNb", block.SequenceNb).Msg("update (NewRoundProposer): Broadcasting PrePrepare.")
	message := Blockchain.Message{
		Flag: Blockchain.PrePrepare,
		Data: block,
	}
	consensus.SocketHandler.BroadcastMessage(message)
	consensus.updateState(NewRoundSt) // After proposing, proposer acts like a regular node waiting for prepares
	return &message                   // Return the PrePrepare so it can be processed by the node itself
}

// Prepared represents the state waiting for enough Prepare messages.
type Prepared struct {
	*PBFTStateConsensus
}

// testState checks if an incoming message is a valid Prepare message for the current block hash.
func (consensus Prepared) testState(message Blockchain.Message) bool {
	prepare, ok := message.Data.(Blockchain.Prepare)
	return ok && message.Flag == Blockchain.PrepareMess &&
		bytes.Equal(prepare.BlockHash, consensus.currentHash) &&
		consensus.Validators.IsActiveValidator(prepare.GetProposer())
}

// giveMeNext checks if enough matching Prepare messages have been received to proceed to commit.
func (consensus Prepared) giveMeNext(message *Blockchain.Message) Blockchain.Payload {
	if consensus.PreparePool.GetNbPrepareOfHashOfActive(string(consensus.currentHash), consensus.Validators) >= consensus.MinApprovals() {
		log.Debug().Str("hash", base64.StdEncoding.EncodeToString(consensus.currentHash)).Msg("giveMeNext (Prepared): Reached Prepare threshold.")
		return Blockchain.CreateCommit(consensus.currentHash, &(consensus.Wallet)) // Create our own commit
	}
	return nil
}

// update transitions the node from Prepared to CommittedSt after broadcasting its Commit.
func (consensus *Prepared) update(payload Blockchain.Payload) *Blockchain.Message {
	commit, ok := payload.(Blockchain.Commit)
	if !ok {
		log.Error().Msg("Invalid payload type in Prepared.update, expected Commit")
		return nil
	}
	// Mark the prepares for this block hash as validated (though this might be redundant if done per-message)
	consensus.PreparePool.Validate(string(commit.BlockHash), consensus.Validators)
	log.Debug().Str("hash", commit.GetHashPayload()).Msg("update (Prepared): Broadcasting Commit, transitioning to Committed state.")
	consensus.CommitPool.AddCommit(commit) // Add our own commit to the pool
	message := Blockchain.Message{
		Flag: Blockchain.CommitMess,
		Data: commit,
	}
	consensus.SocketHandler.BroadcastMessage(message)
	consensus.updateState(CommittedSt)
	return &message // Return the Commit message
}

// Committed represents the state waiting for enough Commit messages.
type Committed struct {
	*PBFTStateConsensus
}

// testState checks if an incoming message is a valid Commit message for the current block hash.
func (consensus Committed) testState(message Blockchain.Message) bool {
	commit, ok := message.Data.(Blockchain.Commit)
	return ok && message.Flag == Blockchain.CommitMess &&
		bytes.Equal(commit.BlockHash, consensus.currentHash) &&
		consensus.Validators.IsActiveValidator(commit.GetProposer())
}

// giveMeNext checks if enough matching Commit messages have been received to finalize the block.
func (consensus Committed) giveMeNext(message *Blockchain.Message) Blockchain.Payload {
	if consensus.CommitPool.GetNbPrepareOfHashOfActive(string(consensus.currentHash), consensus.Validators) >= consensus.MinApprovals() {
		block := consensus.BlockPool.GetBlock(consensus.currentHash) // Retrieve the block from our pool
		if block.Hash != nil {                                        // Check if block was found
			log.Debug().Str("hash", base64.StdEncoding.EncodeToString(consensus.currentHash)).Msg("giveMeNext (Committed): Reached Commit threshold.")
			return block
		}
		// This should ideally not happen if PrePrepare was processed correctly.
		log.Error().Str("hash", base64.StdEncoding.EncodeToString(consensus.currentHash)).Msg("giveMeNext (Committed): Commit threshold reached, but block not found in pool!")
	}
	return nil
}

// update transitions the node from Committed to FinalCommittedSt, finalizing the block.
func (consensus *Committed) update(payload Blockchain.Payload) *Blockchain.Message {
	block, ok := payload.(Blockchain.Block)
	if !ok {
		log.Error().Msg("Invalid payload type in Committed.update, expected Block")
		return nil
	}

	// Standard commit logic
	log.Info().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("update (Committed): Finalizing block.")
	consensus.CommitPool.Validate(string(block.Hash), consensus.Validators) // Mark commits as validated

	// --- BEGIN: Report Consensus Result to Dealer ---
	for _, tx := range block.Transactions {
		if sdnInput, isSdn := tx.TransaCore.Input.(Blockchain.SdnControlInput); isSdn {
			log.Info().Int("nodeId", consensus.GetId()).Str("txHash", tx.GetHashPayload()).Msg("Committed block contains SDN transaction. Reporting result to Dealer.")

			finalPbftAction := &pbftconsensus_pb.Action{
				ActionJson: sdnInput.ProposedActionJson,
			}

			consensusResult := Blockchain.ConsensusResult{
				TxHash:        tx.Hash,
				Success:       true,
				FinalAction:   finalPbftAction,
				ResultMessage: "Consensus OK",
				ReportingNode: consensus.Wallet.PublicKey(),
			}

			// TODO: Make dealerAddr configurable (e.g., via NodeArg or environment variable)
			dealerAddr := "pbft-dealer:5000" // Default for Docker Compose setup

			// Send in a goroutine to avoid blocking consensus
			go func(dr Blockchain.ConsensusResult, da string, nodeId int) {
				conn, err := net.DialTimeout("tcp", da, 5*time.Second)
				if err != nil {
					log.Error().Err(err).Str("dealerAddr", da).Str("txHash", dr.GetHashPayload()).
						Msg("Failed to connect to Dealer to send consensus result.")
					return
				}
				defer conn.Close()

				// Perform ID exchange
				encoder := gob.NewEncoder(conn)
				err = encoder.Encode(nodeId) // Send this node's ID
				if err != nil {
					log.Error().Err(err).Msgf("Error sending ID to Dealer %s", da)
					return
				}

				var dealerIdResponse int // Variable to receive Dealer's ID
				decoder := gob.NewDecoder(conn)
				err = decoder.Decode(&dealerIdResponse)
				if err != nil {
					log.Error().Err(err).Msgf("Error receiving ID from Dealer %s", da)
					return
				}
				log.Debug().Int("nodeIdSent", nodeId).Int("dealerIdRcvd", dealerIdResponse).Msg("ID exchanged with Dealer for result reporting.")

				// Send the ConsensusResultMess
				msgToSend := Blockchain.Message{
					Flag: Blockchain.ConsensusResultMess,
					Data: dr,
				}
				// Re-initialize encoder for the message AFTER ID exchange
				// Note: The existing encoder on `conn` should be fine if the stream is continuous.
				// Creating a new one might be safer if there are concerns about gob state.
				// For simplicity, let's reuse. If issues, uncommenting the next line is a debug step.
				// encoder = gob.NewEncoder(conn)

				err = encoder.Encode(msgToSend)
				if err != nil {
					log.Error().Err(err).Str("dealerAddr", da).Str("txHash", dr.GetHashPayload()).
						Msg("Failed to send ConsensusResultMess to Dealer.")
				} else {
					log.Info().Str("dealerAddr", da).Str("txHash", dr.GetHashPayload()).
						Msg("Successfully sent ConsensusResultMess to Dealer.")
				}
			}(consensusResult, dealerAddr, consensus.GetId()) // Pass node ID

			break // Assuming only one SDN transaction per block that needs reporting
		}
	}
	// --- END: Report Consensus Result to Dealer ---

	// Broadcast finalized block to non-validators if in PoA mode and this node is the proposer
	if consensus.IsProposer() && consensus.PoANV {
		log.Debug().Int("seqNb", block.SequenceNb).Msg("Broadcasting finalized block to non-validators (PoA mode).")
		consensus.SocketHandler.BroadcastMessageNV(Blockchain.Message{
			Flag: Blockchain.BlocMsg,
			Data: Blockchain.BlockMsg{Block: block},
		})
	}

	// Add block to the blockchain and clean up pools
	consensus.BlockChain.AddUpdatedBlockCommited(block.Hash, consensus.BlockPool, consensus.TransactionPool, consensus.PreparePool, consensus.CommitPool)
	if consensus.RamOpt {
		consensus.BlockPool.RemoveBlock(block.Hash) // Clean up block pool if RAM optimization is on
	}

	consensus.updateState(FinalCommittedSt) // Transition to mark finalization of this block
	consensus.updateStateAfterCommit()      // Determine the next actual working state (NewRound or NewRoundProposer)
	return nil                               // No PBFT message is directly emitted from this state's update for progression
}

// NewRoundNV represents the state for non-validator nodes in PoA mode.
type NewRoundNV struct {
	*PBFTStateConsensus
}

// testState checks if an incoming message is a valid BlockMsg for the next expected block.
func (consensus NewRoundNV) testState(message Blockchain.Message) bool {
	blockMsg, ok := message.Data.(Blockchain.BlockMsg)
	// Also check if the block is valid and the next expected one.
	return ok && message.Flag == Blockchain.BlocMsg &&
		consensus.BlockChain.IsValidNewBlock(blockMsg.Block) // IsValidNewBlock checks sequence number too
}

// giveMeNext checks if a valid BlockMsg for the next block exists in the NV pool or is the triggering message.
func (consensus NewRoundNV) giveMeNext(message *Blockchain.Message) Blockchain.Payload {
	if message != nil && message.Flag == Blockchain.BlocMsg {
		if blockMsg, ok := message.Data.(Blockchain.BlockMsg); ok {
			// If the triggering message is the one in the pool and is next
			if consensus.BlockPoolNV.ExistingBlock(blockMsg.Block) && blockMsg.Block.SequenceNb == consensus.BlockChain.GetCurrentSeqNb()+1 {
				log.Debug().Int("seqNb", blockMsg.Block.SequenceNb).Msg("giveMeNext (NewRoundNV): Triggering message is the valid BlockMsg.")
				return blockMsg
			}
		}
	}
	// Search the pool if triggering message wasn't it
	nextSeq := consensus.BlockChain.GetCurrentSeqNb() + 1
	for _, block := range consensus.BlockPoolNV.GetBlocksOfSeqNb(nextSeq) {
		// Ensure block is still valid (though it should be if added correctly)
		if consensus.BlockChain.IsValidNewBlock(block) {
			log.Debug().Int("seqNb", block.SequenceNb).Msg("giveMeNext (NewRoundNV): Found valid BlockMsg in NV pool (pool search).")
			return Blockchain.BlockMsg{Block: block}
		}
	}
	return nil
}

// update transitions the non-validator node after receiving a valid BlockMsg.
func (consensus *NewRoundNV) update(payload Blockchain.Payload) *Blockchain.Message {
	blockMsg, ok := payload.(Blockchain.BlockMsg)
	if !ok {
		log.Error().Msg("Invalid payload type in NewRoundNV.update, expected BlockMsg")
		return nil
	}
	block := blockMsg.Block
	consensus.currentHash = block.Hash // Set current hash, though less critical for NV
	log.Info().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("update (NewRoundNV): Adding block directly from BlockMsg.")
	consensus.BlockChain.AddUpdatedBlock(block, consensus.TransactionPool) // Add to chain, flush TXs
	if consensus.RamOpt {
		consensus.BlockPoolNV.RemoveBlock(block.Hash) // Clean up NV block pool
	}
	consensus.updateState(FinalCommittedSt)  // Mark finalization
	consensus.updateStateAfterCommit()       // Determine next state (will remain NVRoundSt)
	return nil                               // No message emitted for PBFT progression
}