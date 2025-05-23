package Launcher

import (
	"encoding/base64"
	"fmt"
	"github.com/rs/zerolog/log"
	"os"
	"pbftnode/source/Blockchain"
)

type ArgTool struct {
	BaseArg
	TypeTool  TypeTool
	PrintFile string
	Separator byte
	NbNode    int
}

type TypeTool uint8

const (
	GetNodeID TypeTool = iota
)

var toolMap = map[string]TypeTool{
	"GetNodeID": GetNodeID,
}

func ParseToolType(str string) TypeTool {
	c, ok := toolMap[str]
	if !ok {
		log.Panic().Msgf("Unknown type %s", str)
	}
	return c
}

func ToolMain(arg ArgTool) {
	arg.init()
	output := os.Stdout
	var err error
	if arg.PrintFile != "" {
		output, err = os.OpenFile(arg.PrintFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		check(err)
	}
	switch arg.TypeTool {
	case GetNodeID:
		generateNodeId(arg, arg.NbNode, arg.Separator, output)
	}
	err = output.Close()
	check(err)
}

func generateNodeId(arg ArgTool, nbNode int, separator byte, output *os.File) {
	validator := Blockchain.Validators{}
	validator.GenerateAddresses(nbNode)

	text := fmt.Sprintf("id%cPublicKay\n", separator)
	_, err := output.WriteString(text)
	check(err)
	listVal := validator.GetNodeList()
	for i, publicKey := range listVal {
		text = fmt.Sprintf("%d%c%s\n", i, separator, base64.StdEncoding.EncodeToString(publicKey))
		_, err := output.WriteString(text)
		check(err)
	}
}
