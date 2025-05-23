package testStruct

import (
	"fmt"
	"math"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Consensus"
)

type testMessageId struct {
	message Blockchain.Message
	iD      int
	nbVal   int
}

type TestSocketChannel struct {
	id              int
	channel         *testChannel
	receivedMessage chan Blockchain.Message
	messageHandler  func(Blockchain.Message)
	Wallet          *Blockchain.Wallet
	toClose         chan bool
	validator       Blockchain.ValidatorGetterInterf
}

func (socket *TestSocketChannel) UpdateDelay(parameter int) {
	socket.channel.UpdateDelay(parameter)
}

func (socket *TestSocketChannel) TransmitTransaction(message Blockchain.Message) {
	socket.BroadcastMessage(message)
}

func (socket *TestSocketChannel) BroadcastMessageNV(message Blockchain.Message) {
	msg := testMessageId{
		message: message,
		iD:      socket.id,
		nbVal:   socket.validator.GetNumberOfValidator(),
	}
	socket.channel.incomingMessagePoA <- msg
}

func (socket *TestSocketChannel) Close() {
	close(socket.receivedMessage)
	<-socket.toClose
	close(socket.toClose)
}

func (socket *TestSocketChannel) manageMessage() {
	for message := range socket.receivedMessage {
		socket.messageHandler(message)
	}
	socket.toClose <- true
}

func (socket *TestSocketChannel) BroadcastMessage(message Blockchain.Message) {
	messID := testMessageId{
		message: message,
		iD:      socket.id,
		nbVal:   socket.validator.GetNumberOfValidator(),
	}
	socket.channel.incomingMessage <- messID
}

func NewTestSocketChannel(id int, channel *testChannel, handler func(Blockchain.Message), wallet *Blockchain.Wallet, validator Blockchain.ValidatorGetterInterf) *TestSocketChannel {
	socket := &TestSocketChannel{
		id:              id,
		channel:         channel,
		receivedMessage: make(chan Blockchain.Message, int(math.Pow(float64(channel.numberOfNode), mathPowBuff))),
		messageHandler:  handler,
		Wallet:          wallet,
		toClose:         make(chan bool, 1),
		validator:       validator,
	}
	channel.addSocket(socket)
	go socket.manageMessage()
	return socket
}

func (socket *TestSocketChannel) SendAMessage(message Blockchain.Message) {
	socket.receivedMessage <- message
}

func createTestConsensus(id int, nbOfNode int, channel *testChannel, broadcast bool, PoA bool) (Blockchain.TestConsensus, *TestSocketChannel) {
	wallet := Blockchain.NewWallet(fmt.Sprintf("NODE%d", id))
	//consensus := Consensus.NewPBFTConsensus(wallet, nbOfNode)
	consensus := Consensus.NewPBFTStateConsensus(wallet, nbOfNode, Blockchain.ConsensusParam{
		Broadcast:        broadcast,
		PoANV:            PoA,
		RamOpt:           true,
		ControlPeriod:    10,
		RefreshingPeriod: 1,
	})
	socket := NewTestSocketChannel(id, channel, consensus.MessageHandler, wallet, consensus.Validators)
	consensus.SocketHandler = socket
	return consensus, socket
}
