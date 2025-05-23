// Package cmd
/*
Copyright © 2021 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"pbftnode/source/Blockchain"
	"pbftnode/source/Launcher"
	"pbftnode/source/config"
)

var (
	nodeArg           Launcher.NodeArg // Holds arguments passed to the Launcher.Node function
	controlType       string           // Temporary variable to hold control type string from flag
	behaviorTxPool    string           // Temporary variable to hold tx pool behavior string from flag
	validatorSelector string           // Temporary variable to hold validator selector string from flag
)

// nodeCmd represents the node command
var nodeCmd = &cobra.Command{
	Use:   "node [IP:Port] [ID]",
	Short: "Starts a PBFT node instance.",
	Long: `Starts a PBFT node instance which connects to the specified bootstrap server
and participates in the consensus network using the provided node ID.

Example:
  pbftnode node 127.0.0.1:4315 0 --NodeNumber 4 --PoA
  pbftnode node bootstrap.example.com:4315 1 -N 8 --ryuReplicaAddr 127.0.0.1:50052`,
	Args: cobra.ExactArgs(2), // Requires bootstrap address and node ID
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize base arguments (logging, interrupt handling)
		nodeArg.BaseArg = baseArg.NewBaseArg(logLevel)

		// Positional arguments
		nodeArg.BootAddr = args[0]
		nodeArg.NodeId = args[1]

		// Check environment variable for listening port if not set by flag
		if nodeArg.ListeningPort == "" {
			bootPort, ok := os.LookupEnv("ListenPort")
			if ok {
				nodeArg.ListeningPort = bootPort
			}
		}

		// Parse string flags into specific types for ConsensusParam
		nodeArg.Param.ControlType = Blockchain.ControlTypeStr(controlType)
		nodeArg.Param.Behavior = Blockchain.StrToBehavior(behaviorTxPool)
		nodeArg.Param.SelectionType = Blockchain.ParseSelectionValidatorType(validatorSelector)

		// --- Pass the Ryu Replica Address ---
		// The nodeArg.RyuReplicaAddr is already populated by Cobra via the flag definition below.
		// It will be used within Launcher.Node when initializing the gRPC client.
		// ---

		// Launch the node
		Launcher.Node(nodeArg)
	},
}

func init() {
	rootCmd.AddCommand(nodeCmd) // Add the node command to the root command

	// --- Flag Definitions ---
	// Networking & Basic Config
	nodeCmd.Flags().IntVarP(&nodeArg.NodeNumber, "NodeNumber", "N", config.NumberOfNodes, "Total number of Nodes expected in the network")
	nodeCmd.Flags().StringVarP(&nodeArg.ListeningPort, "listeningPort", "p", "", "Listening port for incoming node connections (overrides env var)")
	nodeCmd.Flags().IntVar(&nodeArg.Sleep, "sleep", 0, "Sleep n milliseconds before starting connections")

	// Consensus Behavior
	nodeCmd.Flags().BoolVarP(&nodeArg.Param.Broadcast, "Broadcasting", "b", false, "Broadcast received Prepare/Commit messages (less efficient, for debugging)")
	nodeCmd.Flags().BoolVar(&nodeArg.Param.PoANV, "PoA", false, "Enable Proof-of-Authority non-validator mode (listen directly to proposer)")
	nodeCmd.Flags().StringVar(&behaviorTxPool, "txPoolBehavior", "Nothing", "Transaction pool overload behavior {Nothing|Ignore|Drop}")
	nodeCmd.Flags().BoolVar(&nodeArg.Param.AcceptTxFromUnknown, "acceptUnknownTx", false, "Accept transactions from non-validator nodes")

	// Persistence & Saving
	nodeCmd.Flags().StringVar(&nodeArg.SaveFile, "chainfile", "", "File path to store the blockchain state")
	nodeCmd.Flags().BoolVar(&nodeArg.MultiSaveFile, "multiSaveFile", false, "Save blockchain state in multiple fragmented files")
	nodeCmd.Flags().IntVar(&nodeArg.RegularSave, "regularSave", 0, "Interval (in minutes) for periodic blockchain saving (0 disables)")

	// Performance & Optimization
	nodeCmd.Flags().BoolVar(&nodeArg.Param.RamOpt, "RamOpt", false, "Enable RAM optimizations (e.g., removing old messages)")

	// Network Delay Simulation
	nodeCmd.Flags().IntVar(&nodeArg.DelayParam.AvgDelay, "avgDelay", 0, "Simulated average network delay (ms)")
	nodeCmd.Flags().StringVar(&nodeArg.DelayParam.DelayType, "delayType", "NoDelay", "Type of delay distribution {NoDelay|Normal|Poisson|Fix}")
	nodeCmd.Flags().IntVar(&nodeArg.DelayParam.StdDelay, "stdDelay", 10, "Standard deviation for Normal delay distribution")
	nodeCmd.Flags().StringVar(&nodeArg.DelayParam.MatAdjPath, "adjMatrix", "", "Path to CSV file defining node-to-node delays (overrides avgDelay)")

	// Monitoring & Debugging
	nodeCmd.Flags().StringVar(&nodeArg.HttpChain, "httpChain", "", "HTTP port to expose blockchain data for viewing")
	nodeCmd.Flags().StringVar(&nodeArg.HttpMetric, "httpMetric", "", "HTTP port to expose performance metrics")
	nodeCmd.Flags().BoolVar(&nodeArg.PPRof, "pprof", false, "Enable Go pprof HTTP server on port 6060")
	nodeCmd.Flags().StringVar(&nodeArg.Param.MetricSaveFile, "metricSaveFile", "", "File path to save performance metrics periodically")
	nodeCmd.Flags().IntVar(&nodeArg.Param.TickerSave, "metricTicker", 0, "Interval (in minutes) for periodic metric saving (0 disables)")
	nodeCmd.Flags().IntVar(&nodeArg.Param.RefreshingPeriod, "RefreshingPeriod", 1, "Metric calculation refresh period (seconds)")

	// Feedback Control Loop (FCB) & Validator Selection
	nodeCmd.Flags().BoolVar(&nodeArg.Control, "FCB", false, "Enable Feedback Control Block for dynamic validator adjustment")
	nodeCmd.Flags().StringVar(&controlType, "FCType", "ModelComparison", "Feedback Control type {OneValidator|Hysteresis|ModelComparison}")
	nodeCmd.Flags().StringVar(&nodeArg.Param.ModelFile, "modelFile", "", "Path to the CSV model file for ModelComparison FCB")
	nodeCmd.Flags().IntVar(&nodeArg.Param.ControlPeriod, "ControlPeriod", 10, "Feedback Control execution period (in multiples of RefreshingPeriod)")
	nodeCmd.Flags().StringVar(&validatorSelector, "validationType", "Pivot", "Validator selection strategy {Pivot|Random|Centric}")
	nodeCmd.Flags().Float64Var(&nodeArg.Param.SelectorArgs.Gamma, "gamma", 1.0, "Gamma parameter for Centric validator selection")

	// --- ADDED FLAG ---
	// gRPC Integration
	nodeCmd.Flags().StringVar(&nodeArg.RyuReplicaAddr, "ryuReplicaAddr", "", "Address (host:port) of the Ryu Replica gRPC server for this node")
	// --- /ADDED FLAG ---

}