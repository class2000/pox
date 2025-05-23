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
	"github.com/spf13/cobra"
	"pbftnode/source/Launcher"
	"strconv"
)

var (
	zatencyLatArg Launcher.ZombieLatArg
)

// LatencyCmd represents the Latency command
var LatencyCmd = &cobra.Command{
	Use:   "latency [IP:Port] [NodeId] [Reducing Validator] [nb of Tx]",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.ExactArgs(4),
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		zombieArgCreate(args)
		zatencyLatArg.ZombieArg = zombieArg
		zatencyLatArg.NbTx, err = strconv.Atoi(args[3])
		if err != nil {
			panic("[nb of Tx] must be an int")
		}
		Launcher.ZombieLat(zatencyLatArg)
	},
}

func init() {
	ZombieCmd.AddCommand(LatencyCmd)
}
