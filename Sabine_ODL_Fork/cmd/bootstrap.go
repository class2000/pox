// Package cmd /*
package cmd

import (
	"fmt"
	"github.com/rs/zerolog/log"
	"os"
	"pbftnode/source/Launcher"
	"strconv"

	"github.com/spf13/cobra"
)

var (
	bootArg Launcher.BootServerArg
)

// bootstrapCmd represents the bootstrap command
var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap [Port]",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.RangeArgs(0, 1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("bootstrap called")
		bootArg.BaseArg = baseArg.NewBaseArg(logLevel)
		var bootPort string
		if len(args) == 0 {
			var ok bool
			bootPort, ok = os.LookupEnv("BootPort")
			if !ok {
				log.Fatal().Msg("The env variable BootPort or the port argument is not set")
			}
		} else {
			bootPort = args[0]
		}
		_, err := strconv.Atoi(bootPort)
		if err != nil {
			log.Fatal().Msgf("The port need to be a number")
		}
		bootArg.BootstrapPort = bootPort
		Launcher.BootServer(bootArg)
		fmt.Println("closed")
	},
}

func init() {
	rootCmd.AddCommand(bootstrapCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// bootstrapCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// bootstrapCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
