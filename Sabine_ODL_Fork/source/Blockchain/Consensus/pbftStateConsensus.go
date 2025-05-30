package Consensus

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"strings" // Added for string manipulation
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/status" // For gRPC status codes

	pbftconsensus "pbftnode/proto"
	"pbftnode/source/Blockchain"
)

const ChannelSize = 64
const ReplicaTimeout = 20 * time.Second

type PBFTStateConsensus struct {
	CoreConsensus
	state             consensusState
	stateFonct        stateInterf
	currentHash       []byte
	BlockPoolNV       *Blockchain.BlockPool
	RamOpt            bool
	Control           *Blockchain.ControlFeedBack
	toKill            chan chan struct{}
	chanReceivMsg     chan Blockchain.Message
	chanPrioInMsg     chan Blockchain.Message
	chanTxMsg         chan Blockchain.Message
	chanUpdateStatus  chan Blockchain.Message
	acceptUnknownTx   bool
	replicaClient     pbftconsensus.RyuReplicaLogicClient
	knownDealerPubKey ed25519.PublicKey
}

func (consensus *PBFTStateConsensus) GetBlockchain() *Blockchain.Blockchain {
	return consensus.BlockChain
}

func (consensus *PBFTStateConsensus) GetTransactionPool() Blockchain.TransactionPoolInterf {
	return consensus.TransactionPool
}

func NewPBFTStateConsensus(wallet *Blockchain.Wallet, numberOfNode int, param Blockchain.ConsensusParam) *PBFTStateConsensus {
	coreArgs := consensusArgs{
		MetricSaveFile:   param.MetricSaveFile,
		TickerSave:       param.TickerSave,
		ControlType:      param.ControlType,
		Behavior:         param.Behavior,
		RefreshingPeriod: param.RefreshingPeriod,
		SelectionType:    param.SelectionType,
		selectorArgs:     param.SelectorArgs,
	}

	consensus := &PBFTStateConsensus{
		CoreConsensus:     *NewCoreConsensus(wallet, numberOfNode, coreArgs),
		state:             NewRoundSt,
		toKill:            make(chan chan struct{}),
		chanReceivMsg:     make(chan Blockchain.Message, ChannelSize),
		chanTxMsg:         make(chan Blockchain.Message, ChannelSize),
		chanPrioInMsg:     make(chan Blockchain.Message, 1024),
		chanUpdateStatus:  make(chan Blockchain.Message, ChannelSize),
		BlockPoolNV:       Blockchain.NewBlockPool(),
		acceptUnknownTx:   param.AcceptTxFromUnknown,
		replicaClient:     param.ReplicaClient,
		knownDealerPubKey: Blockchain.NewWallet("DEALER").PublicKey(),
	}

	if consensus.IsProposer() {
		consensus.state = NewRoundProposerSt
	}
	consensus.stateFonct = consensus.updateStateFct()
	consensus.Broadcast = param.Broadcast
	consensus.PoANV = param.PoANV
	consensus.RamOpt = !param.Broadcast && param.RamOpt

	go consensus.MsgHandlerGoroutine(consensus.chanUpdateStatus, consensus.chanReceivMsg, consensus.toKill)
	go consensus.transactionHandler(consensus.chanUpdateStatus, consensus.chanTxMsg, consensus.TransactionPool.GetStopingChan())
	consensus.Control = Blockchain.NewControlFeedBack(consensus.Metrics, consensus, param.ModelFile, param.ControlPeriod)

	log.Info().Int("nodeId", consensus.GetId()).Msg("PBFT State Consensus initialized")
	if consensus.replicaClient == nil {
		log.Warn().Int("nodeId", consensus.GetId()).Msg("ReplicaClient is nil, SDN control/link validation disabled.")
	}
	return consensus
}

func (consensus *PBFTStateConsensus) SetControlInstruction(instruct bool) {
	if consensus.Control != nil {
		consensus.Control.SetInstruction(instruct)
	}
}

func (consensus *PBFTStateConsensus) updateState(state consensusState) {
	if consensus.state == state {
		return
	}
	oldState := consensus.state
	consensus.state = state
	if zerolog.GlobalLevel() <= zerolog.TraceLevel {
		log.Trace().
			Int("ID", consensus.GetId()).
			Str("Old State", oldState.String()).
			Str("New State", state.String()).
			Int("Proposer", consensus.BlockChain.GetProposerId()).
			Str("ref", base64.StdEncoding.EncodeToString(consensus.currentHash)).
			Int64("at", time.Now().UnixNano()).
			Msg("Consensus State Change")
	}
}

func (consensus *PBFTStateConsensus) MessageHandler(message Blockchain.Message) {
	if message.Data == nil {
		log.Warn().Msg("Received message with nil Data field, dropping.")
		return
	}

	senderPubKey := message.Data.GetProposer()
	isKnownValidator := consensus.Validators.IsValidator(senderPubKey)
	isKnownDealer := bytes.Equal(senderPubKey, consensus.knownDealerPubKey)
	allowMessage := false

	if isKnownValidator {
		allowMessage = true
	} else if isKnownDealer {
		// Allow transactions (SDN or LinkEvent) from the dealer
		if _, okSdn := message.Data.(Blockchain.Transaction).TransaCore.Input.(Blockchain.SdnControlInput); okSdn {
			allowMessage = true
			log.Debug().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Str("type", "SdnControlInputTx").Msg("Allowing SdnControlInput transaction from known Dealer")
		} else if _, okLink := message.Data.(Blockchain.Transaction).TransaCore.Input.(Blockchain.LinkEventInput); okLink {
			allowMessage = true
			log.Debug().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Str("type", "LinkEventInputTx").Msg("Allowing LinkEventInput transaction from known Dealer")
		} else if message.Flag == Blockchain.TransactionMess { // Allow other general transactions from dealer if acceptUnknownTx is true
			allowMessage = consensus.acceptUnknownTx
			if allowMessage {
				log.Debug().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Str("type", "GenericTx").Msg("Allowing generic transaction from known Dealer (acceptUnknownTx=true)")
			} else {
				log.Warn().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Str("type", "GenericTx").Msg("Dropping generic transaction from known Dealer (acceptUnknownTx=false)")
			}
		} else {
			log.Warn().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Str("type", message.Flag.String()).Msg("Dropping non-transaction message from Dealer (not typical)")
		}

	} else if message.Flag == Blockchain.TransactionMess && consensus.acceptUnknownTx {
		if tx, ok := message.Data.(Blockchain.Transaction); ok && !tx.IsCommand() {
			allowMessage = true
			log.Debug().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Msg("Allowing transaction from unknown sender (acceptUnknownTx=true)")
		} else {
			log.Warn().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Msg("Dropping Command transaction from unknown sender or non-SdnControlInput/LinkEventInput from non-validator")
		}
	} else {
		log.Warn().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).
			Str("type", message.Flag.String()).
			Msg("Dropping message from unknown or non-validator sender")
	}

	if !allowMessage {
		return
	}

	if message.Priority {
		select {
		case consensus.chanPrioInMsg <- message:
		default:
			log.Warn().Str("type", message.Flag.String()).Msg("Priority message channel full, dropping message.")
		}
	} else {
		if message.Flag == Blockchain.TransactionMess {
			select {
			case consensus.chanTxMsg <- message:
			default:
				log.Warn().Str("type", message.Flag.String()).Msg("Transaction message channel full, dropping message.")
			}
		} else {
			select {
			case consensus.chanReceivMsg <- message:
			default:
				log.Warn().Str("type", message.Flag.String()).Msg("Regular consensus message channel full, dropping message.")
			}
		}
	}
}
func (consensus *PBFTStateConsensus) transactionHandler(chanUpdateStatus chan<- Blockchain.Message, channelTx <-chan Blockchain.Message, stopAddTxChan <-chan bool) {
	var addTx = true
	var droppedCount int64 = 0

	for {
		select {
		case msg, ok := <-channelTx:
			if !ok {
				log.Info().Msg("Transaction handler channel closed.")
				return
			}
			transac, castOk := msg.Data.(Blockchain.Transaction)
			if !castOk {
				log.Error().Msg("Received non-transaction data on transaction channel.")
				continue
			}

			if addTx || transac.IsCommand() { // Always add commands
				// Also always add SdnControlInput and LinkEventInput regardless of addTx status if from dealer/validator
				_, isSdn := transac.TransaCore.Input.(Blockchain.SdnControlInput)
				_, isLink := transac.TransaCore.Input.(Blockchain.LinkEventInput)

				if isSdn || isLink || addTx || transac.IsCommand() {
					if droppedCount > 0 && !(isSdn || isLink || transac.IsCommand()) { // Only log resume for normal tx
						log.Warn().Int64("count", droppedCount).Msg("Resuming transaction processing after dropping.")
						droppedCount = 0
					}
					consensus.receiveTransacMess(msg) // Validate and add to pool
					select {
					case chanUpdateStatus <- msg:
						log.Trace().Str("txHash", transac.GetHashPayload()).Msg("transactionHandler: Signaled chanUpdateStatus.")
					default:
						log.Warn().Str("txHash", transac.GetHashPayload()).Msg("transactionHandler: chanUpdateStatus was full, signal potentially dropped.")
					}
				} else { // Was a normal tx and addTx is false
					droppedCount++
					if droppedCount%1000 == 1 {
						log.Warn().Int64("droppedSoFar", droppedCount).Msg("Transaction pool likely full, dropping incoming normal transaction.")
					}
				}
			} else { // Normal tx and addTx is false
				droppedCount++
				if droppedCount%1000 == 1 {
					log.Warn().Int64("droppedSoFar", droppedCount).Msg("Transaction pool likely full, dropping incoming normal transaction.")
				}
			}

		case addTx = <-stopAddTxChan:
			if addTx {
				if droppedCount > 0 {
					log.Warn().Int64("at", time.Now().UnixNano()).
						Int64("totalDropped", droppedCount).
						Msg("Transaction pool below threshold, resuming adding normal transactions.")
					droppedCount = 0
				} else {
					log.Info().Msg("Transaction pool below threshold, continuing normal operation.")
				}
			} else {
				log.Warn().Int64("at", time.Now().UnixNano()).
					Msg("Transaction pool threshold reached. Dropping new non-command/non-SDN/non-Link transactions.")
			}
		}
	}
}

func (consensus *PBFTStateConsensus) drainAndUpdateState(triggeringMessage Blockchain.Message) {
	if triggeringMessage.Data != nil {
		consensus.updateStatusWithMsg(triggeringMessage)
	}
	for {
		select {
		case message, ok := <-consensus.chanUpdateStatus:
			if !ok {
				log.Info().Int("nodeId", consensus.GetId()).Msg("drainAndUpdateState: chanUpdateStatus closed.")
				return
			}
			if message.Data == nil {
				continue
			}
			log.Debug().Int("nodeId", consensus.GetId()).Str("trigger", message.Flag.String()).Msg("drainAndUpdateState: Processing additional signal from chanUpdateStatus.")
			consensus.updateStatusWithMsg(message)
		default:
			log.Debug().Int("nodeId", consensus.GetId()).Msg("drainAndUpdateState: chanUpdateStatus drained for now.")
			return
		}
	}
}

func (consensus *PBFTStateConsensus) MsgHandlerGoroutine(chanUpdateStatus chan Blockchain.Message, chanReceivMsg <-chan Blockchain.Message, toKill <-chan chan struct{}) {
	log.Info().Int("nodeId", consensus.GetId()).Msg("Message handler goroutine started.")
	for {
		select {
		case channel, ok := <-toKill:
			if !ok {
				log.Info().Int("nodeId", consensus.GetId()).Msg("Kill channel closed. Goroutine exiting.")
				return
			}
			log.Info().Int("nodeId", consensus.GetId()).Msg("Message handler goroutine shutting down on signal.")
			channel <- struct{}{}
			return
		default:
		}

		select {
		case message, ok := <-chanUpdateStatus:
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanUpdateStatus is closed, but toKill was not. Exiting.")
				return
			}
			if message.Data != nil {
				log.Debug().Int("nodeId", consensus.GetId()).Str("trigger", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanUpdateStatus.")
				consensus.drainAndUpdateState(message)
			}
			continue
		default:
		}

		select {
		case message, ok := <-consensus.chanPrioInMsg:
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanPrioInMsg is closed. Exiting.")
				return
			}
			log.Debug().Int("nodeId", consensus.GetId()).Str("msgType", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanPrioInMsg.")
			consensus.handleOneMsg(message, chanUpdateStatus)
			consensus.drainAndUpdateState(message)

		case message, ok := <-chanReceivMsg:
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanReceivMsg is closed. Exiting.")
				return
			}
			log.Debug().Int("nodeId", consensus.GetId()).Str("msgType", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanReceivMsg.")
			consensus.handleOneMsg(message, chanUpdateStatus)
			consensus.drainAndUpdateState(message)

		case message, ok := <-chanUpdateStatus:
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanUpdateStatus closed (unexpectedly here). Exiting.")
				return
			}
			if message.Data != nil {
				log.Debug().Int("nodeId", consensus.GetId()).Str("trigger", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanUpdateStatus (secondary check).")
				consensus.drainAndUpdateState(message)
			}

		case channel, ok := <-toKill:
			if !ok {
				log.Info().Int("nodeId", consensus.GetId()).Msg("Kill channel closed during main select. Goroutine exiting.")
				return
			}
			log.Info().Int("nodeId", consensus.GetId()).Msg("Message handler goroutine shutting down on signal (main select).")
			channel <- struct{}{}
			return
			// default:
			// time.Sleep(1 * time.Millisecond)
		}
	}
}

func (consensus *PBFTStateConsensus) handleOneMsg(message Blockchain.Message, chanUpdateStatus chan<- Blockchain.Message) {
	consensus.receivedMessage(message)
	select {
	case chanUpdateStatus <- message:
	default:
		log.Warn().Str("type", message.Flag.String()).Msg("chanUpdateStatus full during handleOneMsg, signal potentially dropped.")
	}
}

func (consensus *PBFTStateConsensus) updateStatusWithMsg(message Blockchain.Message) {
	for {
		currentState := consensus.state
		currentSeqNb := consensus.GetSeqNb()
		nextPayload := consensus.stateFonct.giveMeNext(&message)

		if nextPayload != nil {
			emittedMsg := consensus.stateFonct.update(nextPayload)
			consensus.stateFonct = consensus.updateStateFct()
			if emittedMsg != nil {
				message = *emittedMsg
			} else {
				message = Blockchain.Message{}
			}
		}

		if consensus.state == currentState && consensus.GetSeqNb() == currentSeqNb && nextPayload == nil {
			break
		}
	}
}

func (consensus *PBFTStateConsensus) updateStateFct() stateInterf {
	switch consensus.state {
	case NewRoundSt:
		return &NewRound{consensus}
	case NewRoundProposerSt:
		return &NewRoundProposer{consensus}
	case PreparedSt:
		return &Prepared{consensus}
	case CommittedSt:
		return &Committed{consensus}
	case RoundChangeSt:
		log.Warn().Msg("RoundChangeSt state handler not implemented.")
		return &NewRound{consensus}
	case NVRoundSt:
		return &NewRoundNV{consensus}
	case FinalCommittedSt:
		log.Debug().Int("nodeId", consensus.GetId()).Msg("FinalCommittedSt: Transitioning to next state after commit.") // Changed to Debug
		consensus.updateStateAfterCommit()
		return consensus.updateStateFct()
	default:
		log.Error().Msgf("Unknown State %d encountered in updateStateFct", consensus.state)
		return &NewRound{consensus}
	}
}

func (consensus *PBFTStateConsensus) receivedMessage(message Blockchain.Message) {
	consensus.logTrace(message, consensus.isActiveValidator())
	switch message.Flag {
	case Blockchain.TransactionMess:
		consensus.receiveTransacMess(message)
	case Blockchain.PrePrepare:
		consensus.receivePrePrepareMessage(message)
	case Blockchain.PrepareMess:
		consensus.receivePrepareMessage(message)
	case Blockchain.CommitMess:
		consensus.receiveCommitMessage(message)
	case Blockchain.RoundChangeMess:
		consensus.receiveRCMessage(message)
	case Blockchain.BlocMsg:
		consensus.receiveBlockMsg(message)
	case Blockchain.ConsensusResultMess:
		log.Warn().Str("type", message.Flag.String()).Msg("Received ConsensusResultMess, which is unexpected for a PBFT node in consensus logic.")
	default:
		log.Warn().Msgf("Unrecognized Message Type: %d", message.Flag)
	}
}
func (consensus *PBFTStateConsensus) receiveTransacMess(message Blockchain.Message) {
	transac, castOk := message.Data.(Blockchain.Transaction)
	if !castOk {
		log.Error().Str("type", message.Flag.String()).Msg("Received TransactionMess flag but data is not Transaction type.")
		return
	}

	isKnownSender := consensus.Validators.IsValidator(message.Data.GetProposer()) ||
		bytes.Equal(message.Data.GetProposer(), consensus.knownDealerPubKey)
	isValidSender := consensus.acceptUnknownTx || isKnownSender

	isNew := !consensus.TransactionPool.ExistingTransaction(transac) && !consensus.BlockChain.ExistTx(transac.Hash)
	isVerified := transac.VerifyTransaction()

	isCommandValid := true
	if transac.IsCommand() { // Only call VerifyAsCommandShort if it's actually a command
		isCommandValid = transac.VerifyAsCommandShort(consensus.Validators)
	}

	log.Debug().Str("txHash", transac.GetHashPayload()).
		Bool("isValidSender", isValidSender).
		Bool("isNew", isNew).
		Bool("isVerified", isVerified).
		Bool("isCommandValidOrNotCmd", isCommandValid). // Renamed for clarity
		Msg("receiveTransacMess: Validation checks.")

	if isValidSender && isNew && isVerified && isCommandValid {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("receiveTransacMess: Validation checks passed, calling ReceiveTrustedMess.")
		consensus.ReceiveTrustedMess(message)
	} else {
		if !isValidSender {
			log.Warn().Str("tx", transac.GetHashPayload()).Msg("receiveTransacMess: Validation failed - Sender invalid or not accepted.")
		}
		if !isNew { // This is normal if transaction was already processed. Use Debug.
			log.Debug().Str("tx", transac.GetHashPayload()).Msg("receiveTransacMess: Validation failed - Transaction already processed or in pool.")
		}
		if !isVerified {
			log.Warn().Str("tx", transac.GetHashPayload()).Msg("receiveTransacMess: Validation failed - Transaction signature verification failed.")
		}
		// isCommandValid failure is logged by VerifyAsCommandShort
	}
}

func (consensus *PBFTStateConsensus) ReceiveTrustedMess(message Blockchain.Message) {
	transac, ok := message.Data.(Blockchain.Transaction)
	if !ok {
		log.Error().Str("type", message.Flag.String()).Msg("ReceiveTrustedMess called with non-Transaction data.")
		return
	}
	log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Calling TransactionPool.AddTransaction.")
	success := consensus.TransactionPool.AddTransaction(transac)
	if !success {
		log.Warn().Str("tx", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction pool rejected transaction (likely full or duplicate).")
		return
	}
	log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: TransactionPool.AddTransaction returned success.")

	senderPubKey := message.Data.GetProposer()
	isFromDealer := bytes.Equal(senderPubKey, consensus.knownDealerPubKey)

	if isFromDealer {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction is from Dealer, added to pool.")
	} else if message.ToBroadcast == Blockchain.AskToBroadcast {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction from peer (AskToBroadcast), broadcasting.")
		consensus.SocketHandler.BroadcastMessage(Blockchain.Message{
			Flag:        Blockchain.TransactionMess,
			Data:        transac,
			ToBroadcast: Blockchain.DontBroadcast,
			Priority:    message.Priority,
		})
	} else if (message.ToBroadcast == Blockchain.DefaultBehaviour) && !consensus.IsProposer() {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction from peer (DefaultBehaviour), forwarding to Proposer.")
		consensus.SocketHandler.TransmitTransaction(message)
	} else {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction from peer (DefaultBehaviour), already Proposer (or not broadcasting), added to pool.")
	}
}

func (consensus *PBFTStateConsensus) receivePrePrepareMessage(message Blockchain.Message) {
	block, ok := message.Data.(Blockchain.Block)
	if !ok {
		log.Error().Msg("Invalid data type for PrePrepare message.")
		return
	}
	log.Debug().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("Received PrePrepare in receivePrePrepareMessage.")

	isValid := consensus.BlockChain.IsValidNewBlock(block)
	isNew := !consensus.BlockPool.ExistingBlock(block)
	log.Debug().Bool("isNew", isNew).Bool("isValid", isValid).Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Checking PrePrepare basic validation.")

	if isNew && isValid {
		log.Debug().Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Basic validation passed. Starting SDN/Link validation.")

		sdnOrLinkValidationPassed := true
		for i, tx := range block.Transactions {
			log.Trace().Str("txHash", tx.GetHashPayload()).Int("txIndex", i).Msg("receivePrePrepareMessage: Checking transaction for SDN/Link input.")

			// Check for SdnControlInput
			if sdnInput, isSdn := tx.TransaCore.Input.(Blockchain.SdnControlInput); isSdn {
				log.Info().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: SdnControlInput transaction found, calling replica...")
				if consensus.replicaClient != nil {
					ctx, cancel := context.WithTimeout(context.Background(), ReplicaTimeout)
					defer cancel()
					req := &pbftconsensus.CalculateActionRequest{PacketInfo: sdnInput.PacketInfo}
					resp, err := consensus.replicaClient.CalculateAction(ctx, req)
					if err != nil {
						detailedStatus, _ := status.FromError(err)
						log.Error().Err(err).Str("grpc_status_code", detailedStatus.Code().String()).Str("grpc_status_message", detailedStatus.Message()).
							Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: replicaClient.CalculateAction returned ERROR")
						sdnOrLinkValidationPassed = false
						break
					} else if resp == nil || resp.ComputedAction == nil {
						log.Error().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: replicaClient.CalculateAction returned nil response/action")
						sdnOrLinkValidationPassed = false
						break
					} else {
						log.Info().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: replicaClient.CalculateAction returned SUCCESS")
						if sdnInput.ProposedActionJson != resp.ComputedAction.ActionJson {
							log.Warn().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).
								Str("proposed", sdnInput.ProposedActionJson).
								Str("computed", resp.ComputedAction.ActionJson).
								Msg("receivePrePrepareMessage: Replica computed action differs from proposed SDN action. Block will NOT be rejected by this node based on this mismatch alone (current policy).")
							// sdnOrLinkValidationPassed = false // To fail on mismatch, uncomment this
							// break
						} else {
							log.Debug().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: Replica action matches proposed SDN action.")
						}
					}
				} else {
					log.Warn().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: Received SdnControlInput transaction but no replica client configured. Cannot validate SDN action.")
					// sdnOrLinkValidationPassed = false // Uncomment if SDN validation is strictly mandatory
					// break
				}
			} else if _, isLink := tx.TransaCore.Input.(Blockchain.LinkEventInput); isLink {
				// For LinkEventInput, there's no explicit validation against a replica here.
				// The primary has reported it, and PBFT ensures all nodes agree on this report.
				// The validation is that it's a validly formed transaction from the dealer.
				log.Info().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: LinkEventInput transaction found. Accepted as part of block proposal.")
			} else {
				log.Trace().Str("txHash", tx.GetHashPayload()).Msgf("receivePrePrepareMessage: Transaction is not SdnControlInput or LinkEventInput, type is %T. Skipping specific validation.", tx.TransaCore.Input)
			}
		}

		if !sdnOrLinkValidationPassed {
			log.Warn().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("receivePrePrepareMessage: PrePrepare failed specific transaction validation step (SDN/Link).")
			return
		}

		consensus.BlockPool.AddBlock(block)
		log.Debug().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("receivePrePrepareMessage: PrePrepare passed validation, added block to BlockPool.")
		if consensus.Broadcast {
			log.Debug().Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Broadcasting PrePrepare.")
			consensus.SocketHandler.BroadcastMessage(message)
		}
		select {
		case consensus.chanUpdateStatus <- message:
		default:
			log.Warn().Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Failed to send update status signal (channel full).")
		}
	} else {
		log.Warn().Bool("isNew", isNew).Bool("isValid", isValid).Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: PrePrepare dropped (isNew && isValid check failed).")
	}
}

func (consensus *PBFTStateConsensus) receivePrepareMessage(message Blockchain.Message) {
	prepare, ok := message.Data.(Blockchain.Prepare)
	if !ok {
		log.Error().Msg("Invalid data type for Prepare message.")
		return
	}
	log.Debug().Str("hash", prepare.GetHashPayload()).Msg("Received Prepare in receivePrepareMessage.")

	if !prepare.IsValidPrepare() {
		log.Warn().Str("hash", prepare.GetHashPayload()).Msg("receivePrepareMessage: Received invalid Prepare message (signature failed).")
		return
	}
	if consensus.PreparePool.ExistingPrepare(prepare) {
		log.Debug().Str("hash", prepare.GetHashPayload()).Msg("receivePrepareMessage: Received duplicate Prepare message.")
		return
	}

	consensus.PreparePool.AddPrepare(prepare)
	log.Debug().Str("hash", prepare.GetHashPayload()).Int("count", consensus.PreparePool.GetNbPrepareOfHash(string(prepare.BlockHash))).Msg("receivePrepareMessage: Added Prepare message.")

	if consensus.Broadcast {
		log.Debug().Str("hash", prepare.GetHashPayload()).Msg("receivePrepareMessage: Broadcasting Prepare.")
		consensus.SocketHandler.BroadcastMessage(message)
	}

	if consensus.RamOpt && consensus.PreparePool.IsFullValidated(string(prepare.BlockHash)) &&
		(consensus.CommitPool.HaveCommitFrom(prepare.BlockHash, &consensus.Wallet) ||
			consensus.BlockChain.ExistBlockOfHash(prepare.BlockHash)) {
		log.Debug().Str("hash", prepare.GetHashPayload()).Msg("receivePrepareMessage: RamOpt: Removing validated Prepare pool entry.")
		consensus.PreparePool.Remove(prepare.BlockHash)
	}

	select {
	case consensus.chanUpdateStatus <- message:
	default:
		log.Warn().Str("hash", prepare.GetHashPayload()).Msg("receivePrepareMessage: Failed to send update status signal (channel full).")
	}
}

func (consensus *PBFTStateConsensus) receiveCommitMessage(message Blockchain.Message) {
	commit, ok := message.Data.(Blockchain.Commit)
	if !ok {
		log.Error().Msg("Invalid data type for Commit message.")
		return
	}
	log.Debug().Str("hash", commit.GetHashPayload()).Msg("Received Commit in receiveCommitMessage.")

	if !commit.IsValidCommit() {
		log.Warn().Str("hash", commit.GetHashPayload()).Msg("receiveCommitMessage: Received invalid Commit message (signature failed).")
		return
	}
	if consensus.CommitPool.ExistingCommit(commit) {
		log.Debug().Str("hash", commit.GetHashPayload()).Msg("receiveCommitMessage: Received duplicate Commit message.")
		return
	}

	consensus.CommitPool.AddCommit(commit)
	log.Debug().Str("hash", commit.GetHashPayload()).Int("count", consensus.CommitPool.GetNbCommitsOfHash(string(commit.BlockHash))).Msg("receiveCommitMessage: Added Commit message.")

	if consensus.Broadcast {
		log.Debug().Str("hash", commit.GetHashPayload()).Msg("receiveCommitMessage: Broadcasting Commit.")
		consensus.SocketHandler.BroadcastMessage(message)
	}

	if consensus.RamOpt && consensus.CommitPool.IsFullValidated(string(commit.BlockHash)) &&
		consensus.BlockChain.ExistBlockOfHash(commit.BlockHash) {
		log.Debug().Str("hash", commit.GetHashPayload()).Msg("receiveCommitMessage: RamOpt: Removing validated Commit pool entry.")
		consensus.CommitPool.Remove(commit.BlockHash)
	}

	select {
	case consensus.chanUpdateStatus <- message:
	default:
		log.Warn().Str("hash", commit.GetHashPayload()).Msg("receiveCommitMessage: Failed to send update status signal (channel full).")
	}
}

func (consensus *PBFTStateConsensus) receiveRCMessage(message Blockchain.Message) {
	round, ok := message.Data.(Blockchain.RoundChange)
	if !ok {
		log.Error().Msg("Invalid data type for RoundChange message.")
		return
	}
	log.Debug().Str("hash", round.GetHashPayload()).Msg("Received RoundChange in receiveRCMessage.")

	if !round.IsValidMessage() {
		log.Warn().Str("hash", round.GetHashPayload()).Msg("receiveRCMessage: Received invalid RoundChange message.")
		return
	}
	if consensus.MessagePool.ExistingMessage(round) {
		log.Debug().Str("hash", round.GetHashPayload()).Msg("receiveRCMessage: Received duplicate RoundChange message.")
		return
	}

	consensus.MessagePool.AddMessage(round)
	log.Debug().Str("hash", round.GetHashPayload()).Msg("receiveRCMessage: Added RoundChange message.")

	if consensus.Broadcast {
		log.Debug().Str("hash", round.GetHashPayload()).Msg("receiveRCMessage: Broadcasting RoundChange.")
		consensus.SocketHandler.BroadcastMessage(message)
	}
	select {
	case consensus.chanUpdateStatus <- message:
	default:
		log.Warn().Str("hash", round.GetHashPayload()).Msg("receiveRCMessage: Failed to send update status signal (channel full).")
	}
}

func (consensus *PBFTStateConsensus) receiveBlockMsg(message Blockchain.Message) {
	blockMsg, ok := message.Data.(Blockchain.BlockMsg)
	if !ok {
		log.Error().Msg("Invalid data type for BlockMsg message.")
		return
	}
	block := blockMsg.Block
	log.Debug().Int("SeqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("Received BlockMsg in receiveBlockMsg.")

	isNext := consensus.BlockChain.GetCurrentSeqNb()+1 == block.SequenceNb
	isValid := block.VerifyBlock()
	isNew := !consensus.BlockPoolNV.ExistingBlock(block) && !consensus.BlockChain.ExistBlockOfHash(block.Hash)

	if isNew && isNext && isValid {
		consensus.BlockPoolNV.AddBlock(block)
		log.Debug().Int("SeqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("receiveBlockMsg: Added BlockMsg to NV pool.")
		select {
		case consensus.chanUpdateStatus <- message:
		default:
			log.Warn().Int("SeqNb", block.SequenceNb).Msg("receiveBlockMsg: Failed to send update status signal (channel full).")
		}
	} else {
		if !isNew {
			log.Debug().Int("SeqNb", block.SequenceNb).Msg("receiveBlockMsg: Dropped duplicate BlockMsg.")
		}
		if !isNext {
			log.Warn().Int("SeqNb", block.SequenceNb).Int("expected", consensus.BlockChain.GetCurrentSeqNb()+1).Msg("receiveBlockMsg: Dropped out-of-order BlockMsg.")
		}
		if !isValid {
			log.Warn().Int("SeqNb", block.SequenceNb).Msg("receiveBlockMsg: Dropped invalid BlockMsg (verification failed).")
		}
	}
}

func (consensus *PBFTStateConsensus) updateStateAfterCommit() {
	if consensus.IsPoANV() && !consensus.isActiveValidator() {
		consensus.updateState(NVRoundSt)
	} else if consensus.IsProposer() {
		consensus.updateState(NewRoundProposerSt)
	} else {
		consensus.updateState(NewRoundSt)
	}
}

func (consensus *PBFTStateConsensus) Close() {
	log.Info().Int("nodeId", consensus.GetId()).Msg("Closing PBFT State Consensus...")
	if consensus.Metrics != nil {
		consensus.Metrics.Close()
		log.Debug().Msg("Metrics handler closed.")
	}
	if consensus.Control != nil {
		consensus.Control.Close()
		log.Debug().Msg("Control feedback loop closed.")
	}

	if consensus.toKill != nil {
		query := make(chan struct{})
		// Non-blocking send to toKill to prevent deadlock if MsgHandlerGoroutine already exited
		select {
		case consensus.toKill <- query:
			select { // Wait for acknowledgment with timeout
			case <-query:
				log.Debug().Msg("Message handler goroutine acknowledged shutdown.")
			case <-time.After(2 * time.Second):
				log.Warn().Msg("Timeout waiting for message handler goroutine ack.")
			}
		case <-time.After(1 * time.Second): // Timeout if MsgHandlerGoroutine is stuck
			log.Warn().Msg("Timeout sending close signal to message handler goroutine (already exited or stuck).")
		}
		// Close toKill only after attempting to send and potentially receive ack.
		// It's safe to close multiple times, but good practice to do it once.
		// Check if it's already closed before closing.
		// For simplicity, we assume it's not closed by another path.
		close(consensus.toKill)
		consensus.toKill = nil // Nil out to prevent further use
	}
	log.Info().Int("nodeId", consensus.GetId()).Msg("PBFT State Consensus closed.")
}

func (consensus PBFTStateConsensus) GetIncQueueSize() int {
	return len(consensus.chanReceivMsg) + len(consensus.chanPrioInMsg) + len(consensus.chanTxMsg)
}

func (consensus PBFTStateConsensus) GenerateNewValidatorListProposition(newSize int) []ed25519.PublicKey {
	seedByte := consensus.GetBlockchain().GetLastBLoc().Hash
	var seed int64 = 0
	if len(seedByte) >= 8 {
		seed = int64(binary.BigEndian.Uint64(seedByte[:8]))
	} else if len(seedByte) > 0 {
		var temp [8]byte
		copy(temp[:], seedByte)
		seed = int64(binary.BigEndian.Uint64(temp[:]))
		log.Warn().Msg("Last block hash too short for 64-bit seed generation, using partial hash padded with 0s.")
	} else {
		log.Warn().Msg("Last block hash is empty (genesis?), using 0 for seed.")
	}
	return consensus.selector.GenerateNewValidatorListProposition(consensus.Validators, newSize, seed)
}

// notifyReplicaOfLinkEvent is called when a LinkEventInput transaction is committed.
func (consensus *PBFTStateConsensus) notifyReplicaOfLinkEvent(linkEventInput Blockchain.LinkEventInput) {
	if consensus.replicaClient == nil {
		log.Warn().Str("txInputType", "LinkEventInput").Msg("Cannot notify replica: replicaClient is nil.")
		return
	}

	log.Info().Int("nodeId", consensus.GetId()).
		Msgf("Committed LinkEventInput. Notifying replica: %s:%d <-> %s:%d is %s",
			linkEventInput.Dpid1, linkEventInput.Port1, linkEventInput.Dpid2, linkEventInput.Port2, linkEventInput.Status)

	// Convert status string from LinkEventInput to proto enum
	var linkStatusProto pbftconsensus.LinkEventInfo_LinkStatus
	switch strings.ToUpper(linkEventInput.Status) { // Use ToUpper for case-insensitivity
	case "LINK_UP":
		linkStatusProto = pbftconsensus.LinkEventInfo_LINK_UP
	case "LINK_DOWN":
		linkStatusProto = pbftconsensus.LinkEventInfo_LINK_DOWN
	default:
		linkStatusProto = pbftconsensus.LinkEventInfo_LINK_STATUS_UNSPECIFIED // Default if string is unrecognized
		log.Warn().Str("status", linkEventInput.Status).Msg("Unknown link status string in committed LinkEventInput transaction, sending UNSPECIFIED to replica.")
	}

	linkEventProto := &pbftconsensus.LinkEventInfo{
		Dpid1:       linkEventInput.Dpid1,
		Port1:       linkEventInput.Port1,
		Dpid2:       linkEventInput.Dpid2,
		Port2:       linkEventInput.Port2,
		Status:      linkStatusProto,
		TimestampNs: linkEventInput.TimestampNs, // Pass along the timestamp
	}

	// Call replica in a goroutine to avoid blocking main consensus flow
	go func(client pbftconsensus.RyuReplicaLogicClient, event *pbftconsensus.LinkEventInfo) {
		ctx, cancel := context.WithTimeout(context.Background(), ReplicaTimeout) // Use defined timeout
		defer cancel()
		_, err := client.NotifyLinkEvent(ctx, event)
		if err != nil {
			detailedStatus, _ := status.FromError(err)
			log.Error().Err(err).Str("grpc_status_code", detailedStatus.Code().String()).Str("grpc_status_message", detailedStatus.Message()).
				Msgf("Failed to notify replica about link event: %v", event)
		} else {
			log.Debug().Msgf("Successfully notified replica about link event: %v", event)
		}
	}(consensus.replicaClient, linkEventProto)
}
