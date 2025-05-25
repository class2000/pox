package Launcher

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	_ "net/http/pprof" // Import for pprof side effects
	"os"
	"strconv"
	"time"

	// gRPC imports
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure" // For insecure gRPC connections

	// Consider adding keepalive if connection drops are suspected later
	// "google.golang.org/grpc/keepalive"

	// Logging library
	"github.com/rs/zerolog/log"

	// Project-specific imports
	pbftconsensus "pbftnode/proto" // Import generated protobuf Go code
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Consensus"
	"pbftnode/source/Blockchain/Socket"
)

// NodeArg holds all arguments and configuration for launching a PBFT node.
type NodeArg struct {
	BaseArg
	BootAddr       string
	NodeId         string
	NodeNumber     int
	SaveFile       string
	MultiSaveFile  bool
	ListeningPort  string
	HttpChain      string
	HttpMetric     string
	Param          Blockchain.ConsensusParam
	PPRof          bool
	Control        bool
	Sleep          int
	RegularSave    int
	DelayParam     DelayParam
	RyuReplicaAddr string
}

// DelayParam holds parameters for network delay simulation.
type DelayParam struct {
	DelayType  string  // Type of delay distribution ("NoDelay", "Normal", "Poisson", "Fix")
	AvgDelay   int     // Average delay in milliseconds
	StdDelay   int     // Standard deviation for "Normal" delay
	matAdj     [][]int // Adjacency matrix loaded from file
	MatAdjPath string  // Path to the adjacency matrix CSV file (optional)
}

// Node is the main function to start and run a PBFT node instance.
func Node(arg NodeArg) {
	var srv *http.Server                                  // HTTP server instance (for chain/metric viewing)
	var replicaClient pbftconsensus.RyuReplicaLogicClient // gRPC client stub for Ryu Replica
	var replicaConn *grpc.ClientConn                      // gRPC connection object

	arg.init()                  // Initialize base arguments (logging, signal handling)
	var Saver *Blockchain.Saver // Blockchain saver instance
	defLoggerPanic()            // Setup panic recovery logging
	defer arg.close()           // Ensure base resources are closed on exit

	// Optional sleep before starting network activity
	if arg.Sleep > 0 {
		log.Info().Int("durationMs", arg.Sleep).Msg("Sleeping before starting connections...")
		time.Sleep(time.Duration(arg.Sleep) * time.Millisecond)
	}

	// Start pprof HTTP server if enabled
	if arg.PPRof {
		go func() {
			log.Info().Msg("Starting pprof HTTP server on [::]:6060")
			// Log error if ListenAndServe fails (e.g., port conflict)
			if err := http.ListenAndServe("[::]:6060", nil); err != nil {
				log.Error().Err(err).Msg("pprof server failed to start")
			}
		}()
	}

	// Create the node's wallet based on its ID
	var wallet = Blockchain.NewWallet(fmt.Sprintf("NODE%s", arg.NodeId))

	// Load network delay adjacency matrix if specified
	arg.loadMatrix() // Loads into arg.DelayParam.matAdj and arg.Param.SelectorArgs.MatAdj

	// --- Establish gRPC Connection to Ryu Replica (Non-Blocking Dial) ---
	if arg.RyuReplicaAddr != "" {
		log.Info().Msgf("Initiating non-blocking gRPC connection attempt to Ryu Replica at %s", arg.RyuReplicaAddr)
		// Configure gRPC dial options
		opts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
		// Dial without context timeout (or a very long one), connection happens in background
		conn, err := grpc.Dial(arg.RyuReplicaAddr, opts...) // Use Dial instead of DialContext

		if err != nil {

			log.Error().Err(err).Msgf("Failed to initiate gRPC connection to Ryu Replica at %s. SDN validation will likely fail.", arg.RyuReplicaAddr)
			replicaClient = nil
			replicaConn = nil
		} else {

			log.Info().Msgf("gRPC connection initiated to Ryu Replica at %s (will connect lazily)", arg.RyuReplicaAddr)
			replicaConn = conn
			replicaClient = pbftconsensus.NewRyuReplicaLogicClient(conn)
		}
	} else {
		log.Warn().Msg("Ryu Replica address not provided. Node cannot participate in SDN control validation.")
	}

	arg.Param.ReplicaClient = replicaClient

	consensus := Consensus.NewPBFTStateConsensus(wallet, arg.NodeNumber, arg.Param)

	delay := Socket.NewNodeDelay(createDelay(arg.DelayParam, consensus.GetId()), true)

	var comm = Socket.NewNetSocketBoot(consensus, arg.BootAddr, arg.ListeningPort, delay)

	consensus.SetSocketHandler(comm)

	comm.InitBootstrapedCo()

	if arg.Control {
		consensus.SetControlInstruction(true)
	}

	if arg.HttpMetric != "" {
		consensus.SetHTTPViewer(arg.HttpMetric)
	}

	log.Info().Msgf("Node %s initialized. Expected first proposer is %d", arg.NodeId, consensus.GetBlockchain().GetProposerId())

	// Start HTTP blockchain viewer server if configured
	if arg.HttpChain != "" {
		srv = Blockchain.HttpBlockchainViewer(consensus.GetBlockchain(), arg.HttpChain)
	}

	// Initialize blockchain saver and configure save function
	Saver = Blockchain.NewSaver(arg.SaveFile, consensus)
	if arg.MultiSaveFile {
		consensus.GetBlockchain().Save = func() { Saver.AskMultiSave() }
	} else {
		consensus.GetBlockchain().Save = func() { Saver.AskToSave() }
	}

	// Start periodic blockchain saving if configured
	var saveTicker *time.Ticker
	if arg.RegularSave > 0 {
		log.Info().Int("intervalMinutes", arg.RegularSave).Msg("Starting periodic blockchain saver.")
		saveTicker = time.NewTicker(time.Duration(arg.RegularSave) * time.Minute)
		go func() {
			for range saveTicker.C { // Loop until ticker is stopped
				log.Info().Msgf("Periodic save triggered (every %d min)", arg.RegularSave)
				consensus.GetBlockchain().Save()
			}
			log.Info().Msg("Periodic blockchain saver stopped.")
		}()
	}

	// --- Main loop: Wait for interrupt signal ---
	<-arg.interruptChan // Block until Ctrl+C or other interrupt
	log.Info().Msgf("Node %s received interrupt signal. Shutting down gracefully...", arg.NodeId)

	// --- Graceful Shutdown Sequence ---
	// Stop periodic saver
	if saveTicker != nil {
		saveTicker.Stop()
		log.Info().Msg("Stopped periodic saver.")
	}

	// Perform final blockchain save and wait for completion
	log.Info().Msg("Performing final blockchain save...")
	consensus.GetBlockchain().Save()
	if Saver != nil { // Ensure Saver was initialized
		Saver.Wait()
	}
	log.Info().Msg("Final save complete.")

	// Shutdown optional HTTP servers
	if srv != nil {
		log.Info().Msg("Shutting down HTTP chain viewer...")
		ctxHttp, cancelHttp := context.WithTimeout(context.Background(), 5*time.Second)
		if err := srv.Shutdown(ctxHttp); err != nil {
			log.Error().Err(err).Msg("Error shutting down HTTP chain viewer")
		}
		cancelHttp() // Release context resources
		log.Info().Msg("HTTP chain viewer shut down.")
	}
	// Add similar shutdown for metrics HTTP server if it were started separately

	// Close peer-to-peer network connections
	log.Info().Msg("Closing P2P network connections...")
	if comm != nil { // Ensure comm was initialized
		comm.Close()
	}
	log.Info().Msg("P2P network connections closed.")

	// Close gRPC connection to Ryu Replica
	if replicaConn != nil {
		log.Info().Msg("Closing gRPC connection to Ryu Replica...")
		if err := replicaConn.Close(); err != nil {
			log.Error().Err(err).Msg("Error closing gRPC connection")
		}
		log.Info().Msg("gRPC connection closed.")
	}

	// Close the consensus module (stops internal goroutines, closes channels)
	log.Info().Msg("Closing consensus module...")
	if consensus != nil { // Ensure consensus was initialized
		consensus.Close()
	}
	log.Info().Msg("Consensus module closed.")

	log.Info().Msgf("Node %s shutdown complete.", arg.NodeId)
}

// createDelay creates the delay configuration for sockets based on node parameters.
func createDelay(arg DelayParam, nodeId int) Socket.DelayConfig {
	delayType := Socket.ParseDelayType(arg.DelayType)
	var matrix []int = nil
	if arg.matAdj != nil {
		// Ensure nodeId is within the bounds of the loaded matrix
		if nodeId >= 0 && nodeId < len(arg.matAdj) {
			matrix = arg.matAdj[nodeId]
		} else {
			log.Warn().Int("nodeId", nodeId).Int("matrixSize", len(arg.matAdj)).Msg("Node ID out of bounds for adjacency matrix, using default delay.")
		}
	}

	return Socket.DelayConfig{
		DelayType: delayType,
		AvgDelay:  arg.AvgDelay,
		StdDelay:  arg.StdDelay,
		Matrix:    matrix,
	}
}

// loadMatrix loads the adjacency matrix from a CSV file for the DelayParam struct.
func (param *DelayParam) loadMatrix() {
	if param.MatAdjPath != "" {
		param.matAdj = readFullMatrix(param.MatAdjPath)
	}
}

// loadMatrix loads the matrix for the NodeArg and sets it in Param.SelectorArgs.
func (arg *NodeArg) loadMatrix() {
	if arg.DelayParam.MatAdjPath != "" {
		arg.DelayParam.matAdj = readFullMatrix(arg.DelayParam.MatAdjPath)
		// Pass the loaded matrix to the consensus parameters for potential use by selectors
		arg.Param.SelectorArgs.MatAdj = arg.DelayParam.matAdj
	}
}

// readFullMatrix reads the adjacency matrix from the specified CSV path.
func readFullMatrix(path string) [][]int {
	csvFile, err := os.Open(path)
	if err != nil {
		log.Error().Err(err).Str("path", path).Msg("Cannot open the adjacency matrix CSV file")
		return nil // Return nil on error
	}
	defer csvFile.Close() // Ensure file is closed

	csvlines, err := csv.NewReader(csvFile).ReadAll()
	if err != nil {
		log.Error().Err(err).Str("path", path).Msg("Cannot read the adjacency matrix CSV file")
		return nil
	}

	if len(csvlines) == 0 {
		log.Warn().Str("path", path).Msg("Adjacency matrix CSV file is empty")
		return nil
	}

	nb_node := len(csvlines)
	lineMatrix := make([][]int, nb_node)

	for index, csvline := range csvlines {
		if len(csvline) != nb_node {
			log.Error().Str("path", path).Int("rowIndex", index).Int("expectedCols", nb_node).Int("actualCols", len(csvline)).Msg("Adjacency matrix CSV is not square")
			return nil // Matrix must be square
		}
		lineMatrix[index] = make([]int, nb_node)
		for i, s := range csvline {
			delay, errVal := strconv.Atoi(s)
			if errVal != nil {
				log.Error().Err(errVal).Str("path", path).Int("row", index).Int("col", i).Str("value", s).Msg("Non-integer value found in adjacency matrix CSV")
				return nil // Ensure all values are integers
			}
			lineMatrix[index][i] = delay
		}
	}

	log.Info().Str("path", path).Int("size", nb_node).Msg("Successfully imported the adjacency matrix")
	return lineMatrix
}

func defLoggerPanic() {

	defer func() {
		if r := recover(); r != nil {
			log.Panic().Msgf("Recovered from panic: %v", r)
			os.Exit(1)
		}
	}()
}

func check(err error) {
	if err != nil {
		log.Error().Err(err).Msg("Check function caught an error (DEPRECATED)")
	}
}
