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
	"fmt"
	"os"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"pbftnode/source/Launcher" // Assuming Launcher contains DealerArg definition
)

var dealerArg Launcher.DealerArg // Use the struct from Launcher package

// dealerCmd represents the dealer command
var dealerCmd = &cobra.Command{
	Use:   "dealer [IP:Port] [Port?]",
	Short: "Starts the PBFT Dealer node.",
	Long: `The Dealer acts as a gateway for clients and potentially the Ryu Primary.
It connects to the bootstrap server to find PBFT nodes and forwards transactions/requests.
It can also listen for gRPC requests from the Ryu Primary controller.

[IP:Port] is the address of the bootstrap server.
[Port?] is the optional port for traditional client connections (can also use env var).`,
	Args: cobra.RangeArgs(1, 2), // Expects bootstrap address, optional client port
	Run: func(cmd *cobra.Command, args []string) {
		dealerArg.BaseArg = baseArg.NewBaseArg(logLevel) // Initialize base args (logging)
		dealerArg.Contact = args[0]                      // Bootstrap server address

		// Determine traditional client income port
		var incomePort string
		if len(args) == 1 {
			var ok bool
			incomePort, ok = os.LookupEnv("IncomePort")
			if !ok {
				// If GrpcPort is set, IncomePort might be optional
				if dealerArg.GrpcPort == "" {
					log.Fatal().Msg("The env variable IncomePort or the port argument is not set, and no gRPC port specified.")
				} else {
					log.Info().Msg("IncomePort not specified, will only listen for gRPC requests.")
					// Keep incomePort empty or set to a specific value indicating disabled?
				}
			}
		} else {
			incomePort = args[1]
		}

		// Validate incomePort if provided
		if incomePort != "" {
			_, err := strconv.Atoi(incomePort)
			if err != nil {
				log.Fatal().Err(err).Msgf("The traditional client income port '%s' must be a number.", incomePort)
			}
			dealerArg.IncomePort = incomePort
		} else {
			dealerArg.IncomePort = "" // Explicitly set to empty if not used
		}

		// Note: dealerArg.GrpcPort is already populated by Cobra via the flag definition in init()

		// --- TODO: Populate ReplicaAddresses if needed ---
		// Example: dealerArg.ReplicaAddresses = []string{"127.0.0.1:50052", ...}
		// This might come from flags, env vars, or config file later.
		// For now, it might be empty, and the dealer's RequestConsensus handler
		// will fallback to using the proposed action.
		// ---

		fmt.Println("Starting PBFT Dealer...")
		Launcher.Dealer(dealerArg) // Call the main dealer logic
		fmt.Println("Dealer closed.")
	},
}

func init() {
	rootCmd.AddCommand(dealerCmd)

	// --- Flag Definitions ---
	dealerCmd.Flags().IntVarP(&dealerArg.NbOfNode, "NbNode", "N", 0, "Set the expected number of PBFT nodes (waits for this many before starting fully)")
	dealerCmd.Flags().BoolVar(&dealerArg.RandomDistrib, "RandomDistrib", false, "Distribute traditional client transactions randomly to one node (default: broadcast)")

	// --- ADDED FLAG ---
	// Default value can be set here or taken from Launcher.DefaultDealerGrpcPort
	dealerCmd.Flags().StringVar(&dealerArg.GrpcPort, "grpcPort", Launcher.DefaultDealerGrpcPort, "Port for the gRPC server listening for Ryu Primary")
	// --- /ADDED FLAG ---

	// Example for adding replica addresses later (could be comma-separated string)
	dealerCmd.Flags().StringSliceVar(&dealerArg.ReplicaAddresses, "replicaAddrs", []string{}, "Comma-separated list of Ryu Replica addresses (e.g., host1:port1,host2:port2)")

}