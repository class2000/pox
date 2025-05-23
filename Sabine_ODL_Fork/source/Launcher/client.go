package Launcher

import (
	"bufio"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog/log"
	"net"
	"os"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Socket"
	"strconv"
	"strings"
	"time"
)

type ClientArg struct {
	BaseArg
	Contact       string
	NodeID        string
	NumberOfNode  int
	ByBootstrap   bool
	SelectionType Blockchain.SelectionValidatorType
}

func Client(arg ClientArg) {
	arg.init()

	fmt.Print("NODE" + arg.NodeID)

	Contact := arg.Contact
	if arg.ByBootstrap {
		Contact, _, _ = getAContact(arg.Contact)
	}

	log.Print("Try to connect with ", Contact)
	conn, err := net.Dial("tcp", Contact)
	if err != nil {
		log.Fatal().Msgf("Error connecting: %s", err.Error())
	}
	id := Socket.ExchangeIdClient(nil, conn)
	fmt.Println("Connected with : ", id)
	Socket.GobLoader()
	encoder := gob.NewEncoder(conn)
	decoder := gob.NewDecoder(conn)
	validator := Blockchain.Validators{}
	validator.GenerateAddresses(arg.NumberOfNode)
	selector := arg.SelectionType.CreateValidatorSelector(Blockchain.ArgsSelector{})
	go handleBlockReturn(decoder, arg.interruptChan, &validator)
	reader := bufio.NewReader(os.Stdin)
	wallet := Blockchain.NewWallet("NODE" + arg.NodeID)

	for len(arg.interruptChan) == 0 {
		// Read in input until newline, Enter key.
		input, _ := reader.ReadString('\n')
		res := strings.Split(strings.Split(string(input), "\n")[0], " ")
		var transac *Blockchain.Transaction
		switch res[0] {
		case "VarieValid":
			i, err := strconv.Atoi(res[1])
			if err != nil {
				log.Error().Msg(err.Error())
			}
			if !validator.IsSizeValid(i) {
				err := fmt.Errorf("The given size %d is invalid, number of node: %d", i, validator.GetNumberOfValidator())
				println(err.Error())
			}
			newListProposal := selector.GenerateNewValidatorListProposition(&validator, i, -1)
			commande := Blockchain.Commande{
				Order:           Blockchain.VarieValid,
				Variation:       i,
				NewValidatorSet: newListProposal,
			}
			transac = wallet.CreateTransaction(commande)
			str, _ := json.MarshalIndent(transac, "", "  ")
			fmt.Println(string(str))
		default:
			transac = wallet.CreateBruteTransaction([]byte(input))
		}
		message := Blockchain.Message{
			Flag:        Blockchain.TransactionMess,
			Data:        transac,
			ToBroadcast: Blockchain.DefaultBehaviour,
		}

		err = encoder.Encode(message)
		if err != nil {
			log.Fatal().Msgf("Error connecting: %s", err.Error())
		}
	}
	defer func() {
		arg.close()
		_ = conn.Close()
	}()
}

func getAContact(contact string) (string, int, []string) {
	var try = nbTry
	var err error
	var conn net.Conn

	for try > 0 && conn == nil {
		conn, err = net.Dial(connType, contact)
		if err != nil {
			log.Error().Msgf("Error Connection to bootstrap Server: %s, test %d/%d", err.Error(), 1+nbTry-try, nbTry)
			time.Sleep(1 * time.Second)
		}
		try--
	}
	if try == 0 && err != nil {
		log.Fatal().Msgf("Error Connection to bootstrap Server: %s", err.Error())
	}

	//Send the ID of the listening socket
	encoder := gob.NewEncoder(conn)
	err = encoder.Encode(strClient)
	if err != nil {
		log.Fatal().Msgf("Error encoding listening socket data: %s", err.Error())
	}

	var listContact []string
	//Receiving the other nodes information
	decoder := gob.NewDecoder(conn)
	err = decoder.Decode(&listContact)
	if err != nil {
		log.Fatal().Msgf("Error decoding: %s", err.Error())
	}
	err = conn.Close()
	var retour string
	if len(listContact) == 0 {
		log.Error().Msgf("No node to contact")
		retour = ""
	} else {
		retour = listContact[0]
	}
	//return listContact[rand.Intn(len(listContact))]
	// The node need to be a talking node
	return retour, len(listContact), listContact
}

func handleBlockReturn(decoder *gob.Decoder, c chan os.Signal, validators *Blockchain.Validators) {
	for len(c) == 0 {
		var message Blockchain.Message
		err := decoder.Decode(&message)
		if err != nil {
			fmt.Println("error gob")
		}
		if message.Flag > 0 {
			switch message.Flag {
			case Blockchain.PrePrepare:
				fmt.Println("PrePrepareMess receive")
			case Blockchain.PrepareMess:
				fmt.Println("PrepareMess received")
			case Blockchain.CommitMess:
				fmt.Println("Commit Message received")
			case Blockchain.RoundChangeMess:
				fmt.Println("RoundChange Message received")
			}
		}
	}
}
