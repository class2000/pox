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
	"github.com/spf13/cobra"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Launcher"
)

var (
	clientArg Launcher.ClientArg
)

// clientCmd represents the client command
var clientCmd = &cobra.Command{
	Use:   "client [IP:Port] [NodeId]",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clientArg.BaseArg = baseArg.NewBaseArg(logLevel)
		clientArg.Contact = args[0]
		clientArg.NodeID = args[1]
		clientArg.SelectionType = Blockchain.ParseSelectionValidatorType(validatorSelector)
		Launcher.Client(clientArg)
	},
}

func init() {
	rootCmd.AddCommand(clientCmd)
	clientCmd.Flags().BoolVarP(&clientArg.ByBootstrap, "Bootstrap", "b", false, "Connect to a random node by the bootstrap Server")
	clientCmd.Flags().IntVarP(&clientArg.NumberOfNode, "NodeNumber", "N", Blockchain.NumberOfNodes, "The number of Nodes in the networks")
	clientCmd.Flags().StringVar(&validatorSelector, "validationType", "Pivot", "Set the selection of validator {Pivot|Random|Centric}")
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// clientCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// clientCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
