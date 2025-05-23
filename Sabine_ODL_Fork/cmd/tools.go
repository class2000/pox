/*
Copyright © 2023 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"github.com/spf13/cobra"
	"pbftnode/source/Launcher"
)

var toolArg Launcher.ArgTool
var separator string

// toolsCmd represents the tools command
var toolsCmd = &cobra.Command{
	Use:   "tools [GetNodeID]",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		toolArg.BaseArg = baseArg.NewBaseArg(logLevel)
		toolArg.Separator = separator[0]

		toolArg.TypeTool = Launcher.ParseToolType(args[0])

		if err != nil {
			panic(err)
		}
		Launcher.ToolMain(toolArg)
	},
}

func init() {
	rootCmd.AddCommand(toolsCmd)
	toolsCmd.Flags().StringVar(&toolArg.PrintFile, "output", "", "Output csv file")
	toolsCmd.Flags().IntVarP(&toolArg.NbNode, "NbNode", "N", 4, "Set the number of nodes")
	toolsCmd.Flags().StringVarP(&separator, "separator", "s", ",", "Set the separator")

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// toolsCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// toolsCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
