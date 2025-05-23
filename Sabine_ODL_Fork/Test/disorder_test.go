package Test

import (
	"fmt"
	"github.com/rs/zerolog"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/testStruct"
	"pbftnode/source/config"
	"testing"
	"time"
)

func TestDisorder(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	tests := []struct {
		name         string
		nbMsgDeleted int
	}{
		{"No deleted", 0},
		{"1 deleted", 1},
		{"2 deleted", 2},
		{"10 deleted", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			experiment := testStruct.NewTestExperiementChannel(5, false, true)
			blockNV := experiment.ListNode[4]
			executeListTransact(experiment, []int{-1}, 0, false)
			experiment.WaitUntilEmpty()

			NVsocket := experiment.GetSocketAndStopMsg(4)

			executeListTransact(experiment, []int{0}, 0, false)
			experiment.WaitUntilEmpty()
			blockchain := experiment.ListNode[0].GetBlockchain()
			block1 := blockchain.GetLastBLoc()

			for i := 0; i < tt.nbMsgDeleted; i++ {
				executeListTransact(experiment, []int{0}, 0, false)
				experiment.WaitUntilEmpty()
				blockchain := experiment.ListNode[0].GetBlockchain()
				block2 := blockchain.GetLastBLoc()
				NVsocket.SendAMessage(Blockchain.Message{
					Flag: Blockchain.BlocMsg,
					Data: Blockchain.BlockMsg{Block: block2},
				})
			}
			time.Sleep(100 * time.Millisecond)
			if blockNV.GetSeqNb() != 2 {
				t.Errorf("Expected %d block, got %d", 2, blockNV.GetSeqNb())
			}
			NVsocket.SendAMessage(Blockchain.Message{
				Flag: Blockchain.BlocMsg,
				Data: Blockchain.BlockMsg{Block: block1},
			})
			time.Sleep(100 * time.Millisecond)
			if blockNV.GetSeqNb() != 3+tt.nbMsgDeleted {
				t.Errorf("Expected %d block, got %d", 2+tt.nbMsgDeleted, blockNV.GetSeqNb())
			}
			experiment.Close()
		})
	}
}

func TestMoreThanBlock(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	tests := []struct {
		name   string
		nbMess int
	}{
		{"1 transaction", 1},
		{"less a block", config.BlockSize - 1},
		{"a block", config.BlockSize},
		{"more than a block", config.BlockSize + 1},
		{"Twice a block", 2 * config.BlockSize},
		{"Three times a block", 3 * config.BlockSize},
		{"Ten times a block", 10 * config.BlockSize},
		{"Max times a block", 2 ^ 21 - 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			experiment := testStruct.NewTestExperiementChannel(20, false, true)
			defer experiment.Close()
			var listTransac []Blockchain.Message
			wallet := experiment.ListSocket[0].Wallet
			for i := 0; i < tt.nbMess; i++ {
				transac := Blockchain.NewBruteTransaction([]byte(fmt.Sprintf("Transac%d", i)), *wallet)
				listTransac = append(listTransac, Blockchain.Message{
					Flag:        Blockchain.TransactionMess,
					Data:        *transac,
					ToBroadcast: Blockchain.AskToBroadcast,
				})
			}
			receiver := experiment.ListSocket[0]
			for _, mess := range listTransac {
				receiver.SendAMessage(mess)
			}
			experiment.WaitUntilEmpty()
			cmpt := 0
			blockchain := experiment.ListNode[0].GetBlockchain()
			for i, block := range *blockchain.GetBlocs() {
				cmpt = cmpt + len(block.Transactions)
				if len(block.Transactions) > config.BlockSize {
					t.Errorf("Too Much Tx on block %d, got %d, max %d", i, len(block.Transactions), config.BlockSize)
				}
			}
			if cmpt != tt.nbMess {
				t.Errorf("All transaction are not mined, got %d, expected %d", cmpt, tt.nbMess)
			}

			fmt.Println(experiment.ListNode[0].GetSeqNb())
			fmt.Printf("Size of the block pool : %d\n", experiment.ListNode[0].GetBlockPool().PoolSize())
		})
	}
}
