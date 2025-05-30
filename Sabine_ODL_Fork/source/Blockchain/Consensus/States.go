package Consensus

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"net" // <<< ADDED IMPORT
	"time"

	pbftconsensus_pb "pbftnode/proto"
	"pbftnode/source/Blockchain"

	"github.com/rs/zerolog/log"
)

// --- State Constants --- (No change from your existing file)
type consensusState int8

const (
	NewRoundSt consensusState = iota
	NewRoundProposerSt
	PreparedSt
	CommittedSt
	FinalCommittedSt
	RoundChangeSt
	NVRoundSt
)

func (id consensusState) String() string { /* ... no change ... */
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

type stateInterf interface {
	testState(Blockchain.Message) bool
	giveMeNext(*Blockchain.Message) Blockchain.Payload
	update(payload Blockchain.Payload) *Blockchain.Message
}

// --- NewRound --- (No significant changes, only logging adjustments if any)
type NewRound struct{ *PBFTStateConsensus }

func (consensus NewRound) testState(message Blockchain.Message) bool { /* ... no change ... */
	preprepared, ok := message.Data.(Blockchain.Block)
	return ok && message.Flag == Blockchain.PrePrepare && consensus.BlockChain.IsValidNewBlock(preprepared)
}
func (consensus NewRound) giveMeNext(message *Blockchain.Message) Blockchain.Payload { /* ... no change ... */
	if message != nil && message.Flag == Blockchain.PrePrepare {
		if block, ok := message.Data.(Blockchain.Block); ok {
			if consensus.BlockPool.ExistingBlock(block) && block.SequenceNb == consensus.BlockChain.GetCurrentSeqNb()+1 {
				log.Debug().Int("seqNb", block.SequenceNb).Msg("giveMeNext (NewRound): Triggering message is the valid PrePrepare from BlockPool.")
				return block
			}
		}
	}
	nextSeq := consensus.BlockChain.GetCurrentSeqNb() + 1
	for _, block := range consensus.BlockPool.GetBlocksOfSeqNb(nextSeq) {
		if consensus.BlockChain.IsValidNewBlock(block) {
			log.Debug().Int("seqNb", block.SequenceNb).Msg("giveMeNext (NewRound): Found valid PrePrepare in BlockPool (pool search).")
			return block
		}
	}
	return nil
}
func (consensus *NewRound) update(payload Blockchain.Payload) *Blockchain.Message { /* ... no change ... */
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
	consensus.PreparePool.AddPrepare(prepare)
	consensus.SocketHandler.BroadcastMessage(emitMsg)
	consensus.updateState(PreparedSt)
	return &emitMsg
}

// --- NewRoundProposer --- (No significant changes)
type NewRoundProposer struct{ *PBFTStateConsensus }

func (consensus NewRoundProposer) testState(message Blockchain.Message) bool { /* ... no change ... */
	return !consensus.TransactionPool.IsEmpty()
}
func (consensus NewRoundProposer) giveMeNext(message *Blockchain.Message) Blockchain.Payload { /* ... no change ... */
	if consensus.Control != nil {
		consensus.Control.CheckControl()
	}
	if consensus.testState(Blockchain.Message{}) {
		newBlock := consensus.BlockChain.CreateBlock(consensus.TransactionPool.GetTxForBloc(), consensus.Wallet)
		log.Debug().Int("seqNb", newBlock.SequenceNb).Int("txCount", len(newBlock.Transactions)).Msg("giveMeNext (NewRoundProposer): Creating new block.")
		return *newBlock
	}
	return nil
}
func (consensus *NewRoundProposer) update(payload Blockchain.Payload) *Blockchain.Message { /* ... no change ... */
	block, ok := payload.(Blockchain.Block)
	if !ok {
		log.Error().Msg("Invalid payload type in NewRoundProposer.update, expected Block")
		return nil
	}
	consensus.currentHash = block.Hash
	consensus.BlockPool.AddBlock(block)
	log.Info().Int("seqNb", block.SequenceNb).Msg("update (NewRoundProposer): Broadcasting PrePrepare.")
	message := Blockchain.Message{
		Flag: Blockchain.PrePrepare,
		Data: block,
	}
	consensus.SocketHandler.BroadcastMessage(message)
	consensus.updateState(NewRoundSt)
	return &message
}

// --- Prepared --- (No significant changes)
type Prepared struct{ *PBFTStateConsensus }

func (consensus Prepared) testState(message Blockchain.Message) bool { /* ... no change ... */
	prepare, ok := message.Data.(Blockchain.Prepare)
	return ok && message.Flag == Blockchain.PrepareMess &&
		bytes.Equal(prepare.BlockHash, consensus.currentHash) &&
		consensus.Validators.IsActiveValidator(prepare.GetProposer())
}
func (consensus Prepared) giveMeNext(message *Blockchain.Message) Blockchain.Payload { /* ... no change ... */
	if consensus.PreparePool.GetNbPrepareOfHashOfActive(string(consensus.currentHash), consensus.Validators) >= consensus.MinApprovals() {
		log.Debug().Str("hash", base64.StdEncoding.EncodeToString(consensus.currentHash)).Msg("giveMeNext (Prepared): Reached Prepare threshold.")
		return Blockchain.CreateCommit(consensus.currentHash, &(consensus.Wallet))
	}
	return nil
}
func (consensus *Prepared) update(payload Blockchain.Payload) *Blockchain.Message { /* ... no change ... */
	commit, ok := payload.(Blockchain.Commit)
	if !ok {
		log.Error().Msg("Invalid payload type in Prepared.update, expected Commit")
		return nil
	}
	consensus.PreparePool.Validate(string(commit.BlockHash), consensus.Validators)
	log.Debug().Str("hash", commit.GetHashPayload()).Msg("update (Prepared): Broadcasting Commit, transitioning to Committed state.")
	consensus.CommitPool.AddCommit(commit)
	message := Blockchain.Message{
		Flag: Blockchain.CommitMess,
		Data: commit,
	}
	consensus.SocketHandler.BroadcastMessage(message)
	consensus.updateState(CommittedSt)
	return &message
}

// --- Committed --- (MODIFIED to notify replica of link events)
type Committed struct{ *PBFTStateConsensus }

func (consensus Committed) testState(message Blockchain.Message) bool { /* ... no change ... */
	commit, ok := message.Data.(Blockchain.Commit)
	return ok && message.Flag == Blockchain.CommitMess &&
		bytes.Equal(commit.BlockHash, consensus.currentHash) &&
		consensus.Validators.IsActiveValidator(commit.GetProposer())
}
func (consensus Committed) giveMeNext(message *Blockchain.Message) Blockchain.Payload { /* ... no change ... */
	if consensus.CommitPool.GetNbPrepareOfHashOfActive(string(consensus.currentHash), consensus.Validators) >= consensus.MinApprovals() {
		block := consensus.BlockPool.GetBlock(consensus.currentHash)
		if block.Hash != nil {
			log.Debug().Str("hash", base64.StdEncoding.EncodeToString(consensus.currentHash)).Msg("giveMeNext (Committed): Reached Commit threshold.")
			return block
		}
		log.Error().Str("hash", base64.StdEncoding.EncodeToString(consensus.currentHash)).Msg("giveMeNext (Committed): Commit threshold reached, but block not found in pool!")
	}
	return nil
}
func (consensus *Committed) update(payload Blockchain.Payload) *Blockchain.Message {
	block, ok := payload.(Blockchain.Block)
	if !ok {
		log.Error().Msg("Invalid payload type in Committed.update, expected Block")
		return nil
	}

	log.Info().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("update (Committed): Finalizing block.")
	consensus.CommitPool.Validate(string(block.Hash), consensus.Validators)

	// --- BEGIN: Report Consensus Result to Dealer AND Notify Replica of Link Events ---
	for _, tx := range block.Transactions {
		// Check for SdnControlInput to report to Dealer
		if sdnInput, isSdn := tx.TransaCore.Input.(Blockchain.SdnControlInput); isSdn {
			log.Info().Int("nodeId", consensus.GetId()).Str("txHash", tx.GetHashPayload()).Msg("Committed block contains SDN transaction. Reporting result to Dealer.")
			finalPbftAction := &pbftconsensus_pb.Action{ActionJson: sdnInput.ProposedActionJson}
			consensusResult := Blockchain.ConsensusResult{
				TxHash:        tx.Hash,
				Success:       true,
				FinalAction:   finalPbftAction,
				ResultMessage: "Consensus OK",
				ReportingNode: consensus.Wallet.PublicKey(),
			}
			dealerAddr := "pbft-dealer:5000" // TODO: Make this configurable if necessary
			go func(dr Blockchain.ConsensusResult, da string, nodeId int) {
				// Logic to connect and send ConsensusResultMess to dealer (as in your existing States.go)
				// This logic is simplified here for brevity, ensure your original send logic is used.
				conn, err := net.DialTimeout("tcp", da, 5*time.Second)
				if err != nil {
					log.Error().Err(err).Str("dealerAddr", da).Str("txHash", dr.GetHashPayload()).Msg("Failed to connect to Dealer for SDN result.")
					return
				}
				defer conn.Close()
				encoder := gob.NewEncoder(conn) // Create encoder for each attempt
				err = encoder.Encode(nodeId)    // Send this node's ID
				if err != nil {
					log.Error().Err(err).Msgf("Error sending ID to Dealer %s for SDN result", da)
					return
				}
				var dealerIdResponse int
				decoder := gob.NewDecoder(conn) // Create decoder for each attempt
				err = decoder.Decode(&dealerIdResponse)
				if err != nil {
					log.Error().Err(err).Msgf("Error receiving ID from Dealer %s for SDN result", da)
					return
				}

				msgToSend := Blockchain.Message{Flag: Blockchain.ConsensusResultMess, Data: dr}
				if err := encoder.Encode(msgToSend); err != nil {
					log.Error().Err(err).Str("dealerAddr", da).Str("txHash", dr.GetHashPayload()).Msg("Failed to send ConsensusResultMess to Dealer.")
				} else {
					log.Info().Str("dealerAddr", da).Str("txHash", dr.GetHashPayload()).Msg("Successfully sent ConsensusResultMess to Dealer.")
				}
			}(consensusResult, dealerAddr, consensus.GetId())
			// break // Assuming one SDN tx per block that needs this specific reporting
		}

		// **NEW**: Check for LinkEventInput to notify replica
		if linkEvent, isLinkEvent := tx.TransaCore.Input.(Blockchain.LinkEventInput); isLinkEvent {
			// Call the method in PBFTStateConsensus to handle notification
			consensus.notifyReplicaOfLinkEvent(linkEvent)
		}
	}
	// --- END: Reporting and Notification ---

	if consensus.IsProposer() && consensus.PoANV {
		log.Debug().Int("seqNb", block.SequenceNb).Msg("Broadcasting finalized block to non-validators (PoA mode).")
		consensus.SocketHandler.BroadcastMessageNV(Blockchain.Message{
			Flag: Blockchain.BlocMsg,
			Data: Blockchain.BlockMsg{Block: block},
		})
	}

	consensus.BlockChain.AddUpdatedBlockCommited(block.Hash, consensus.BlockPool, consensus.TransactionPool, consensus.PreparePool, consensus.CommitPool)
	if consensus.RamOpt {
		consensus.BlockPool.RemoveBlock(block.Hash)
	}

	consensus.updateState(FinalCommittedSt)
	consensus.updateStateAfterCommit()
	return nil
}

// --- NewRoundNV --- (MODIFIED to notify replica of link events)
type NewRoundNV struct{ *PBFTStateConsensus }

func (consensus NewRoundNV) testState(message Blockchain.Message) bool { /* ... no change ... */
	blockMsg, ok := message.Data.(Blockchain.BlockMsg)
	return ok && message.Flag == Blockchain.BlocMsg &&
		consensus.BlockChain.IsValidNewBlock(blockMsg.Block)
}
func (consensus NewRoundNV) giveMeNext(message *Blockchain.Message) Blockchain.Payload { /* ... no change ... */
	if message != nil && message.Flag == Blockchain.BlocMsg {
		if blockMsg, ok := message.Data.(Blockchain.BlockMsg); ok {
			if consensus.BlockPoolNV.ExistingBlock(blockMsg.Block) && blockMsg.Block.SequenceNb == consensus.BlockChain.GetCurrentSeqNb()+1 {
				log.Debug().Int("seqNb", blockMsg.Block.SequenceNb).Msg("giveMeNext (NewRoundNV): Triggering message is the valid BlockMsg.")
				return blockMsg
			}
		}
	}
	nextSeq := consensus.BlockChain.GetCurrentSeqNb() + 1
	for _, block := range consensus.BlockPoolNV.GetBlocksOfSeqNb(nextSeq) {
		if consensus.BlockChain.IsValidNewBlock(block) {
			log.Debug().Int("seqNb", block.SequenceNb).Msg("giveMeNext (NewRoundNV): Found valid BlockMsg in NV pool (pool search).")
			return Blockchain.BlockMsg{Block: block}
		}
	}
	return nil
}
func (consensus *NewRoundNV) update(payload Blockchain.Payload) *Blockchain.Message {
	blockMsg, ok := payload.(Blockchain.BlockMsg)
	if !ok {
		log.Error().Msg("Invalid payload type in NewRoundNV.update, expected BlockMsg")
		return nil
	}
	block := blockMsg.Block
	consensus.currentHash = block.Hash
	log.Info().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("update (NewRoundNV): Adding block directly from BlockMsg.")

	// **NEW**: Notify replica of link events if present in the block for NV
	for _, tx := range block.Transactions {
		if linkEvent, isLinkEvent := tx.TransaCore.Input.(Blockchain.LinkEventInput); isLinkEvent {
			consensus.notifyReplicaOfLinkEvent(linkEvent)
		}
		// NV nodes typically don't report SDN consensus results to dealer, but they *do* process LinkEvents
	}

	consensus.BlockChain.AddUpdatedBlock(block, consensus.TransactionPool)
	if consensus.RamOpt {
		consensus.BlockPoolNV.RemoveBlock(block.Hash)
	}
	consensus.updateState(FinalCommittedSt)
	consensus.updateStateAfterCommit()
	return nil
}
