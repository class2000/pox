package Consensus

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

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
		replicaClient:     param.ReplicaClient, // Store the provided gRPC client
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
		log.Warn().Int("nodeId", consensus.GetId()).Msg("ReplicaClient is nil, SDN control validation disabled.")
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

		log.Debug().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Str("type", message.Flag.String()).Msg("Allowing message from known Dealer")
		allowMessage = true
	} else if message.Flag == Blockchain.TransactionMess && consensus.acceptUnknownTx {
		if tx, ok := message.Data.(Blockchain.Transaction); ok && !tx.IsCommand() {
			log.Debug().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Msg("Allowing transaction from unknown sender (acceptUnknownTx=true)")
			allowMessage = true
		} else {
			log.Warn().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).Msg("Dropping Command transaction from unknown sender (or acceptUnknownTx=false)")
		}
	} else {
		log.Warn().Str("sender", base64.StdEncoding.EncodeToString(senderPubKey)).
			Str("type", message.Flag.String()).
			Msg("Dropping message from unknown or non-validator sender")
	}

	if !allowMessage {
		return // Drop the message
	}

	if message.Priority {
		select {
		case consensus.chanPrioInMsg <- message:
		default:
			log.Warn().Str("type", message.Flag.String()).Msg("Priority message channel full, dropping message.")
		}
	} else {
		// Standard routing (no special handling for Dealer Tx here)
		if message.Flag == Blockchain.TransactionMess {
			select {
			case consensus.chanTxMsg <- message:
			default:
				log.Warn().Str("type", message.Flag.String()).Msg("Transaction message channel full, dropping message.")
			}
		} else { // Handle PrePrepare, Prepare, Commit, etc.
			select {
			case consensus.chanReceivMsg <- message:
			default:
				log.Warn().Str("type", message.Flag.String()).Msg("Regular consensus message channel full, dropping message.")
			}
		}
	}
}

// transactionHandler processes transactions from its dedicated channel.
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

			// Process all transactions received on this channel
			if addTx || transac.IsCommand() {
				if droppedCount > 0 {
					log.Warn().Int64("count", droppedCount).Msg("Resuming transaction processing after dropping.")
					droppedCount = 0
				}
				// Now calling receiveTransacMess (which validates and calls ReceiveTrustedMess)
				consensus.receiveTransacMess(msg) // Validate and add to pool
				select {
				case chanUpdateStatus <- msg:
					log.Trace().Str("txHash", transac.GetHashPayload()).Msg("transactionHandler: Signaled chanUpdateStatus.")
				default:
					log.Warn().Str("txHash", transac.GetHashPayload()).Msg("transactionHandler: chanUpdateStatus was full, signal potentially dropped for this new transaction.")
				}
			} else {
				droppedCount++
				if droppedCount%1000 == 1 {
					log.Warn().Int64("droppedSoFar", droppedCount).Msg("Transaction pool likely full, dropping incoming transaction.")
				}
			}
		case addTx = <-stopAddTxChan: // Signal from TransactionPool about overload status
			if addTx {
				if droppedCount > 0 {
					log.Warn().Int64("at", time.Now().UnixNano()).
						Int64("totalDropped", droppedCount).
						Msg("Transaction pool below threshold, resuming adding transactions.")
					droppedCount = 0
				} else {
					log.Info().Msg("Transaction pool below threshold, continuing normal operation.")
				}
			} else {
				log.Warn().Int64("at", time.Now().UnixNano()).
					Msg("Transaction pool threshold reached. Dropping new non-command transactions.")
			}
		}
	}
}

func (consensus *PBFTStateConsensus) drainAndUpdateState(triggeringMessage Blockchain.Message) {
	// Process the initial triggering message
	if triggeringMessage.Data != nil { // Ensure it's a valid message
		consensus.updateStatusWithMsg(triggeringMessage)
	}
	for {
		select {
		case message, ok := <-consensus.chanUpdateStatus:
			if !ok {
				log.Info().Int("nodeId", consensus.GetId()).Msg("drainAndUpdateState: chanUpdateStatus closed.")
				return // Channel closed
			}
			if message.Data == nil { // Skip empty/nil trigger messages if any
				continue
			}
			log.Debug().Int("nodeId", consensus.GetId()).Str("trigger", message.Flag.String()).Msg("drainAndUpdateState: Processing additional signal from chanUpdateStatus.")
			consensus.updateStatusWithMsg(message)
		default:
			// No more messages in chanUpdateStatus right now
			log.Debug().Int("nodeId", consensus.GetId()).Msg("drainAndUpdateState: chanUpdateStatus drained for now.")
			return
		}
	}
}

func (consensus *PBFTStateConsensus) MsgHandlerGoroutine(chanUpdateStatus chan Blockchain.Message, chanReceivMsg <-chan Blockchain.Message, toKill <-chan chan struct{}) {
	log.Info().Int("nodeId", consensus.GetId()).Msg("Message handler goroutine started.")
	for {
		// Prioritize shutdown
		select {
		case channel, ok := <-toKill:
			if !ok {
				log.Info().Int("nodeId", consensus.GetId()).Msg("Kill channel closed. Goroutine exiting.")
				return
			}
			log.Info().Int("nodeId", consensus.GetId()).Msg("Message handler goroutine shutting down on signal.")
			channel <- struct{}{} // Acknowledge shutdown
			return
		default:
			// Not shutting down, continue
		}

		// Prioritize processing state updates that have already been signaled
		select {
		case message, ok := <-chanUpdateStatus:
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanUpdateStatus is closed, but toKill was not. This is unexpected. Exiting.")
				return
			}
			if message.Data != nil { // Check if it's a real message, not just a placeholder
				log.Debug().Int("nodeId", consensus.GetId()).Str("trigger", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanUpdateStatus.")
				consensus.drainAndUpdateState(message) // Drain and process all queued updates
			}
			continue // After draining updates, immediately re-evaluate priorities (loop back to check toKill, then chanUpdateStatus again)
		default:
			// No pending state updates to prioritize, try to read external messages
		}

		// Now check external message channels (Prio then Regular)
		// We use a single select here to fairly choose between prio and regular if both are ready,
		// but the structure above tries to give chanUpdateStatus precedence.
		select {
		case message, ok := <-consensus.chanPrioInMsg:
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanPrioInMsg is closed. Exiting.")
				return
			}
			log.Debug().Int("nodeId", consensus.GetId()).Str("msgType", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanPrioInMsg.")
			consensus.handleOneMsg(message, chanUpdateStatus) // This will signal chanUpdateStatus
			consensus.drainAndUpdateState(message)            // Immediately process the update caused by this prio message

		case message, ok := <-chanReceivMsg:
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanReceivMsg is closed. Exiting.")
				return
			}
			log.Debug().Int("nodeId", consensus.GetId()).Str("msgType", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanReceivMsg.")
			consensus.handleOneMsg(message, chanUpdateStatus) // This will signal chanUpdateStatus
			consensus.drainAndUpdateState(message)            // Immediately process the update caused by this regular message

		case message, ok := <-chanUpdateStatus: // Catch any updates signaled during the small window before this select
			if !ok {
				log.Warn().Int("nodeId", consensus.GetId()).Msg("chanUpdateStatus closed (unexpectedly here). Exiting.")
				return
			}
			if message.Data != nil {
				log.Debug().Int("nodeId", consensus.GetId()).Str("trigger", message.Flag.String()).Msg("MsgHandlerGoroutine: Picked from chanUpdateStatus (secondary check).")
				consensus.drainAndUpdateState(message)
			}

		case channel, ok := <-toKill: // Re-check toKill, as it's the highest priority
			if !ok {
				log.Info().Int("nodeId", consensus.GetId()).Msg("Kill channel closed during main select. Goroutine exiting.")
				return
			}
			log.Info().Int("nodeId", consensus.GetId()).Msg("Message handler goroutine shutting down on signal (main select).")
			channel <- struct{}{}
			return
			// default: // Optional: Add a small sleep if all channels are consistently empty to prevent busy-waiting CPU spin
			// time.Sleep(1 * time.Millisecond)
		}
	}
}

// handleOneMsg processes a single received message.
func (consensus *PBFTStateConsensus) handleOneMsg(message Blockchain.Message, chanUpdateStatus chan<- Blockchain.Message) {
	consensus.receivedMessage(message) // Process the message based on its type
	select {
	case chanUpdateStatus <- message: // Signal state update check
	default:
	}
}

// updateStatusWithMsg checks if the last processed message triggered a state change.
func (consensus *PBFTStateConsensus) updateStatusWithMsg(message Blockchain.Message) {
	for {
		currentState := consensus.state
		currentSeqNb := consensus.GetSeqNb()

		// Pass the triggering message to giveMeNext
		nextPayload := consensus.stateFonct.giveMeNext(&message)

		if nextPayload != nil {
			emittedMsg := consensus.stateFonct.update(nextPayload)
			consensus.stateFonct = consensus.updateStateFct()
			if emittedMsg != nil {
				// If update emitted a message, use it for the next potential state check within this loop iteration
				message = *emittedMsg
			} else {
				// If no message was emitted, clear the message variable to prevent re-processing
				message = Blockchain.Message{}
			}
		}

		// Check if state or sequence number changed, or if no payload was ready
		if consensus.state == currentState && consensus.GetSeqNb() == currentSeqNb && nextPayload == nil {
			break // No state/seq change and nothing new ready, exit the inner loop
		}
		// If state/seq changed or a payload was generated, loop again to check the *new* state immediately
	}
}

// updateStateFct returns the stateInterf implementation for the current consensus state.
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
		log.Error().Msg("Attempted to get state function for FinalCommittedSt. Should have transitioned already.")
		// This indicates a logic error, transition immediately to the next state.
		consensus.updateStateAfterCommit()
		return consensus.updateStateFct() // Recursively call to get the correct next state handler
	default:
		log.Error().Msgf("Unknown State %d encountered in updateStateFct", consensus.state)
		// Return NewRound handler as a safe default fallback
		return &NewRound{consensus}
	}
}

// receivedMessage directs incoming messages to specific handlers based on type.
// NO LONGER includes workaround for Dealer's SDN Transaction.
func (consensus *PBFTStateConsensus) receivedMessage(message Blockchain.Message) {
	consensus.logTrace(message, consensus.isActiveValidator())

	// Standard message routing for actual PBFT messages
	switch message.Flag {
	case Blockchain.TransactionMess:
		consensus.receiveTransacMess(message) // Process the message based on its type
	case Blockchain.PrePrepare:
		consensus.receivePrePrepareMessage(message) // Replica call logic is inside here
	case Blockchain.PrepareMess:
		consensus.receivePrepareMessage(message)
	case Blockchain.CommitMess:
		consensus.receiveCommitMessage(message)
	case Blockchain.RoundChangeMess:
		consensus.receiveRCMessage(message)
	case Blockchain.BlocMsg:
		consensus.receiveBlockMsg(message)
	// --- ADDED CASE ---
	case Blockchain.ConsensusResultMess:
		// This message type is expected by the Dealer, not a PBFT Node for consensus logic.
		// Nodes should not typically receive this unless for testing or specific architecture.
		log.Warn().Str("type", message.Flag.String()).Msg("Received ConsensusResultMess, which is unexpected for a PBFT node in consensus.")
		// --- END ADDED CASE ---
	default:
		log.Warn().Msgf("Unrecognized Message Type: %d", message.Flag)
	}
}

// receiveTransacMess handles adding regular transactions to the pool (called by transactionHandler).
func (consensus *PBFTStateConsensus) receiveTransacMess(message Blockchain.Message) {
	transac, castOk := message.Data.(Blockchain.Transaction) // Check cast explicitly
	if !castOk {
		log.Error().Str("type", message.Flag.String()).Msg("Received TransactionMess flag but data is not Transaction type.")
		return // Drop message if cast fails
	}

	// Add detailed logging for validation checks
	isValidSender := consensus.acceptUnknownTx || consensus.Validators.IsValidator(message.Data.GetProposer())
	isNew := !consensus.TransactionPool.ExistingTransaction(transac) && !consensus.BlockChain.ExistTx(transac.Hash)
	isVerified := transac.VerifyTransaction()
	isCommandValid := !transac.IsCommand() || transac.VerifyAsCommandShort(consensus.Validators) // Note: SdnControlInput is NOT a command, so this is true for SDN tx

	log.Debug().Str("txHash", transac.GetHashPayload()).
		Bool("isValidSender", isValidSender).
		Bool("isNew", isNew).
		Bool("isVerified", isVerified).
		Bool("isCommandValid", isCommandValid).
		Msg("receiveTransacMess: Validation checks.")

	if isValidSender && isNew && isVerified && isCommandValid {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("receiveTransacMess: Validation checks passed, calling ReceiveTrustedMess.")
		consensus.ReceiveTrustedMess(message) // Validate and add to pool
	} else {
		// Log why it failed more clearly
		if !isValidSender {
			log.Warn().Str("tx", transac.GetHashPayload()).Msg("receiveTransacMess: Validation failed - Sender invalid or not accepted.")
		}
		if !isNew {
			log.Debug().Str("tx", transac.GetHashPayload()).Msg("receiveTransacMess: Validation failed - Transaction already processed or in pool.")
		}
		if !isVerified {
			log.Warn().Str("tx", transac.GetHashPayload()).Msg("receiveTransacMess: Validation failed - Transaction signature verification failed.")
		}
		// isCommandValid failure is logged by VerifyAsCommandShort if it returns false
	}
}

// ReceiveTrustedMess adds a validated transaction to the pool and handles broadcasting/forwarding.
func (consensus *PBFTStateConsensus) ReceiveTrustedMess(message Blockchain.Message) {
	transac, ok := message.Data.(Blockchain.Transaction) // Assume cast is safe here based on caller
	if !ok {
		log.Error().Str("type", message.Flag.String()).Msg("ReceiveTrustedMess called with non-Transaction data.")
		return
	}
	log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Calling TransactionPool.AddTransaction.")
	success := consensus.TransactionPool.AddTransaction(transac)
	if !success {
		log.Warn().Str("tx", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction pool rejected transaction (likely full).")
		return
	}
	log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: TransactionPool.AddTransaction returned success.")

	senderPubKey := message.Data.GetProposer()
	isFromDealer := bytes.Equal(senderPubKey, consensus.knownDealerPubKey)

	if isFromDealer {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction is from Dealer, added to pool. Will be included in next block if Proposer.")
		// No immediate broadcast/forwarding needed for transactions directly from the dealer.
	} else if message.ToBroadcast == Blockchain.AskToBroadcast {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction from peer (AskToBroadcast), broadcasting.")
		consensus.SocketHandler.BroadcastMessage(Blockchain.Message{
			Flag:        Blockchain.TransactionMess,
			Data:        transac,
			ToBroadcast: Blockchain.DontBroadcast, // Prevent loops
			Priority:    message.Priority,
		})
	} else if (message.ToBroadcast == Blockchain.DefaultBehaviour) && !consensus.IsProposer() {
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction from peer (DefaultBehaviour), forwarding to Proposer.")
		consensus.SocketHandler.TransmitTransaction(message) // Forward to proposer
	} else {
		// If from peer, DefaultBehaviour, and we ARE the proposer, also just add to pool.
		log.Debug().Str("txHash", transac.GetHashPayload()).Msg("ReceiveTrustedMess: Transaction from peer (DefaultBehaviour), already Proposer, added to pool.")
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
		log.Debug().Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Basic validation passed (isNew && isValid). Starting SDN validation.")

		// --- SDN Validation Step ---
		sdnValidationPassed := true
		for _, tx := range block.Transactions {
			log.Trace().Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: Checking transaction for SDN input for validation.")
			if sdnInput, isSdn := tx.TransaCore.Input.(Blockchain.SdnControlInput); isSdn {
				log.Info().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: SDN transaction found, calling replica...")
				if consensus.replicaClient != nil {
					// --- Use Background context for testing ---
					ctx := context.Background()

					req := &pbftconsensus.CalculateActionRequest{PacketInfo: sdnInput.PacketInfo}

					callStartTime := time.Now()
					log.Debug().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: >>> Calling replicaClient.CalculateAction...")

					resp, err := consensus.replicaClient.CalculateAction(ctx, req)

					callDuration := time.Since(callStartTime)
					if err != nil {
						log.Error().Err(err).Dur("duration", callDuration).Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: <<< replicaClient.CalculateAction returned ERROR")
						sdnValidationPassed = false
						break // Stop validating this block on error
					} else if resp == nil || resp.ComputedAction == nil {
						log.Error().Dur("duration", callDuration).Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: <<< replicaClient.CalculateAction returned nil response/action")
						sdnValidationPassed = false
						break // Stop validating this block on nil response
					} else {
						log.Info().Dur("duration", callDuration).Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: <<< replicaClient.CalculateAction returned SUCCESS")
						// Optional: Compare JSON strings - This comparison logic is already there, logging WARN if mismatch
						// If you uncomment the sdnValidationPassed = false here, mismatches will cause validation failure.
						if sdnInput.ProposedActionJson != resp.ComputedAction.ActionJson {
							log.Warn().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).
								Str("proposed", sdnInput.ProposedActionJson).
								Str("computed", resp.ComputedAction.ActionJson).
								Msg("receivePrePrepareMessage: Replica computed action differs from proposed action")
							// sdnValidationPassed = false // Uncomment to fail on mismatch
							// break                   // Uncomment to fail on mismatch
						} else {
							log.Debug().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: Replica action matches proposed action.") // Use Debug for less noise
						}
					}

				} else {
					log.Warn().Int("seqNb", block.SequenceNb).Str("txHash", tx.GetHashPayload()).Msg("receivePrePrepareMessage: Received SDN transaction but no replica client configured. Cannot validate.")
					sdnValidationPassed = false // ADD THIS LINE if SDN validation is mandatory
					break                       // Exit loop if validation is mandatory but impossible
				}
			} else {
				log.Trace().Str("txHash", tx.GetHashPayload()).Msgf("receivePrePrepareMessage: Transaction is not SdnControlInput, type is %T. Skipping SDN validation for this transaction.", tx.TransaCore.Input)
			}
		}
		// --- /SDN Validation Step ---

		if !sdnValidationPassed { // <-- This check determines if the block is processed
			log.Warn().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("receivePrePrepareMessage: PrePrepare failed SDN validation step.")
			return // Do not add to pool or broadcast
		}

		// Add block to pool if validation passes
		consensus.BlockPool.AddBlock(block)
		log.Debug().Int("seqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("receivePrePrepareMessage: PrePrepare passed validation, added block to BlockPool.")

		// Re-broadcast PrePrepare only if it's a real one and configured
		if consensus.Broadcast {
			log.Debug().Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Broadcasting PrePrepare.")
			consensus.SocketHandler.BroadcastMessage(message)
		}

		// Trigger state update check
		select {
		case consensus.chanUpdateStatus <- message:
			log.Debug().Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Sent update status signal.")
		default:
			log.Warn().Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: Failed to send update status signal (channel full).")
		}

	} else {
		// Log why it was dropped
		log.Warn().Bool("isNew", isNew).Bool("isValid", isValid).Int("seqNb", block.SequenceNb).Msg("receivePrePrepareMessage: PrePrepare dropped (isNew && isValid check failed).")
		// More specific logs moved inside the if block above
	}
}

// receivePrepareMessage handles incoming Prepare messages.
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
		log.Debug().Str("hash", prepare.GetHashPayload()).Msg("receivePrepareMessage: Sent update status signal.")
	default:
		log.Warn().Str("hash", prepare.GetHashPayload()).Msg("receivePrepareMessage: Failed to send update status signal (channel full).")
	}
}

// receiveCommitMessage handles incoming Commit messages.
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
		log.Debug().Str("hash", commit.GetHashPayload()).Msg("receiveCommitMessage: Sent update status signal.")
	default:
		log.Warn().Str("hash", commit.GetHashPayload()).Msg("receiveCommitMessage: Failed to send update status signal (channel full).")
	}
}

// receiveRCMessage handles incoming RoundChange messages (currently basic).
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
		log.Debug().Str("hash", round.GetHashPayload()).Msg("receiveRCMessage: Sent update status signal.")
	default:
		log.Warn().Str("hash", round.GetHashPayload()).Msg("receiveRCMessage: Failed to send update status signal (channel full).")
	}
}

// receiveBlockMsg handles BlockMsg for non-validators in PoA mode.
func (consensus *PBFTStateConsensus) receiveBlockMsg(message Blockchain.Message) {
	blockMsg, ok := message.Data.(Blockchain.BlockMsg)
	if !ok {
		log.Error().Msg("Invalid data type for BlockMsg message.")
		return
	}
	block := blockMsg.Block

	log.Debug().Int("SeqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("Received BlockMsg in receiveBlockMsg.")

	isNext := consensus.BlockChain.GetCurrentSeqNb()+1 == block.SequenceNb
	isValid := block.VerifyBlock() // This should already pass checks like signature and hash validity
	isNew := !consensus.BlockPoolNV.ExistingBlock(block) && !consensus.BlockChain.ExistBlockOfHash(block.Hash)

	if isNew && isNext && isValid {
		consensus.BlockPoolNV.AddBlock(block)
		log.Debug().Int("SeqNb", block.SequenceNb).Str("hash", block.GetHashPayload()).Msg("receiveBlockMsg: Added BlockMsg to NV pool.")
		select {
		case consensus.chanUpdateStatus <- message:
			log.Debug().Int("SeqNb", block.SequenceNb).Msg("receiveBlockMsg: Sent update status signal.")
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

// updateStateAfterCommit determines the next state after a block has been committed.
func (consensus *PBFTStateConsensus) updateStateAfterCommit() {
	if consensus.IsPoANV() && !consensus.isActiveValidator() {
		consensus.updateState(NVRoundSt)
	} else if consensus.IsProposer() {
		consensus.updateState(NewRoundProposerSt)
	} else {
		consensus.updateState(NewRoundSt)
	}
}

// Close gracefully shuts down the consensus engine and associated resources.
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
		select {
		case consensus.toKill <- query:
			select {
			case <-query:
				log.Debug().Msg("Message handler goroutine acknowledged shutdown.")
			case <-time.After(2 * time.Second):
				log.Warn().Msg("Timeout waiting for message handler goroutine ack.")
			}
		case <-time.After(1 * time.Second):
			log.Warn().Msg("Timeout sending close signal to message handler goroutine.")
		}
		close(consensus.toKill)
		consensus.toKill = nil
	}

	log.Info().Int("nodeId", consensus.GetId()).Msg("PBFT State Consensus closed.")
}

// GetIncQueueSize returns the approximate number of messages waiting in the input queues.
func (consensus PBFTStateConsensus) GetIncQueueSize() int {
	return len(consensus.chanReceivMsg) + len(consensus.chanPrioInMsg) + len(consensus.chanTxMsg)
}

// GenerateNewValidatorListProposition uses the configured selector strategy to propose a new validator set.
func (consensus PBFTStateConsensus) GenerateNewValidatorListProposition(newSize int) []ed25519.PublicKey {
	seedByte := consensus.GetBlockchain().GetLastBLoc().Hash
	var seed int64 = 0
	if len(seedByte) >= 8 {
		seed = int64(binary.BigEndian.Uint64(seedByte[:8]))
	} else if len(seedByte) > 0 {
		// Use whatever bytes are available, pad with 0s if needed for int64
		var temp [8]byte
		copy(temp[:], seedByte)
		seed = int64(binary.BigEndian.Uint64(temp[:]))
		log.Warn().Msg("Last block hash too short for 64-bit seed generation, using partial hash padded with 0s.")
	} else {
		log.Warn().Msg("Last block hash is empty (genesis?), using 0 for seed.")
	}

	return consensus.selector.GenerateNewValidatorListProposition(consensus.Validators, newSize, seed)
}
