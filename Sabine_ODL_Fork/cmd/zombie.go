package cmd

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

import (
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Launcher"
	"strconv"
)

var (
	zombieArg Launcher.ZombieArg
)

// ZombieCmd clientCmd represents the client command
var ZombieCmd = &cobra.Command{
	Use:   "zombie [IP:Port] [NodeId] [Reducing Validator] [nb of Tx]",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
}

func init() {
	rootCmd.AddCommand(ZombieCmd)
	ZombieCmd.PersistentFlags().BoolVarP(&zombieArg.ByBootstrap, "Bootstrap", "b", false, "Connect to a random node by the bootstrap Server")
	ZombieCmd.Flags().StringVar(&validatorSelector, "validationType", "Pivot", "Set the selection of validator {Pivot|Random}")
	ZombieCmd.PersistentFlags().IntVarP(&zombieArg.NumberOfNode, "NbNode", "N", 7, "Set the number of nodes and wait until N nodes are connected to the bootstrap server")
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// clientCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// clientCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func zombieArgCreate(args []string) {
	var err error
	zombieArg.BaseArg = baseArg.NewBaseArg(logLevel)
	zombieArg.Contact = args[0]
	zombieArg.NodeID = args[1]
	zombieArg.NbVal, err = strconv.Atoi(args[2])
	log.Info().Msgf("Contact : %s, nodeId : %s, reduction %s", args[0], args[1], args[2])
	zombieArg.SelectionType = Blockchain.ParseSelectionValidatorType(validatorSelector)
	if err != nil {
		panic("[Reducing Validator] must be an int")
	}
	if zombieArg.NbVal < 4 || zombieArg.NumberOfNode < zombieArg.NbVal {
		panic(fmt.Sprintf("The number of validator should be between 4 and the number of nodes,\n got nbval of %d and a nb of node of %d", zombieArg.NbVal, zombieArg.NumberOfNode))
	}
	if !(zombieArg.SelectionType == Blockchain.PivotSelection || zombieArg.SelectionType == Blockchain.RandomSelection) {
		panic("The selection can only be Pivot or Random")
	}
}
