package testStruct

import (
	"math"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Socket"
	"time"
)

const mathPowBuff = 2

type testChannel struct {
	Blockchain.CloseHandler
	numberOfNode           int
	emitMessageDetailed    [][Blockchain.NbTypeMess]int
	receiveMessageDetailed [][Blockchain.NbTypeMess]int
	incomingMessage        chan testMessageId
	listChan               map[int]*chan Blockchain.Message
	PoANV                  bool
	incomingMessagePoA     chan testMessageId
	isEmpty                chan chan bool
	moved                  bool
	avgLatency             *Socket.NodeDelay
	listLatency            []*Socket.SocketDelay
	toNotSend              []bool
	toIgnore               chan queryIgnore
}

func newTestChannel(numberOfNode int, avgLatency *Socket.NodeDelay) *testChannel {
	channel := &testChannel{
		numberOfNode:           numberOfNode,
		emitMessageDetailed:    make([][Blockchain.NbTypeMess]int, numberOfNode),
		receiveMessageDetailed: make([][Blockchain.NbTypeMess]int, numberOfNode),
		incomingMessage:        make(chan testMessageId, int(math.Pow(float64(numberOfNode), mathPowBuff))),
		listChan:               make(map[int]*chan Blockchain.Message),
		isEmpty:                make(chan chan bool, 2),
		CloseHandler:           Blockchain.NewCloseHandler(),
		avgLatency:             avgLatency,
		listLatency:            make([]*Socket.SocketDelay, numberOfNode),
		toNotSend:              make([]bool, numberOfNode),
		toIgnore:               make(chan queryIgnore),
	}
	for i := 0; i < numberOfNode; i++ {
		channel.listLatency[i] = channel.avgLatency.NewSocketDelay(i)
	}
	return channel
}

func newTestChannelFullPBFT(numberOfNode int) *testChannel {
	newChannel := newTestChannel(numberOfNode, Socket.NewNodeDelayNoDelay())
	go newChannel.channelLoopGen()
	return newChannel
}

func newTestChannelPoANV(numberOfNode int) *testChannel {
	newChannel := newTestChannel(numberOfNode, Socket.NewNodeDelayNoDelay())
	newChannel.PoANV = true
	newChannel.incomingMessagePoA = make(chan testMessageId, int(math.Pow(float64(numberOfNode), mathPowBuff)))
	go newChannel.channelLoopGen()
	return newChannel
}

func (channel *testChannel) close() {
	channel.StopLoop()
	close(channel.incomingMessage)
	if channel.PoANV {
		close(channel.incomingMessagePoA)
	}
	close(channel.isEmpty)
	close(channel.toIgnore)
}
func (channel *testChannel) addSocket(socket *TestSocketChannel) {
	channel.listChan[socket.id] = &socket.receivedMessage
}

// Loop to transmit message without PoA
func (channel *testChannel) channelLoopGen() {
	for {
		select {
		case messID := <-channel.incomingMessage:
			channel.moved = true
			channel.broadcastGenMsg(messID)
		case messID := <-channel.incomingMessagePoA:
			channel.moved = true
			channel.broadcastPoAMsg(messID)
		case query := <-channel.toIgnore:
			channel.toNotSend[query.idToIgnore] = !channel.toNotSend[query.idToIgnore]
			query.back <- struct{}{}
		case <-channel.ToClose:
			channel.StopLoopRoutine()
			return
		case canal := <-channel.isEmpty:
			canal <- !channel.moved && (len(channel.incomingMessage) == 0) && (!channel.PoANV || len(channel.incomingMessagePoA) == 0)
			channel.moved = false
		}
	}
}

type queryIgnore struct {
	idToIgnore int
	back       chan struct{}
}

func newQueryIgnore(idToIgnore int) queryIgnore {
	return queryIgnore{idToIgnore: idToIgnore, back: make(chan struct{}, 1)}
}

func (query *queryIgnore) close() {
	close(query.back)
}

func (channel *testChannel) ignoreId(Id int) {
	idChan := newQueryIgnore(Id)
	channel.toIgnore <- idChan
	<-idChan.back
	idChan.close()
}

func (channel testChannel) IsEmpty() bool {
	canal := make(chan bool, 1)
	channel.isEmpty <- canal
	response := <-canal
	close(canal)
	return response
}

// WaitUntilEmpty wait Until the chan of the testChannel are empty
func (channel testChannel) WaitUntilEmpty() {
	var roundEmpty int
	for roundEmpty < 3 {
		time.Sleep(100 * time.Millisecond)
		if channel.IsEmpty() {
			roundEmpty++
		} else {
			roundEmpty = 0
		}
	}
}

// Distribute messages received in channel.incomingMessage to all nodes
func (channel *testChannel) broadcastGenMsg(messID testMessageId) {
	var nbMess int
	for i := range channel.listChan {
		if i != messID.iD && (!channel.PoANV || i < messID.nbVal) && !channel.toNotSend[i] {
			// *chann <- messID.message
			channel.unicastMsg(i, messID)
			nbMess++
			channel.receiveMessageDetailed[i][messID.message.Flag-1]++
		}
	}
	channel.emitMessageDetailed[messID.iD][messID.message.Flag-1] += nbMess
}

func (channel *testChannel) broadcastPoAMsg(messID testMessageId) {
	var nbMess int
	for i, chann := range channel.listChan {
		if i != messID.iD && (!channel.PoANV || i >= messID.nbVal) && !channel.toNotSend[i] {
			*chann <- messID.message
			nbMess++
			channel.receiveMessageDetailed[i][messID.message.Flag-1]++
		}
	}
	channel.emitMessageDetailed[messID.iD][messID.message.Flag-1] += nbMess
}

func (channel *testChannel) unicastMsg(id int, messID testMessageId) {
	chann := *channel.listChan[id]
	go func() {
		channel.listLatency[id].SleepNewDelay()
		chann <- messID.message
	}()
}

func (channel *testChannel) UpdateDelay(parameter int) {
	channel.avgLatency.AvgDelay = parameter
	for i, _ := range channel.listLatency {
		channel.listLatency[i] = channel.avgLatency.NewSocketDelay(i)
	}
}

type TestExperiementChannel struct {
	channel    *testChannel
	NbOfNode   int
	ListNode   []Blockchain.TestConsensus
	ListSocket []*TestSocketChannel
}

func NewTestExperiementChannel(nbOfNode int, broadcast bool, PoA bool) *TestExperiementChannel {
	experiement := &TestExperiementChannel{NbOfNode: nbOfNode}
	if PoA {
		experiement.channel = newTestChannelPoANV(nbOfNode)
	} else {
		experiement.channel = newTestChannelFullPBFT(nbOfNode)
	}
	experiement.ListNode = make([]Blockchain.TestConsensus, nbOfNode)
	experiement.ListSocket = make([]*TestSocketChannel, nbOfNode)
	for i := 0; i < nbOfNode; i++ {
		consensus, socket := createTestConsensus(i, nbOfNode, experiement.channel, broadcast, PoA)
		experiement.ListNode[i], experiement.ListSocket[i] = consensus, socket
	}
	return experiement
}

func (experiment *TestExperiementChannel) Close() {
	for _, socket := range experiment.ListSocket {
		socket.Close()
	}
	experiment.channel.close()
}

// GetExchanges Return the sum of emitted Message and the number of received Message for an TestExperiementChannel
func (experiment TestExperiementChannel) GetExchanges() ([]int, []int) {
	emitMessage, receiveMess := make([]int, experiment.NbOfNode), make([]int, experiment.NbOfNode)
	for i := range experiment.channel.emitMessageDetailed {
		for _, j := range experiment.channel.emitMessageDetailed[i] {
			emitMessage[i] = emitMessage[i] + j
		}
		for _, j := range experiment.channel.receiveMessageDetailed[i] {
			receiveMess[i] = receiveMess[i] + j
		}
	}
	return emitMessage, receiveMess
}

// GetExchangesDetailed Return the detailed of emitted Message and the number of received Message for an TestExperiementChannel
func (experiment TestExperiementChannel) GetExchangesDetailed() ([][Blockchain.NbTypeMess]int, [][Blockchain.NbTypeMess]int) {
	return experiment.channel.emitMessageDetailed, experiment.channel.receiveMessageDetailed
}

// WaitUntilEmpty wait Until the chan of the testChannel are empty
func (experiment TestExperiementChannel) WaitUntilEmpty() {
	experiment.channel.WaitUntilEmpty()
}

func (experiment *TestExperiementChannel) GetSocketAndStopMsg(idNode int) *TestSocketChannel {
	experiment.channel.ignoreId(idNode)
	return experiment.ListSocket[idNode]
}
