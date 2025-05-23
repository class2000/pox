package Consensus

import (
	"fmt"
	"github.com/rs/zerolog"
	"pbftnode/source/Blockchain"
	"testing"
	"time"
)

type compteur struct {
	cmpt   [Blockchain.NbTypeMess]int
	idNode int
}

type testSocket compteur

func (socket *testSocket) UpdateDelay(parameter int) {}

func (t *testSocket) TransmitTransaction(message Blockchain.Message) {
	t.BroadcastMessage(message)
}

type initState [Blockchain.NbTypeMess]int // [nb of transaction, nb of PrepareMessage, nb of Commit, number of RC message]
type expected [Blockchain.NbTypeMess]int  // Number of broadcast of previous message
const testNumberOfNode = 100

func (t *testSocket) BroadcastMessage(message Blockchain.Message) {
	t.cmpt[message.Flag-1]++
}

func (socket testSocket) eraseCmpt() {
	for i := range socket.cmpt {
		socket.cmpt[i] = 0
	}
}

func (t *testSocket) BroadcastMessageNV(message Blockchain.Message) {
	panic("implement me")
}

func (t testSocket) compareWithExpected(exp expected) bool {
	for i, value := range t.cmpt {
		if value != exp[i] {
			return false
		}
	}
	return true
}

func TestPBFTConsensus_receiveTransacMess(t *testing.T) {
	//N := NumberOfNodes
	tests := []struct {
		name           string
		initState      initState
		proposer       bool
		knownValidator bool //define if the sender is known
		nbMessReceive  int
		Broadcast      expected
	}{
		{"send 1 Transaction", [Blockchain.NbTypeMess]int{}, false, true, 1, [Blockchain.NbTypeMess]int{1}},
		{"send 5 same Transaction", [Blockchain.NbTypeMess]int{}, false, true, 5, [Blockchain.NbTypeMess]int{1}},
		{"send 1 Transaction, proposer", [Blockchain.NbTypeMess]int{}, true, true, 1, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"send 5 same Transaction, proposer", [Blockchain.NbTypeMess]int{}, true, true, 5, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"receive existing transaction, the existing transaction should be mined", [Blockchain.NbTypeMess]int{1}, true, true, 5, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"receive existing transaction", [Blockchain.NbTypeMess]int{1}, false, true, 5, [Blockchain.NbTypeMess]int{}},
		{"Unknown sender", [Blockchain.NbTypeMess]int{}, false, false, 1, [Blockchain.NbTypeMess]int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Init phase
			consensus, testSocket := createTestSocket(tt.proposer)
			transaction, _ := implementInitState(consensus, tt.initState, nil)
			time.Sleep(100 * time.Millisecond)
			testSocket.eraseCmpt()
			fmt.Println("Start experiment")
			if !tt.knownValidator {
				proposerWallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", 2*consensus.GetValidator().GetNumberOfValidator()))
				transaction = Blockchain.NewBruteTransaction([]byte("transaction"), *proposerWallet)
			}
			message := Blockchain.Message{
				Flag: Blockchain.TransactionMess,
				Data: *transaction,
			}
			// Tested phase
			for i := 0; i < tt.nbMessReceive; i++ {
				consensus.MessageHandler(message)
			}
			time.Sleep(100 * time.Millisecond)
			// Assert phase
			if !testSocket.compareWithExpected(tt.Broadcast) {
				t.Errorf("Error with the test %s, got %v, expected %v", tt.name, testSocket.cmpt, tt.Broadcast)
			}
		})
	}
}

func createTestSocket(proposer bool) (testConsensus, *testSocket) {
	testSocket := testSocket{}
	wallet := Blockchain.NewWallet("tempo")
	consensus := NewPBFTStateConsensus(wallet, testNumberOfNode, Blockchain.ConsensusParam{
		Broadcast:        true,
		RefreshingPeriod: 1,
		ControlPeriod:    10,
	})
	consensus.Broadcast = true
	testSocket.idNode = getIdValidator(consensus.getBlockchain(), consensus.Validators, proposer)
	wallet = Blockchain.NewWallet(fmt.Sprintf("NODE%d", testSocket.idNode))
	consensus.setWallet(*wallet)
	consensus.SocketHandler = &testSocket
	return consensus, &testSocket
}

func getIdValidator(chain *Blockchain.Blockchain, validator Blockchain.ValidatorInterf, isValidator bool) int {
	proposer := chain.GetProposerId()
	if isValidator {
		return proposer
	}
	return (proposer + 1) % validator.GetNumberOfValidator()
}

func TestTestSocket(t *testing.T) {
	tests := []struct {
		name       string
		askMessage initState
		Broadcast  expected
	}{
		{"send 1 Transaction", [Blockchain.NbTypeMess]int{1, 2, 7, 8, 4}, [Blockchain.NbTypeMess]int{1, 2, 7, 8, 4}},
		{"send 2 same Transaction", [Blockchain.NbTypeMess]int{5, 7, 8, 6, 2}, [Blockchain.NbTypeMess]int{5, 7, 8, 6, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, testSocket := createTestSocket(false)
			for index, value := range tt.askMessage {
				for i := 0; i < value; i++ {
					testSocket.BroadcastMessage(Blockchain.Message{
						Flag: Blockchain.MessageType(index + 1),
						Data: nil,
					})
				}
			}
			if !testSocket.compareWithExpected(tt.Broadcast) {
				t.Errorf("The testSocket doesn't work, got %v, expected %v", testSocket.cmpt, tt.Broadcast)
			}
		})
	}
}

func implementInitState(consensus testConsensus, state initState, data *Blockchain.Input) (*Blockchain.Transaction, *Blockchain.Block) {
	proposerId := consensus.getBlockchain().GetProposerId()
	proposerWallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", proposerId))
	nodeId := consensus.GetId()
	var transaction *Blockchain.Transaction
	if data == nil {
		transaction = Blockchain.NewBruteTransaction([]byte("transaction"), *proposerWallet)
	} else {
		transaction = Blockchain.NewTransaction(*data, *proposerWallet)
	}
	var block = consensus.getBlockchain().CreateBlock([]Blockchain.Transaction{*transaction}, *proposerWallet)
	if state[Blockchain.TransactionMess-1] > 0 {
		consensus.getTransactionPool().AddTransaction(*transaction)
	}
	if state[Blockchain.PrePrepare-1] > 0 {
		consensus.GetBlockPool().AddBlock(*block)
		//consensus.PreparePool.Prepare(block, &consensus.Wallet)
	}
	if state[Blockchain.PrepareMess-1] > 0 {
		for i := 0; i < state[Blockchain.PrepareMess-1]; i++ {
			wallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", i))
			if i == nodeId {
				wallet = Blockchain.NewWallet(fmt.Sprintf("NODE%d", consensus.GetValidator().GetNumberOfValidator()-1))
			}
			prepare := Blockchain.CreatePrepare(block, wallet)
			consensus.GetPreparePool().AddPrepare(prepare)
		}
		if state[Blockchain.PrepareMess-1] >= consensus.MinApprovals() {
			prepare := Blockchain.CreatePrepare(block, consensus.GetWallet())
			consensus.GetCommitPool().Commit(&prepare, consensus.GetWallet())
		}
	}
	if state[Blockchain.CommitMess-1] > 0 {
		for i := 0; i < state[Blockchain.CommitMess-1]; i++ {
			wallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", i))
			if i == nodeId {
				wallet = Blockchain.NewWallet(fmt.Sprintf("NODE%d", consensus.GetValidator().GetNumberOfValidator()-1))
			}
			prepare := Blockchain.Prepare{
				BlockHash: block.Hash,
			}
			commit := Blockchain.CreateCommit(prepare.BlockHash, wallet)
			consensus.GetCommitPool().AddCommit(commit)
		}
		if state[Blockchain.CommitMess-1] >= consensus.MinApprovals() {
			consensus.getBlockchain().AddUpdatedBlockCommited(block.Hash, consensus.GetBlockPool(), consensus.getTransactionPool(), *consensus.GetPreparePool(), *consensus.GetCommitPool())
			consensus.GetMessagePool().CreateMessage(consensus.getBlockchain().GetBlock(consensus.getBlockchain().GetLenght()-1).Hash, consensus.GetWallet())
		}
	}
	return transaction, block
}

func TestPBFTConsensus_receivePrePrepareMessage(t *testing.T) {
	//N := NumberOfNodes
	tests := []struct {
		name           string
		initState      initState
		isValid        bool //define if the block is created by the proposer
		nodeValidator  bool //define if the node is the validator
		knownValidator bool //define if the sender is known
		nbMessReceive  int
		Broadcast      expected
	}{
		{"receive PrePrePare, know transac: basic case", [Blockchain.NbTypeMess]int{1}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"receive PrePrePare, don't know transac", [Blockchain.NbTypeMess]int{}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"receive PrePrePare, know transac", [Blockchain.NbTypeMess]int{1}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"receive Invalid Bloc", [Blockchain.NbTypeMess]int{1}, false, false, true, 1, [Blockchain.NbTypeMess]int{}},
		{"block already exist", [Blockchain.NbTypeMess]int{0, 1}, true, false, true, 1, [Blockchain.NbTypeMess]int{}},
		{"receive multiple PrePrePare", [Blockchain.NbTypeMess]int{}, true, false, true, 5, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"node is the proposer", [Blockchain.NbTypeMess]int{}, true, true, true, 1, [Blockchain.NbTypeMess]int{0, 1, 1}},
		{"prepare from an unknown validator", [Blockchain.NbTypeMess]int{1}, true, false, false, 1, [Blockchain.NbTypeMess]int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Init phase
			consensus, testSocket := createTestSocket(tt.nodeValidator)
			transaction, block := implementInitState(consensus, tt.initState, nil)
			if !tt.isValid {
				invalidWallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", getIdValidator(consensus.getBlockchain(), consensus.GetValidator(), false)))
				block = consensus.getBlockchain().CreateBlock([]Blockchain.Transaction{*transaction}, *invalidWallet)
			}
			if !tt.knownValidator {
				invalidWallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", getIdValidator(consensus.getBlockchain(), consensus.GetValidator(), false)+2*consensus.GetValidator().GetNumberOfValidator()))
				block = consensus.getBlockchain().CreateBlock([]Blockchain.Transaction{*transaction}, *invalidWallet)
			}
			message := Blockchain.Message{
				Flag: Blockchain.PrePrepare,
				Data: *block,
			}
			// Tested phase
			for i := 0; i < tt.nbMessReceive; i++ {

				consensus.MessageHandler(message)
			}
			// Assert phase
			if !testSocket.compareWithExpected(tt.Broadcast) {
				t.Errorf("Error with the test %s, got %v, expected %v", tt.name, testSocket.cmpt, tt.Broadcast)
			}
		})
	}
}

func TestPBFTConsensus_receivePrepareMessage(t *testing.T) {
	N := testNumberOfNode        //100
	threshold := (N * 2 / 3) + 1 //67
	tests := []struct {
		name           string
		initState      initState
		isBlocValid    bool //define if the block is created by the proposer
		fromKnown      bool //define if received Prepare Message has already sent message
		knownValidator bool //define if the sender is known
		nbMessReceive  int  //number of message from other node, different of the receiver
		Broadcast      expected
	}{
		{"receive PrePare, receive first prepare: basic case", [Blockchain.NbTypeMess]int{0, 1, 0}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 1}},
		{"receive PrePare, receive enough prepare to commit : basic case", [Blockchain.NbTypeMess]int{0, 1, threshold - 1}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 1, 1}},
		{"check if it doesn't send at threshold", [Blockchain.NbTypeMess]int{0, 1, threshold - 2}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 1}},
		{"receive PrePare, know transaction: basic case", [Blockchain.NbTypeMess]int{0, 1, 0}, true, false, true, threshold, [Blockchain.NbTypeMess]int{0, 0, threshold, 1}},
		{"Check if send just one commit", [Blockchain.NbTypeMess]int{0, 1, threshold - 1}, true, false, true, 2, [Blockchain.NbTypeMess]int{0, 0, 2, 1}},
		{"receive PrePare, know nothing", [Blockchain.NbTypeMess]int{}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 1}},
		{"receive already known prepare", [Blockchain.NbTypeMess]int{0, 1, threshold * 2 / 3}, true, true, true, threshold * 2 / 3, [Blockchain.NbTypeMess]int{}},
		{"receive PrePare of a an other bloc", [Blockchain.NbTypeMess]int{0, 1, 0}, false, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 1}},
		{"receive from unknown validator", [Blockchain.NbTypeMess]int{0, 1, 0}, true, false, false, 1, [Blockchain.NbTypeMess]int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Assert Phase
			if tt.nbMessReceive == N {
				t.Error("Not Enough node")
			}
			// Init phase
			consensus, testSocket := createTestSocket(tt.fromKnown)
			_, block := implementInitState(consensus, tt.initState, nil)
			if !tt.isBlocValid {
				invalidWallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", getIdValidator(consensus.getBlockchain(), consensus.GetValidator(), false)))
				block = consensus.getBlockchain().CreateBlock([]Blockchain.Transaction{*Blockchain.NewBruteTransaction([]byte("invalidTransaction"), *invalidWallet)}, *invalidWallet)
			}
			// Tested phase
			if tt.initState[Blockchain.PrepareMess-1] > N {
				t.Error("too much initial prepare message for the number of node")
			}
			shift := tt.initState[Blockchain.PrepareMess-1]
			if tt.fromKnown {
				// try if BrutData are valid
				if tt.initState[Blockchain.PrepareMess-1] < tt.nbMessReceive {
					t.Error("Too much message, not enough known prepare message")
				}
				shift = 0
			}
			if !tt.knownValidator {
				shift = 2 * consensus.GetValidator().GetNumberOfValidator()
			}
			for i := 0; i < tt.nbMessReceive; i++ {
				wallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", i+shift))
				if i+shift == testSocket.idNode {
					wallet = Blockchain.NewWallet(fmt.Sprintf("NODE%d", consensus.GetValidator().GetNumberOfValidator()-1))
					//if tt.initState[PrepareMess-1]<= i {
					//	wallet = NewWallet(fmt.Sprintf("NODE%d", consensus.NumberOfNodes-2))
					//}else {
					//	wallet = NewWallet(fmt.Sprintf("NODE%d", consensus.NumberOfNodes-1))
					//}
				}
				prepare := Blockchain.CreatePrepare(block, wallet)
				message := Blockchain.Message{
					Flag: Blockchain.PrepareMess,
					Data: prepare,
				}
				consensus.MessageHandler(message)
			}
			// Assert phase
			if !testSocket.compareWithExpected(tt.Broadcast) {
				t.Errorf("Error with the test %s, got %v, expected %v", tt.name, testSocket.cmpt, tt.Broadcast)
			}
		})
	}
}

func TestPBFTConsensus_receiveCommitMessage(t *testing.T) {
	N := testNumberOfNode        //100
	threshold := (N * 2 / 3) + 1 //67
	tests := []struct {
		name           string
		initState      initState
		isBlocValid    bool //define if the block is created by the proposer
		fromKnown      bool //define if received Prepare Message has already sent message
		knownValidator bool //define if the sender is known
		nbMessReceive  int  //number of message from other node, different of the receiver
		Broadcast      expected
	}{
		{"receive PrePare, receive first prepare: basic case", [Blockchain.NbTypeMess]int{0, 1, threshold, 0}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 0, 1, 0}},
		{"already committed", [Blockchain.NbTypeMess]int{0, 1, threshold, threshold}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 0, 1, 0}},
		{"receive just enough", [Blockchain.NbTypeMess]int{0, 1, threshold, 0}, true, false, true, threshold - 1, [Blockchain.NbTypeMess]int{0, 0, 0, threshold - 1, 0}},
		{"receive not enough Commit", [Blockchain.NbTypeMess]int{0, 1, threshold, 0}, true, false, true, threshold - 2, [Blockchain.NbTypeMess]int{0, 0, 0, threshold - 2}},
		{"already committed", [Blockchain.NbTypeMess]int{0, 1, threshold, N * 3 / 4}, true, false, true, 2, [Blockchain.NbTypeMess]int{0, 0, 0, 2, 0}},
		{"receive large enough Commit", [Blockchain.NbTypeMess]int{0, 1, threshold, 0}, true, false, true, N * 3 / 4, [Blockchain.NbTypeMess]int{0, 0, 0, N * 3 / 4, 0}},
		{"know nothing", [Blockchain.NbTypeMess]int{}, true, false, true, 1, [Blockchain.NbTypeMess]int{0, 0, 0, 1}},
		{"know nothing, try to force Bloc", [Blockchain.NbTypeMess]int{}, true, false, true, N * 3 / 4, [Blockchain.NbTypeMess]int{0, 0, 0, N * 3 / 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Assert Phase
			if tt.nbMessReceive == N {
				t.Error("Not Enough node")
			}
			// Init phase
			consensus, testSocket := createTestSocket(tt.fromKnown)
			_, block := implementInitState(consensus, tt.initState, nil)
			if !tt.isBlocValid {
				invalidWallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", getIdValidator(consensus.getBlockchain(), consensus.GetValidator(), false)))
				block = consensus.getBlockchain().CreateBlock([]Blockchain.Transaction{*Blockchain.NewBruteTransaction([]byte("invalidTransaction"), *invalidWallet)}, *invalidWallet)
			}
			// Tested phase
			if tt.initState[Blockchain.CommitMess-1] > N {
				t.Error("too much initial prepare message for the number of node")
			}
			shift := tt.initState[Blockchain.CommitMess-1]
			if tt.fromKnown {
				// try if BrutData are valid
				if tt.initState[Blockchain.CommitMess-1] < tt.nbMessReceive {
					t.Error("Too much message, not enough known prepare message")
				}
				shift = 0
			}
			if !tt.knownValidator {
				shift = 2 * consensus.GetValidator().GetNumberOfValidator()
			}
			for i := 0; i < tt.nbMessReceive; i++ {
				wallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", i+shift))
				if i+shift == testSocket.idNode {
					wallet = Blockchain.NewWallet(fmt.Sprintf("NODE%d", consensus.GetValidator().GetNumberOfValidator()-1))
				}
				prepare := Blockchain.Prepare{
					BlockHash: block.Hash,
				}
				commit := Blockchain.CreateCommit(prepare.BlockHash, wallet)
				message := Blockchain.Message{
					Flag: Blockchain.CommitMess,
					Data: commit,
				}
				consensus.MessageHandler(message)
			}
			// Assert phase
			if !testSocket.compareWithExpected(tt.Broadcast) {
				t.Errorf("Error with the test %s, got %v, expected %v", tt.name, testSocket.cmpt, tt.Broadcast)
			}
		})
	}
}

func TestDisorder(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	N := testNumberOfNode        //100
	threshold := (N * 2 / 3) + 1 //67
	tests := []struct {
		name         string
		initState    initState
		nbPrePrepare int
		nbPrepare    int
		Broadcast    expected
	}{
		{"Normale", [Blockchain.NbTypeMess]int{0, 1, threshold - 1, 0}, 0, 1, [Blockchain.NbTypeMess]int{0, 0, 1, 1, 0}},
		{"PrePrepare is missing", [Blockchain.NbTypeMess]int{0, 0, threshold - 1, 0}, 0, 1, [Blockchain.NbTypeMess]int{0, 0, 1, 0, 0}},
		//{"PrePrepare is missing", [Blockchain.NbTypeMess]int{0, 0, threshold, 0}, 1,0,[Blockchain.NbTypeMess]int{0, 1, 1, 1, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Init phase
			consensus, testSocket := createTestSocket(true)
			_, block := implementInitState(consensus, tt.initState, nil)

			sendPrePrepare(tt.nbPrePrepare, consensus, block)
			sendPrepare(tt.initState, tt.nbPrepare, testSocket, consensus, block)
			if !testSocket.compareWithExpected(tt.Broadcast) {
				t.Errorf("Error with the test %s, got %v, expected %v", tt.name, testSocket.cmpt, tt.Broadcast)
			}
		})
	}
}

func sendPrepare(minitState initState, nbmss int, testSocket *testSocket, consensus testConsensus, block *Blockchain.Block) {
	shift := minitState[Blockchain.PrepareMess-1]
	for i := 0; i < nbmss; i++ {
		wallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", i+shift))
		if i+shift == testSocket.idNode {
			wallet = Blockchain.NewWallet(fmt.Sprintf("NODE%d", consensus.GetValidator().GetNumberOfValidator()-1))
		}
		prepare := Blockchain.CreatePrepare(block, wallet)
		message := Blockchain.Message{
			Flag: Blockchain.PrepareMess,
			Data: prepare,
		}
		consensus.MessageHandler(message)
	}
}

func sendPrePrepare(nbmss int, consensus testConsensus, block *Blockchain.Block) {
	for i := 0; i < nbmss; i++ {
		preprepare := Blockchain.Message{
			Flag: Blockchain.PrePrepare,
			Data: *block,
		}
		consensus.MessageHandler(preprepare)
	}
}
