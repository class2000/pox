package Test

import (
	"bytes"
	"fmt"
	"github.com/rs/zerolog"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/testStruct"
	"testing"
	"time"
)

func TestChannelNode(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	tests := []struct {
		name             string
		nbOfNode         int
		nbOfTransaction  int
		expectedBloc     int
		milliwait        int64
		broadcast        bool
		PoA              bool
		broadcastTransac bool
	}{
		{"Do nothing", 4, 0, 0, 0, true, false, false},
		{"Send a transaction", 4, 1, 1, 0, true, false, false},
		{"Send two transaction", 4, 2, 2, 200, true, false, false},
		{"Send a transaction for more node", 40, 1, 1, 0, true, false, false},
		{"Send a transaction, no broadcasting", 4, 1, 1, 0, false, false, false},
		{"Send a transaction for more node, no broadcasting", 40, 1, 1, 0, false, false, false},
		{"Do nothing, PoA", 4, 0, 0, 0, true, true, false},
		{"Send a transaction, PoA", 4, 1, 1, 0, true, true, false},
		{"Send a transaction, no broadcasting, PoA", 4, 1, 1, 0, false, true, false},
		{"Send two transaction, no Broadcasting, PoA", 4, 2, 2, 200, false, true, false},
		{"Broadcast the transaction for few tx", 20, 30, 30, 5, false, false, true},
		{"Broadcast the transaction for a lot of tx, for few nodes", 4, 200, 200, 5, false, false, true},
		{"Broadcast the transaction for a lot of tx, for a lot nodes", 20, 200, 200, 15, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			experiment := testStruct.NewTestExperiementChannel(tt.nbOfNode, tt.broadcast, tt.PoA)
			blockchain := experiment.ListNode[0].GetBlockchain()
			fmt.Println("expected validator : ", blockchain.GetProposerId())
			for IDTransac := 0; IDTransac < tt.nbOfTransaction; IDTransac++ {
				//socket := experiment.ListSocket[rand.Intn(tt.nbOfNode)]
				socket := experiment.ListSocket[blockchain.GetProposerId()]
				transac := Blockchain.NewBruteTransaction([]byte(fmt.Sprintf("Transac%d", IDTransac)), *socket.Wallet)
				socket.SendAMessage(Blockchain.Message{
					Flag:        Blockchain.TransactionMess,
					Data:        *transac,
					ToBroadcast: Blockchain.AskToBroadcast,
				})
				time.Sleep(time.Duration(tt.milliwait) * time.Millisecond)
			}

			experiment.WaitUntilEmpty()
			fmt.Println("End of the Sleep")

			fmt.Println(experiment.GetExchanges())
			fmt.Println(experiment.GetExchangesDetailed())
			defer experiment.Close()
			blockchainRef := experiment.ListNode[0].GetBlockchain()
			for i, consensus := range experiment.ListNode {
				blockchain_ref := consensus.GetBlockchain()
				blockchain := blockchain_ref.GetBlocs()
				var nbTrans int
				for _, block := range *blockchain {
					nbTrans = nbTrans + len(block.Transactions)
				}
				if nbTrans != tt.expectedBloc {
					t.Errorf("Wrong number of transactions on %d get %d, expected %d", i, nbTrans, tt.expectedBloc)
				}
				if !consensus.GetTransactionPool().IsEmpty() {
					t.Errorf("The txPool of the node %d is not empty", i)
				}

				for j := 0; j < consensus.GetSeqNb(); j++ {
					if !bytes.Equal(blockchainRef.GetBlock(j).Hash, blockchain_ref.GetBlock(j).Hash) {
						t.Errorf("Error on node %d, the hash of the block %d is different of the reference", i, j)
					}
				}
				fmt.Println("Nb of bloc", consensus.GetSeqNb())
			}
		})
	}

}

func TestCommandVarie(t *testing.T) {
	return
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	tests := []struct {
		name              string
		nbOfNode          int
		milliwait         int64
		listTransac       []int
		expectedFinalSize int
	}{
		{"Just 1 Brut transact", 15, 200, []int{0}, 15},
		{"Just 1 Brut transact", 15, 200, []int{0, 0}, 15},
		{"Just 1 Brut transact", 8, 200, []int{-2}, 6},
		{"Just 1 Brut transact", 5, 200, []int{-1}, 4},
		{"Just 1 Brut transact", 5, 200, []int{-1, 0}, 4},
		{"Just 1 Brut transact", 15, 200, []int{-2}, 13},
		{"Just 1 Brut transact", 15, 200, []int{-5, 0}, 10},
		{"Just 1 Brut transact", 15, 200, []int{-5, 0, 3}, 13},
		{"Just 1 Brut transact", 15, 200, []int{-5, 0, 3}, 13},
		{"Just 1 Brut transact", 40, 200, []int{-20, 0, -15, 0, 0}, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			experiment := testStruct.NewTestExperiementChannel(tt.nbOfNode, false, false)
			defer experiment.Close()

			executeListTransact(experiment, tt.listTransac, tt.milliwait, false)

			experiment.WaitUntilEmpty()
			expectation := expectExchange(tt.listTransac, *experiment.ListNode[0].GetBlockchain(), tt.nbOfNode)
			emitMessage, _ := experiment.GetExchangesDetailed()
			fmt.Println(experiment.GetExchanges())
			//			fmt.Println("expect: \t",expectation)
			//			fmt.Println("got : \t\t",emitMessage)
			for i, consensus := range experiment.ListNode {
				blockchain := consensus.GetBlockchain()
				if blockchain.GetLastBLoc().SequenceNb != len(tt.listTransac) {
					t.Errorf("Wrong Number of Bloc on bloc %d get %d, expected %d", i, blockchain.GetLastBLoc().SequenceNb, len(tt.listTransac))
				}
				if consensus.GetValidator().GetNumberOfValidator() != tt.expectedFinalSize {
					t.Errorf("Wrong Current Node Number on node %d get %d, expected %d", i, consensus.GetValidator().GetNumberOfValidator(), tt.expectedFinalSize)
				}
				// nbValidator := tt.nbOfNode
				for j, order := range tt.listTransac {
					block := blockchain.GetBlock(j + 1)
					transac := block.Transactions[0]
					switch input := transac.TransaCore.Input.(type) {
					case Blockchain.BrutData:
						if order != 0 {
							t.Error("Doesn't expect a BrutData")
						}
					case Blockchain.Commande:
						if order == 0 {
							t.Error("Doesn't expect a Command")
						} else {
							if input.Variation != order {
								t.Errorf("The variation is different, expected %d, get %d", order, input.Variation)
							}
						}
					}
				}

				// Check the number of emitted messages, doesn't check the roundChanges
				for j := 0; j < Blockchain.NbTypeMess; j++ {
					if emitMessage[i][j] != expectation[i][j] {
						t.Errorf("Error on node %d, on the number of emission of %d, expected %d, got %d", i, j, expectation[i][j], emitMessage[i][j])
					}
				}

			}
		})
	}
}

func TestVarieValidPoA(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	tests := []struct {
		name              string
		nbOfNode          int
		milliwait         int64
		listTransac       []int
		expectedFinalSize int
	}{
		{"Just 1 Brut transact", 15, 200, []int{0}, 15},
		{"Just 1 Brut transact", 15, 200, []int{0, 0}, 15},
		{"Just 1 Brut transact", 8, 200, []int{-2}, 6},
		{"Just 1 Brut transact", 5, 200, []int{-1}, 4},
		{"Just 1 Brut transact", 5, 200, []int{-1, 0}, 4},
		{"Just 1 Brut transact", 15, 200, []int{-2}, 13},
		{"Just 1 Brut transact", 15, 200, []int{-5, 0}, 10},
		{"Just 1 Brut transact", 15, 200, []int{-10, 0, 0, 0, 0, 0, 0}, 5},
		{"Just 1 Brut transact", 15, 200, []int{-5, 0, 3}, 13},
		{"Just 1 Brut transact", 15, 200, []int{-5, 0, 3}, 13},
		{"Just 1 Brut transact", 40, 200, []int{-20, 0, -15, 0, 0}, 5},
		{"Just 1 Brut transact", 50, 200, []int{-46, 0, 20, 0, 0, -20, 0, 0, 20, 0, 0}, 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			experiment := testStruct.NewTestExperiementChannel(tt.nbOfNode, false, true)
			defer experiment.Close()

			executeListTransact(experiment, tt.listTransac, tt.milliwait, false)

			experiment.WaitUntilEmpty()
			expectation := expectExchangePoA(tt.listTransac, *experiment.ListNode[0].GetBlockchain(), tt.nbOfNode)
			emitMessage, _ := experiment.GetExchangesDetailed()
			fmt.Println(experiment.GetExchanges())
			fmt.Println("expect: \t", expectation)
			fmt.Println("got : \t\t", emitMessage)
			for i, consensus := range experiment.ListNode {
				blockchain := consensus.GetBlockchain()
				if blockchain.GetLastBLoc().SequenceNb != len(tt.listTransac) {
					t.Errorf("Wrong Number of Bloc on bloc %d get %d, expected %d", i, blockchain.GetLastBLoc().SequenceNb, len(tt.listTransac))
				}
				if consensus.GetValidator().GetNumberOfValidator() != tt.expectedFinalSize {
					t.Errorf("Wrong Current Node Number on node %d get %d, expected %d", i, consensus.GetValidator().GetNumberOfValidator(), tt.expectedFinalSize)
				}
				// nbValidator := tt.nbOfNode
				for j, order := range tt.listTransac {
					block := blockchain.GetBlock(j + 1)
					transac := block.Transactions[0]
					switch input := transac.TransaCore.Input.(type) {
					case Blockchain.BrutData:
						if order != 0 {
							t.Error("Doesn't expect a BrutData")
						}
					case Blockchain.Commande:
						if order == 0 {
							t.Error("Doesn't expect a Command")
						} else {
							if input.Variation != order {
								t.Errorf("The variation is different, expected %d, get %d", order, input.Variation)
							}
						}
					}
				}

				// Check the number of emitted messages, doesn't check the roundChanges
				for j := 0; j < Blockchain.NbTypeMess; j++ {
					if emitMessage[i][j] != expectation[i][j] {
						t.Errorf("Error on node %d, on the number of emission of %d, expected %d, got %d", i, j, expectation[i][j], emitMessage[i][j])
					}
				}

			}
		})
	}
}

func executeListTransact(experiment *testStruct.TestExperiementChannel, listTransac []int, wait int64, broadcastTransac bool) {
	N := len(experiment.ListNode)

	for _, orderCode := range listTransac {
		blockchain := experiment.ListNode[0].GetBlockchain()
		socketID := blockchain.GetProposerId()
		if broadcastTransac {
			socketID = (socketID + 1) % experiment.NbOfNode
		}
		socket := experiment.ListSocket[socketID]
		fmt.Println("expected validator : ", socketID)
		var transac *Blockchain.Transaction

		N = N + orderCode
		newList := experiment.ListNode[0].GenerateNewValidatorListProposition(N)
		transac = Blockchain.NewTransaction(Blockchain.Commande{
			Order:           Blockchain.VarieValid,
			Variation:       N,
			NewValidatorSet: newList,
		}, *socket.Wallet)

		socket.SendAMessage(Blockchain.Message{
			Flag: Blockchain.TransactionMess,
			Data: *transac,
		})
		time.Sleep(time.Duration(wait) * time.Millisecond)

	}
}

// Return the expection of exchange according a list of Transaction
func expectExchange(listTransac []int, blockchain Blockchain.Blockchain, numberOfNode int) [][6]int {
	expection := make([][Blockchain.NbTypeMess]int, numberOfNode)
	nbValidator := numberOfNode
	for _, order := range listTransac {
		// Should be the validator inside
		proposer := blockchain.GetProposerId()
		for j := 0; j < nbValidator; j++ {
			if j == proposer {
				expection[j][Blockchain.PrePrepare-1] = expection[j][Blockchain.PrePrepare-1] + numberOfNode - 1
			}
			expection[j][Blockchain.PrepareMess-1] = expection[j][Blockchain.PrepareMess-1] + numberOfNode - 1
			expection[j][Blockchain.CommitMess-1] = expection[j][Blockchain.CommitMess-1] + numberOfNode - 1
		}
		nbValidator = nbValidator + order
	}
	return expection
}

// Return the expection of exchange according a list of Transaction
func expectExchangePoA(listTransac []int, blockchain Blockchain.Blockchain, numberOfNode int) [][6]int {
	expection := make([][Blockchain.NbTypeMess]int, numberOfNode)
	nbValidator := numberOfNode
	for _, order := range listTransac {
		proposer := blockchain.GetProposerId()
		for j := 0; j < nbValidator; j++ {
			if j == proposer {
				expection[j][Blockchain.PrePrepare-1] = expection[j][Blockchain.PrePrepare-1] + nbValidator - 1
				expection[j][Blockchain.BlocMsg-1] = expection[j][Blockchain.BlocMsg-1] + numberOfNode - nbValidator
			}
			expection[j][Blockchain.PrepareMess-1] = expection[j][Blockchain.PrepareMess-1] + nbValidator - 1
			expection[j][Blockchain.CommitMess-1] = expection[j][Blockchain.CommitMess-1] + nbValidator - 1
		}
		nbValidator = nbValidator + order
	}
	return expection
}
