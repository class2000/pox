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
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"pbftnode/source/Launcher"
	"strconv"
)

var (
	runArg Launcher.ZombieRunArg
)

// RunCmd represents the client command
var RunCmd = &cobra.Command{
	Use:   "running [IP:Port] [NodeId] [Reducing Validator] [NbOfNode] [Throughput] [DelayPerChange] [NbValPerChange]",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.MinimumNArgs(7),
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		zombieArgCreate(args)
		runArg.ZombieArg = zombieArg
		runArg.ZombieArg.NumberOfNode, err = strconv.Atoi(args[3])
		checkInt(err)
		runArg.Throughput, err = strconv.Atoi(args[4])
		checkInt(err)
		runArg.DelayPerChange, err = strconv.Atoi(args[5])
		checkInt(err)
		runArg.NbValPerChange, err = strconv.Atoi(args[6])
		checkInt(err)
		Launcher.ZombieRun(runArg)
	},
}

func init() {
	ZombieCmd.AddCommand(RunCmd)
}

func checkInt(err error) {
	if err != nil {
		log.Error().Msgf("The argument need to be a int. err : %s", err.Error())
	}
}
