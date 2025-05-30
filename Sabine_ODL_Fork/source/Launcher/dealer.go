package Launcher

import (
	"context"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"strings" // Added for string manipulation
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb" // For google.protobuf.Empty

	"github.com/rs/zerolog/log"

	pbftconsensus "pbftnode/proto"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Socket"
)

const nbTry int = 180                     // Already defined
const DefaultDealerGrpcPort = "50051"     // Already defined
const consensusTimeout = 60 * time.Second // Already defined

type DealerArg struct {
	BaseArg
	Contact          string
	NbOfNode         int
	IncomePort       string
	GrpcPort         string
	RandomDistrib    bool
	ReplicaAddresses []string
}

var (
	pendingConsensusRequests = make(map[string]chan *pbftconsensus.ConsensusResponse)
	pendingLinkEventRequests = make(map[string]chan struct{}) // For ReportLinkEvent, just need to know it was processed
	pendingMutex             sync.Mutex
	dealerWallet             = Blockchain.NewWallet("DEALER")
)

type pbftConsensusServer struct {
	pbftconsensus.UnimplementedPBFTConsensusServer
	sender   sender
	numNodes int
}

func newPbftConsensusServer(s sender, numNodes int) *pbftConsensusServer {
	return &pbftConsensusServer{
		sender:   s,
		numNodes: numNodes,
	}
}

// RequestConsensus handles the RPC from the Ryu Primary for packet actions.
func (s *pbftConsensusServer) RequestConsensus(ctx context.Context, req *pbftconsensus.ConsensusRequest) (*pbftconsensus.ConsensusResponse, error) {
	log.Info().Msgf("gRPC Server: Received RequestConsensus from Ryu Primary")
	log.Debug().Msgf("  PacketInfo DPID: %s, InPort: %d", req.PacketInfo.Dpid, req.PacketInfo.InPort)
	log.Debug().Msgf("  Proposed Action: %s", req.ProposedAction.ActionJson)

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
	txHashStr := string(pbftTx.Hash)
	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msg("Created PBFT transaction for SDN request")

	if s.sender == nil {
		log.Error().Msg("gRPC Server: PBFT sender interface is nil")
		return nil, status.Error(codes.Internal, "Dealer internal error: sender not initialized")
	}

	respChan := make(chan *pbftconsensus.ConsensusResponse, 1)
	pendingMutex.Lock()
	if _, exists := pendingConsensusRequests[txHashStr]; exists {
		pendingMutex.Unlock()
		log.Error().Str("txHash", pbftTx.GetHashPayload()).Msg("Duplicate consensus request detected (tx hash already pending).")
		return nil, status.Error(codes.AlreadyExists, "Duplicate consensus request for SDN action (tx hash already pending)")
	}
	pendingConsensusRequests[txHashStr] = respChan
	pendingMutex.Unlock()

	cleanup := func() {
		pendingMutex.Lock()
		delete(pendingConsensusRequests, txHashStr)
		pendingMutex.Unlock()
		log.Debug().Str("txHash", pbftTx.GetHashPayload()).Msg("Removed pending SDN request from map.")
	}
	defer cleanup()

	nodesToSignalForSend := s.numNodes
	if nodesToSignalForSend == 0 {
		nodesToSignalForSend = 1
	}

	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msgf("Broadcasting SDN PBFT transaction to %d nodes", nodesToSignalForSend)
	s.sender.Add(nodesToSignalForSend)
	go func(txToSend Blockchain.Transaction) {
		s.sender.send(txToSend, true, Blockchain.AskToBroadcast)
	}(*pbftTx)

	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msgf("Waiting up to %v for SDN consensus result...", consensusTimeout)
	select {
	case response := <-respChan:
		log.Info().Str("txHash", pbftTx.GetHashPayload()).
			Bool("success", response.ConsensusReached).
			Str("status", response.StatusMessage).
			Msg("Received SDN consensus result.")
		return response, nil
	case <-time.After(consensusTimeout):
		log.Error().Str("txHash", pbftTx.GetHashPayload()).Msgf("Timeout after %v waiting for SDN consensus result.", consensusTimeout)
		return nil, status.Errorf(codes.DeadlineExceeded, "SDN consensus timed out after %v", consensusTimeout)
	case <-ctx.Done():
		log.Warn().Str("txHash", pbftTx.GetHashPayload()).Err(ctx.Err()).Msg("SDN RequestConsensus cancelled by client.")
		return nil, status.Error(codes.Canceled, "SDN request cancelled by client")
	}
}

// ReportLinkEvent handles RPC from Ryu Primary about link status changes.
func (s *pbftConsensusServer) ReportLinkEvent(ctx context.Context, req *pbftconsensus.LinkEventInfo) (*emptypb.Empty, error) {
	log.Info().Msgf("gRPC Server: Received ReportLinkEvent: %s:%d <-> %s:%d is %s",
		req.Dpid1, req.Port1, req.Dpid2, req.Port2, req.Status.String())

	// Convert proto enum to string for our internal LinkEventInput
	var statusStr string
	switch req.Status {
	case pbftconsensus.LinkEventInfo_LINK_UP:
		statusStr = "LINK_UP"
	case pbftconsensus.LinkEventInfo_LINK_DOWN:
		statusStr = "LINK_DOWN"
	default:
		statusStr = "LINK_STATUS_UNSPECIFIED" // Or handle as an error
		log.Warn().Str("proto_status", req.Status.String()).Msg("Received unspecified link status in ReportLinkEvent")
	}

	linkInput := Blockchain.LinkEventInput{
		Dpid1:       req.Dpid1,
		Port1:       req.Port1,
		Dpid2:       req.Dpid2,
		Port2:       req.Port2,
		Status:      statusStr,
		TimestampNs: req.TimestampNs, // Pass along the timestamp
	}
	pbftTx := Blockchain.NewTransaction(linkInput, *dealerWallet)
	if pbftTx == nil {
		errMsg := "Failed to create PBFT transaction for link event"
		log.Error().Msg(errMsg)
		return nil, status.Error(codes.Internal, errMsg)
	}
	// txHashStr := string(pbftTx.Hash) //  <<<<< COMPILE ERROR: declared and not used. Commented out.
	// If you need txHashStr for logging or a pending map later, uncomment and use it.
	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msg("Created PBFT transaction for link event")

	if s.sender == nil {
		log.Error().Msg("gRPC Server (ReportLinkEvent): PBFT sender interface is nil")
		return nil, status.Error(codes.Internal, "Dealer internal error: sender not initialized")
	}

	nodesToSignalForSend := s.numNodes
	if nodesToSignalForSend == 0 {
		nodesToSignalForSend = 1
	}

	log.Info().Str("txHash", pbftTx.GetHashPayload()).Msgf("Broadcasting Link Update PBFT transaction to %d nodes", nodesToSignalForSend)
	s.sender.Add(nodesToSignalForSend)
	go func(txToSend Blockchain.Transaction) {
		s.sender.send(txToSend, true, Blockchain.AskToBroadcast) // High priority
	}(*pbftTx)

	return &emptypb.Empty{}, nil
}

func handleConsensusResult(result Blockchain.ConsensusResult) {
	txHashStr := string(result.TxHash)
	log.Info().Str("txHash", result.GetHashPayload()).
		Bool("success", result.Success).
		Str("fromNode", base64.StdEncoding.EncodeToString(result.ReportingNode)).
		Msg("Dealer received ConsensusResult notification from a node")

	pendingMutex.Lock()
	respChan, exists := pendingConsensusRequests[txHashStr]
	// delete(pendingConsensusRequests, txHashStr) // Delete immediately if found, or defer in RequestConsensus is better
	pendingMutex.Unlock()

	if exists {
		response := &pbftconsensus.ConsensusResponse{
			FinalAction:      result.FinalAction,
			ConsensusReached: result.Success,
			StatusMessage:    result.ResultMessage,
		}
		select {
		case respChan <- response:
			log.Debug().Str("txHash", result.GetHashPayload()).Msg("Sent consensus result to waiting gRPC handler.")
		default:
			log.Warn().Str("txHash", result.GetHashPayload()).Msg("Consensus result received, but gRPC handler channel was blocked or request already handled.")
		}
		// The cleanup defer in RequestConsensus will handle deleting from the map
	} else {
		log.Warn().Str("txHash", result.GetHashPayload()).Msg("Received consensus result for an unknown or already completed/timed-out SDN request.")
	}
}

func Dealer(arg DealerArg) {
	var continu = true
	var mySender sender
	var listContact []string
	var nContact int
	var grpcServer *grpc.Server
	var wg sync.WaitGroup

	arg.init()
	Socket.GobLoader()

	go func() {
		<-arg.interruptChan
		log.Info().Msg("Dealer: Received interrupt signal...")
		continu = false // Signal other loops

		// Order of shutdown:
		// 1. Stop accepting new gRPC connections/requests
		// 2. Stop accepting new TCP client/node connections
		// 3. Wait for ongoing processing
		// 4. Close internal senders

		if grpcServer != nil {
			log.Info().Msg("Dealer: Initiating graceful gRPC server stop...")
			grpcServer.GracefulStop()
			log.Info().Msg("Dealer: gRPC server stopped.")
		}

		// Listener closure is now handled by its own interrupt logic via shutdownChan

		if mySender != nil {
			log.Info().Msg("Dealer: Closing sender connections to PBFT nodes...")
			mySender.close() // Close connections to PBFT nodes
			log.Info().Msg("Dealer: Sender connections closed.")
		}
	}()

	// --- Bootstrap Connection ---
	if arg.NbOfNode > 0 {
		var try int = nbTry
		ctxBoot, cancelBoot := context.WithCancel(context.Background())
		defer cancelBoot()

		bootstrapWaitDone := make(chan struct{})
		go func() {
			defer close(bootstrapWaitDone)
			select {
			case <-arg.interruptChan:
				cancelBoot()
			case <-ctxBoot.Done():
			}
		}()

		for try > 0 && nContact < arg.NbOfNode && continu { // Check continu
			if ctxBoot.Err() != nil {
				log.Warn().Msg("Bootstrap waiting cancelled.")
				arg.close()
				return
			}
			_, nContact, listContact = getAContact(arg.Contact)
			log.Info().Msgf("%d nodes connected to the bootstrap server (waiting for %d)", nContact, arg.NbOfNode)
			if nContact < arg.NbOfNode {
				select {
				case <-time.After(1 * time.Second):
				case <-ctxBoot.Done():
				}
			}
			try--
		}
		cancelBoot()
		<-bootstrapWaitDone

		if !continu {
			log.Info().Msg("Dealer startup interrupted during bootstrap.")
			arg.close()
			return
		}
		if try == 0 && nContact < arg.NbOfNode {
			log.Fatal().Msg("Too many attempts to connect to bootstrap or not enough nodes")
		}
		log.Info().Msgf("Proceeding with %d connected nodes.", nContact)
	} else {
		_, nContact, listContact = getAContact(arg.Contact) // Still get contacts if NbNode=0
		log.Info().Msgf("%d nodes connected to the bootstrap server", nContact)
		if nContact == 0 {
			log.Warn().Msg("No nodes reported by bootstrap server.")
		}
	}

	effectiveNodeCount := len(listContact)
	if effectiveNodeCount > 0 {
		mySender = newMultiSend(listContact, !arg.RandomDistrib)
		if ms, ok := mySender.(*multiSend); ok {
			ms.nodeCount = effectiveNodeCount
		}
	} else {
		log.Warn().Msg("No PBFT node contacts obtained, sender will not be functional for broadcasting.")
		// Create a dummy sender or handle this case more gracefully if sender is essential
		mySender = &dummySender{} // Implement a dummy sender that logs/errors out
	}

	if arg.IncomePort != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msgf("Dealer: Starting listener on port %s (for clients AND node results)", arg.IncomePort)
			createIncomingListener(&arg, mySender) // Pass arg for interruptChan
			log.Info().Msg("Dealer: Incoming listener stopped.")
		}()
	} else {
		log.Warn().Msg("Dealer: IncomePort not specified. Nodes cannot send ConsensusResult back via this port.")
	}

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

	log.Info().Msg("Dealer: Initialization complete. Waiting for components to stop...")
	wg.Wait() // Wait for listener and gRPC server goroutines to finish

	log.Info().Msg("Dealer: Shutdown sequence complete.")
	arg.close()
}

// Dummy sender for when no nodes are available
type dummySender struct{}

func (ds *dummySender) close() { log.Debug().Msg("Dummy sender close called.") }
func (ds *dummySender) send(transac Blockchain.Transaction, priority bool, broadcastType Blockchain.BroadcastType) {
	log.Error().Str("txHash", transac.GetHashPayload()).Msg("Dummy sender: Cannot send, no nodes available.")
	// If send is part of a WaitGroup, we need to call Done here
	// This depends on how Add/Done are used. Assuming sender.Add() is called before sender.send()
}
func (ds *dummySender) Wait()   {}
func (ds *dummySender) Add(int) {}

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
	// Defer listenSocket.Close() here will close it when createIncomingListener exits.
	// This means if main interrupt happens, this goroutine will exit, and then the listener closes.

	log.Info().Msgf("Listener started on %s for clients and node results", arg.IncomePort)
	shutdownChan := make(chan struct{})
	var connWg sync.WaitGroup

	go func() {
		// This goroutine waits for the main interrupt signal from BaseArg (arg.interruptChan).
		// When received, it closes the listener, which unblocks the Accept() call,
		// and then signals all active connection handlers to stop.
		<-arg.BaseArg.interruptChan
		log.Info().Msg("Incoming listener: Main interrupt signal received. Closing listener socket.")
		listenSocket.Close() // This will cause the Accept() in the loop below to return an error.
		close(shutdownChan)  // Signal all active handleConnection goroutines to shut down.
	}()

	for {
		conn, err := listenSocket.Accept()
		if err != nil {
			// Check if the error is due to the listener being closed (expected on shutdown)
			if strings.Contains(err.Error(), "use of closed network connection") {
				log.Info().Msg("Incoming listener: Accept loop cleanly exited due to listener closure.")
			} else {
				log.Error().Err(err).Msg("Error accepting connection on incoming listener.")
			}
			// Wait for any active connection handlers to finish before this goroutine exits.
			connWg.Wait()
			return // Exit the accept loop.
		}

		log.Info().Str("remoteAddr", conn.RemoteAddr().String()).Msg("Accepted incoming connection")
		connWg.Add(1)
		go handleConnection(conn, arg, send, &connWg, shutdownChan)
	}
}

func handleConnection(conn net.Conn, arg *DealerArg, send sender, wg *sync.WaitGroup, shutdown <-chan struct{}) {
	defer wg.Done()
	defer conn.Close()

	idIncoming := Socket.ExchangeIdServer(nil, conn) // Perform ID exchange
	log.Info().Str("remoteAddr", conn.RemoteAddr().String()).Int("id", idIncoming).Msg("Handling incoming connection")

	decoder := gob.NewDecoder(conn)
	msgChan := make(chan Blockchain.Message, 10)
	errChan := make(chan error, 1)
	decodeWg := sync.WaitGroup{} // To wait for the decoder goroutine

	decodeWg.Add(1)
	go func() {
		defer decodeWg.Done()
		defer close(msgChan) // Close msgChan when decoder exits
		for {
			var message Blockchain.Message
			if err := decoder.Decode(&message); err != nil {
				select {
				case <-shutdown: // If shutdown is signaled, decoder might get an error, don't propagate
				default:
					if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
						errChan <- err
					} else if err == io.EOF {
						errChan <- io.EOF
					}
				}
				return
			}
			select {
			case msgChan <- message:
			case <-shutdown:
				return
			case <-time.After(1 * time.Second): // Timeout to prevent deadlock if msgChan is full
				log.Warn().Int("id", idIncoming).Msg("Timeout sending decoded message to handler channel in handleConnection.")
			}
		}
	}()

	for {
		select {
		case <-shutdown:
			log.Info().Int("id", idIncoming).Msg("Stopping handler due to global shutdown signal.")
			conn.Close()    // Force decoder to unblock
			decodeWg.Wait() // Wait for decoder goroutine to finish processing/exiting
			return

		case err := <-errChan:
			if err == io.EOF {
				log.Info().Int("id", idIncoming).Msg("Connection closed by remote peer (EOF).")
			} else {
				log.Error().Int("id", idIncoming).Err(err).Msg("Error decoding message in handler.")
			}
			decodeWg.Wait()
			return

		case message, ok := <-msgChan:
			if !ok { // msgChan was closed by the decoder goroutine
				log.Info().Int("id", idIncoming).Msg("Message channel closed (decoder exited), connection handler exiting.")
				decodeWg.Wait() // Should already be done, but good for safety
				return
			}

			if idIncoming == -1 { // Client Logic
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
									send.send(tx, prio, Blockchain.AskToBroadcast)
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
			} else { // Node Logic (idIncoming >= 0)
				if message.Flag == Blockchain.ConsensusResultMess {
					if result, resultOk := message.Data.(Blockchain.ConsensusResult); resultOk {
						handleConsensusResult(result)
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
