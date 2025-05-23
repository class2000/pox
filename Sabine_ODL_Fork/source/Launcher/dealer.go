package Launcher

import (
	"context"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes" // <<< For gRPC status codes
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status" // <<< For gRPC status codes

	"github.com/rs/zerolog/log"

	pbftconsensus "pbftnode/proto"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Socket"
)

const nbTry int = 180
const DefaultDealerGrpcPort = "50051"
const consensusTimeout = 60 * time.Second // Timeout for waiting on PBFT consensus (increased for safety)

type DealerArg struct {
	BaseArg
	Contact          string
	NbOfNode         int
	IncomePort       string
	GrpcPort         string
	RandomDistrib    bool
	ReplicaAddresses []string // Kept for potential future use, but not used in RequestConsensus directly
}

// --- Global state for pending requests ---
var (
	// Map: transaction hash (string) -> channel to send the final gRPC response
	pendingConsensusRequests = make(map[string]chan *pbftconsensus.ConsensusResponse)
	pendingMutex             sync.Mutex                       // Protect access to the map
	dealerWallet             = Blockchain.NewWallet("DEALER") // Define dealer's wallet once
)

type pbftConsensusServer struct {
	pbftconsensus.UnimplementedPBFTConsensusServer
	sender   sender
	numNodes int
	// replicaSelector func() string // Removed: Nodes handle replica interaction
}

func newPbftConsensusServer(s sender, numNodes int) *pbftConsensusServer {
	// Removed replicaAddrs from parameters
	return &pbftConsensusServer{
		sender:   s,
		numNodes: numNodes,
	}
}

// RequestConsensus handles the RPC from the Ryu Primary.
// It now broadcasts the request as a PBFT transaction and waits for the actual result.
func (s *pbftConsensusServer) RequestConsensus(ctx context.Context, req *pbftconsensus.ConsensusRequest) (*pbftconsensus.ConsensusResponse, error) {
	log.Info().Msgf("gRPC Server: Received RequestConsensus from Ryu Primary")
	log.Debug().Msgf("  PacketInfo DPID: %s, InPort: %d", req.PacketInfo.Dpid, req.PacketInfo.InPort)
	log.Debug().Msgf("  Proposed Action: %s", req.ProposedAction.ActionJson)

	// 1. Create PBFT Transaction
	sdnInput := Blockchain.SdnControlInput{
		PacketInfo:         req.PacketInfo,
		ProposedActionJson: req.ProposedAction.ActionJson,
	}
	pbftTx := Blockchain.NewTransaction(sdnInput, *dealerWallet)
	if pbftTx == nil {
		errMsg := "Failed to create PBFT transaction for SDN request"
		log.Error().Msg(errMsg)
		return nil, status.Error(codes.Internal, errMsg)
	}
	txHashStr := string(pbftTx.Hash) // Use string representation for map key
	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msg("Created PBFT transaction for SDN request")

	// Check sender and node count
	if s.sender == nil {
		log.Error().Msg("gRPC Server: PBFT sender interface is nil")
		return nil, status.Error(codes.Internal, "Dealer internal error: sender not initialized")
	}
	if s.numNodes <= 0 {
		log.Warn().Msg("gRPC Server: Number of nodes is zero or unknown, cannot ensure consensus.")
		// Allow broadcast attempt but this is a problematic state
		// return nil, status.Error(codes.FailedPrecondition, "Dealer error: Cannot determine number of nodes for consensus")
	}

	// 2. Create and store response channel for this request
	respChan := make(chan *pbftconsensus.ConsensusResponse, 1) // Buffered channel
	pendingMutex.Lock()
	if _, exists := pendingConsensusRequests[txHashStr]; exists {
		pendingMutex.Unlock()
		log.Error().Str("txHash", pbftTx.GetHashPayload()).Msg("Duplicate consensus request detected (same transaction hash). Responding with error.")
		return nil, status.Error(codes.AlreadyExists, "Duplicate consensus request detected (tx hash already pending)")
	}
	pendingConsensusRequests[txHashStr] = respChan
	pendingMutex.Unlock()

	// Cleanup function to remove from map on exit/timeout/completion
	cleanup := func() {
		pendingMutex.Lock()
		// Check if the channel is still the one we created, in case of races (though unlikely with map key check)
		if currentChan, ok := pendingConsensusRequests[txHashStr]; ok && currentChan == respChan {
			delete(pendingConsensusRequests, txHashStr)
			log.Debug().Str("txHash", pbftTx.GetHashPayload()).Msg("Removed pending request from map.")
		}
		pendingMutex.Unlock()
	}
	defer cleanup()

	// 3. Broadcast the transaction
	// The number of nodes for sender.Add should reflect how many PBFT nodes it will attempt to send to.
	nodesToSignalForSend := s.numNodes // Assuming multiSend.Add expects count of targets for this send op
	if nodesToSignalForSend == 0 {     // If numNodes was 0, try to send to at least one if possible (though problematic)
		nodesToSignalForSend = 1
	}
	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msgf("Broadcasting SDN PBFT transaction to %d nodes (sender.Add called with this count)", nodesToSignalForSend)
	s.sender.Add(nodesToSignalForSend)         // Add count for the send operation itself
	go func(txToSend Blockchain.Transaction) { // Send in goroutine
		s.sender.send(txToSend, true, Blockchain.AskToBroadcast) // High priority, ask nodes to relay
	}(*pbftTx)

	// 4. Wait for the result from the PBFT network OR timeout OR context cancellation
	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msgf("Waiting up to %v for consensus result...", consensusTimeout)
	select {
	case response := <-respChan:
		log.Info().Str("txHash", pbftTx.GetHashPayload()).
			Bool("success", response.ConsensusReached).
			Str("status", response.StatusMessage).
			Msg("Received consensus result from PBFT network via internal channel.")
		return response, nil

	case <-time.After(consensusTimeout):
		log.Error().Str("txHash", pbftTx.GetHashPayload()).Msgf("Timeout after %v waiting for consensus result from PBFT network.", consensusTimeout)
		return nil, status.Errorf(codes.DeadlineExceeded, "Consensus timed out after %v", consensusTimeout)

	case <-ctx.Done():
		log.Warn().Str("txHash", pbftTx.GetHashPayload()).Err(ctx.Err()).Msg("Request cancelled by client (Ryu Primary).")
		return nil, status.Error(codes.Canceled, "Request cancelled by client")
	}
}

// handleConsensusResult processes the result notification received from a PBFT node.
func handleConsensusResult(result Blockchain.ConsensusResult) {
	txHashStr := string(result.TxHash) // Use string for map key
	log.Info().Str("txHash", result.GetHashPayload()).
		Bool("success", result.Success).
		Str("fromNode", base64.StdEncoding.EncodeToString(result.ReportingNode)). // Log sender
		Msg("Dealer received ConsensusResult notification from a node")

	pendingMutex.Lock()
	respChan, exists := pendingConsensusRequests[txHashStr]
	pendingMutex.Unlock() // Unlock before potentially blocking on channel send

	if exists {
		response := &pbftconsensus.ConsensusResponse{
			FinalAction:      result.FinalAction,
			ConsensusReached: result.Success,
			StatusMessage:    result.ResultMessage,
		}
		// Send the response back to the waiting RequestConsensus handler non-blockingly
		select {
		case respChan <- response:
			log.Debug().Str("txHash", result.GetHashPayload()).Msg("Sent consensus result to waiting gRPC handler.")
		default:
			// This case means the RequestConsensus handler is no longer waiting (timed out or cancelled).
			log.Warn().Str("txHash", result.GetHashPayload()).Msg("Consensus result received, but gRPC handler channel was blocked or request already handled (timeout/cancel).")
			// The defer cleanup() in RequestConsensus will eventually remove the map entry.
		}
	} else {
		log.Warn().Str("txHash", result.GetHashPayload()).Msg("Received consensus result for an unknown or already completed/timed-out request.")
	}
}

// Dealer is the main function to start the dealer node.
func Dealer(arg DealerArg) {
	var continu = true // Used to signal shutdown to loops
	var mySender sender
	var listContact []string
	var nContact int
	var grpcServer *grpc.Server
	var wg sync.WaitGroup // For managing goroutines

	arg.init()
	Socket.GobLoader()

	// Setup interrupt handler
	go func() {
		<-arg.interruptChan
		log.Info().Msg("Dealer: Received interrupt signal...")
		continu = false

		if grpcServer != nil {
			log.Info().Msg("Dealer: Initiating graceful gRPC server stop...")
			grpcServer.GracefulStop() // Waits for active RPCs
			log.Info().Msg("Dealer: gRPC server stopped.")
		} else {
			log.Info().Msg("Dealer: gRPC server was not running or already stopped.")
		}

		if mySender != nil {
			log.Info().Msg("Dealer: Closing sender connections...")
			mySender.close()
			log.Info().Msg("Dealer: Sender connections closed.")
		}
		// Listener closure is handled by its own interrupt handler via shutdownChan
	}()

	// --- Bootstrap Connection ---
	if arg.NbOfNode > 0 {
		var try int = nbTry
		ctxBoot, cancelBoot := context.WithCancel(context.Background())
		defer cancelBoot()

		// Goroutine to cancel bootstrap wait on main interrupt
		bootstrapWaitDone := make(chan struct{})
		go func() {
			defer close(bootstrapWaitDone)
			select {
			case <-arg.interruptChan: // If main interrupt occurs
				cancelBoot() // Cancel the bootstrap context
			case <-ctxBoot.Done(): // If context is cancelled by other means
			}
		}()

		for try > 0 && nContact < arg.NbOfNode {
			if ctxBoot.Err() != nil { // Check if context was cancelled (e.g., by interrupt)
				log.Warn().Msg("Bootstrap waiting cancelled by interrupt.")
				arg.close()
				return
			}
			_, nContact, listContact = getAContact(arg.Contact) // Assumes getAContact is defined
			log.Info().Msgf("%d nodes connected to the bootstrap server (waiting for %d)", nContact, arg.NbOfNode)
			if nContact < arg.NbOfNode {
				select {
				case <-time.After(1 * time.Second):
				case <-ctxBoot.Done(): // Re-check context during sleep
					log.Warn().Msg("Bootstrap waiting cancelled during sleep.")
					arg.close()
					return
				}
			}
			try--
		}
		cancelBoot()        // Explicitly cancel context if loop finished normally
		<-bootstrapWaitDone // Wait for the interrupt checker goroutine to finish

		if continu && try == 0 && nContact < arg.NbOfNode {
			log.Fatal().Msg("Too many attempts to connect to bootstrap or not enough nodes")
		} else if continu {
			log.Info().Msgf("Proceeding with %d connected nodes.", nContact)
		} else { // If !continu, means interrupt happened
			log.Info().Msg("Dealer startup interrupted during bootstrap phase.")
			arg.close()
			return
		}
	} else {
		_, nContact, listContact = getAContact(arg.Contact)
		log.Info().Msgf("%d nodes connected to the bootstrap server", nContact)
		if nContact == 0 {
			log.Warn().Msg("No nodes reported by bootstrap server.")
		}
	}
	// Socket.GobLoader() // GobLoader is called by NewNetSocket

	// --- Initialize Sender ---
	effectiveNodeCount := len(listContact)
	if effectiveNodeCount > 0 {
		mySender = newMultiSend(listContact, !arg.RandomDistrib)
		if ms, ok := mySender.(*multiSend); ok {
			ms.nodeCount = effectiveNodeCount
		}
	} else {
		log.Warn().Msg("No PBFT node contacts obtained, sender will not be functional.")
	}

	// --- Start Listener for Incoming Connections (Clients AND Nodes) ---
	if arg.IncomePort != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msgf("Dealer: Starting listener on port %s (for clients AND node results)", arg.IncomePort)
			createIncomingListener(&arg, mySender) // Pass BaseArg for interruptChan
			log.Info().Msg("Dealer: Incoming listener stopped.")
		}()
	} else {
		log.Warn().Msg("Dealer: IncomePort not specified. Nodes will not be able to send ConsensusResult back via this port.")
		// This is a critical warning if this is the intended mechanism for results.
	}

	// --- Start gRPC Server ---
	if arg.GrpcPort == "" {
		arg.GrpcPort = DefaultDealerGrpcPort
		log.Warn().Msgf("Dealer: gRPC port not specified, using default %s", arg.GrpcPort)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		lis, err := net.Listen("tcp", fmt.Sprintf(":%s", arg.GrpcPort))
		if err != nil {
			log.Fatal().Msgf("Failed to listen for gRPC: %v", err)
		}
		log.Info().Msgf("Dealer: Starting gRPC server for Ryu Primary on port %s", arg.GrpcPort)

		grpcServer = grpc.NewServer()
		pbftServer := newPbftConsensusServer(mySender, effectiveNodeCount)
		pbftconsensus.RegisterPBFTConsensusServer(grpcServer, pbftServer)
		reflection.Register(grpcServer)

		log.Info().Msg("Dealer: gRPC server starting to serve...")
		if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			log.Error().Err(err).Msg("gRPC server failed")
		}
		log.Info().Msg("Dealer: gRPC server Serve() returned.")
	}()

	log.Info().Msg("Dealer: Initialization complete. Waiting for all components to stop...")
	wg.Wait() // Wait for listener and gRPC server goroutines

	log.Info().Msg("Dealer: Shutdown sequence complete.")
	arg.close()
}

// createIncomingListener starts listening for both client and node connections.
func createIncomingListener(arg *DealerArg, send sender) {
	if arg.IncomePort == "" {
		log.Warn().Msg("No income port specified for incoming connections. Listener not started.")
		return
	}
	listenSocket, err := net.Listen(connType, addr+":"+arg.IncomePort)
	if err != nil {
		log.Error().Msgf("Failed to start listener on %s: %s", arg.IncomePort, err.Error())
		return
	}
	defer listenSocket.Close()

	log.Info().Msgf("Listener started on %s for clients and node results", arg.IncomePort)
	shutdownChan := make(chan struct{}) // Channel to signal connection handlers to stop
	var connWg sync.WaitGroup           // WaitGroup for active connection handlers

	// Goroutine to close listener on application interrupt signal
	go func() {
		<-arg.BaseArg.interruptChan // Wait for the main interrupt signal from BaseArg
		log.Info().Msg("Incoming listener: Main interrupt signal received. Closing listener.")
		listenSocket.Close() // Close the listener socket to stop Accept loop
		close(shutdownChan)  // Signal active handlers to stop
	}()

	// Accept loop
	for {
		conn, err := listenSocket.Accept()
		if err != nil {
			select {
			case <-shutdownChan: // Check if shutdown was signaled
				log.Info().Msg("Incoming listener: Shutting down accept loop.")
				connWg.Wait() // Wait for active handlers to finish
				return        // Exit the accept loop
			default:
				log.Error().Err(err).Msg("Error accepting connection")
				if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
					log.Info().Msg("Listener closed.")
					connWg.Wait()
					return
				}
				continue // Continue accepting on other errors
			}
		}
		log.Info().Str("remoteAddr", conn.RemoteAddr().String()).Msg("Accepted incoming connection")
		connWg.Add(1)
		go handleConnection(conn, arg, send, &connWg, shutdownChan) // Pass shutdownChan
	}
}

// handleConnection handles both client and node incoming connections.
func handleConnection(conn net.Conn, arg *DealerArg, send sender, wg *sync.WaitGroup, shutdown <-chan struct{}) {
	defer wg.Done()
	defer conn.Close()

	idIncoming := Socket.ExchangeIdServer(nil, conn)
	log.Info().Str("remoteAddr", conn.RemoteAddr().String()).Int("id", idIncoming).Msg("Handling incoming connection")

	decoder := gob.NewDecoder(conn)
	msgChan := make(chan Blockchain.Message, 10) // Buffered channel for incoming messages
	errChan := make(chan error, 1)

	// Goroutine to continuously decode messages
	decodeWg := sync.WaitGroup{}
	decodeWg.Add(1)
	go func() {
		defer decodeWg.Done()
		defer close(msgChan)
		for {
			var message Blockchain.Message
			if err := decoder.Decode(&message); err != nil {
				select {
				case <-shutdown: // Don't send error if shutdown was signaled
				default:
					if err != io.EOF && err.Error() != "use of closed network connection" { // Avoid logging expected close errors
						errChan <- err
					} else {
						// Send EOF as a signal to the main loop to close this handler.
						// Or just let it close msgChan which will be detected.
						if err == io.EOF {
							log.Debug().Int("id", idIncoming).Msg("Decoder: EOF received.")
							errChan <- io.EOF // Signal EOF
						}
					}
				}
				return // Stop decoding on error or shutdown
			}
			select {
			case msgChan <- message:
			case <-shutdown:
				log.Debug().Int("id", idIncoming).Msg("Decoder stopping during send due to shutdown.")
				return
			case <-time.After(1 * time.Second):
				log.Warn().Int("id", idIncoming).Msg("Timeout sending decoded message to handler channel.")
			}
		}
	}()

	// Main handler loop for this connection
	for {
		select {
		case <-shutdown:
			log.Info().Int("id", idIncoming).Msg("Stopping handler due to shutdown signal.")
			// Ensure decoder goroutine also exits
			conn.Close()    // Force decoder to unblock if stuck on Decode()
			decodeWg.Wait() // Wait for decoder goroutine to finish
			return

		case err := <-errChan:
			if err == io.EOF {
				log.Info().Int("id", idIncoming).Msg("Connection closed by remote peer (EOF).")
			} else {
				log.Error().Int("id", idIncoming).Err(err).Msg("Error decoding message.")
			}
			decodeWg.Wait() // Wait for decoder goroutine
			return

		case message, ok := <-msgChan:
			if !ok {
				log.Info().Int("id", idIncoming).Msg("Message channel closed (decoder exited), handler exiting.")
				decodeWg.Wait() // Ensure decoder is finished
				return
			}

			// Process based on who connected (Client or Node)
			if idIncoming == -1 { // === Client Logic ===
				if message.Flag == Blockchain.TransactionMess {
					if transaction, txOk := message.Data.(Blockchain.Transaction); txOk {
						log.Debug().Str("txHash", transaction.GetHashPayload()).Msgf("Received transaction from client %s", conn.RemoteAddr())
						if send != nil {
							numNodes := 0
							if ms, okSend := send.(*multiSend); okSend {
								numNodes = ms.nodeCount
							}
							nodesToSignal := 0
							if arg.RandomDistrib {
								nodesToSignal = 1
							} else if numNodes > 0 {
								nodesToSignal = numNodes
							} else {
								log.Warn().Msgf("Cannot determine node count for transaction from client %s, defaulting to 1", conn.RemoteAddr())
								nodesToSignal = 1
							}
							if nodesToSignal > 0 {
								send.Add(nodesToSignal)
								go func(tx Blockchain.Transaction, prio bool) {
									send.send(tx, prio, Blockchain.DontBroadcast) // Dealer forwards, nodes then broadcast
								}(transaction, message.Priority)
							}
						} else {
							log.Warn().Msgf("Sender not available, dropping transaction from client %s", conn.RemoteAddr())
						}
					} else {
						log.Warn().Msgf("Received non-transaction data in TransactionMess from client %s", conn.RemoteAddr())
					}
				} else {
					log.Warn().Int("id", idIncoming).Str("type", message.Flag.String()).Msg("Received unexpected message type from client")
				}
			} else { // === Node Logic (idIncoming >= 0) ===
				// Expecting ConsensusResultMess from nodes
				if message.Flag == Blockchain.ConsensusResultMess { // <<< Ensure this flag is defined
					if result, resultOk := message.Data.(Blockchain.ConsensusResult); resultOk {
						handleConsensusResult(result) // Process the actual consensus result
					} else {
						log.Error().Int("nodeId", idIncoming).Str("type", message.Flag.String()).
							Msgf("Received ConsensusResultMess flag but data is not ConsensusResult type. Got: %T", message.Data)
					}
				} else {
					log.Warn().Int("nodeId", idIncoming).Str("type", message.Flag.String()).
						Msg("Received unexpected message type from node (expected ConsensusResultMess)")
				}
			}
		}
	}
}
