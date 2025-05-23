package Socket

import (
	"bytes"
	"github.com/rs/zerolog/log"
	"net"
	"pbftnode/source/Blockchain"
)

// Handler of the list of singleSocket in a NetSocket
type listSocket struct {
	Blockchain.CloseHandler
	listSocket   []*singleSocket
	inMessage    chan Blockchain.Message
	inMessageNV  chan queryBroadcast
	inTransac    chan Blockchain.Message
	inAddr       chan queryAddr
	inAppendSock chan querySingle
	inRemoveSock chan querySingle
	inExist      chan querySingle
	inGetList    chan chan []*singleSocket
	inNewDelay   chan NodeDelay
	consensus    Blockchain.Consensus
}

type listSocketInterf interface {
	askForRemoving(*singleSocket)
	askIfExist(*singleSocket) bool
}

func newListSocket(consensus Blockchain.Consensus) *listSocket {
	return &listSocket{
		CloseHandler: Blockchain.NewCloseHandler(),
		listSocket:   []*singleSocket{},
		inMessage:    make(chan Blockchain.Message),
		inMessageNV:  make(chan queryBroadcast),
		inTransac:    make(chan Blockchain.Message),
		inAddr:       make(chan queryAddr),
		inAppendSock: make(chan querySingle),
		inRemoveSock: make(chan querySingle),
		inExist:      make(chan querySingle),
		inGetList:    make(chan chan []*singleSocket),
		inNewDelay:   make(chan NodeDelay),
		consensus:    consensus,
	}
}

// Generic broadcast, the message is sent to all nodes
func (list *listSocket) broadcastMessage(message Blockchain.Message) {
	for _, socket := range list.listSocket {
		// if socket != nil && (!list.consensus.IsPoANV() || socket.id < list.consensus.GetNumberOfValidator()) {
		if socket != nil && (!list.consensus.IsPoANV() || socket.id == -1 || list.consensus.IsActiveValidator(socket.publicKey)) {
			socket.sendMessage(message)
		}
	}
}

// Generic broadcast, the message is sent to all nodes
func (list *listSocket) broadcastMessageNV(query queryBroadcast) {
	message := query.message
	if !list.consensus.IsPoANV() {
		log.Panic().Msgf("This Channel shouldn't be used")
	}
	for _, socket := range list.listSocket {
		if socket != nil && !list.consensus.IsActiveValidator(socket.publicKey) {
			socket.sendMessage(message)
		}
	}
	query.back <- true
}

func (list *listSocket) handleListSocket() {
	for {
		select {
		case <-list.ToClose:
			list.StopLoopRoutine()
			return
		case message := <-list.inMessage:
			list.broadcastMessage(message)
		case query := <-list.inMessageNV:
			list.broadcastMessageNV(query)
		case message := <-list.inTransac:
			list.transmitTransaction(message)
		case addr := <-list.inAddr:
			list.knowAddrRoutine(addr)
		case sock := <-list.inAppendSock:
			list.appendSingleSocketRoutine(sock)
		case sock := <-list.inRemoveSock:
			list.removeSingleRoutine(sock)
		case query := <-list.inExist:
			list.sendIfExist(&query)
		case fullList := <-list.inGetList:
			fullListCp := make([]*singleSocket, len(list.listSocket))
			copy(fullListCp, list.listSocket)
			fullList <- fullListCp
		case newDelay := <-list.inNewDelay:
			list.updateDelay(newDelay)
		}
	}
}

func (list *listSocket) close() {
	list.StopLoop()
	close(list.inMessage)
	close(list.inMessageNV)
	close(list.inTransac)
	close(list.inAddr)
	close(list.inAppendSock)
	close(list.inRemoveSock)
	close(list.inExist)
	close(list.inGetList)
	close(list.inNewDelay)
}

type queryAddr struct {
	addr  net.Addr
	canal chan bool
}

func newQueryAddr(addr net.Addr) queryAddr {
	return queryAddr{addr: addr, canal: make(chan bool, 1)}
}

func (query *queryAddr) close() {
	close(query.canal)
}

func (list listSocket) knowAddrRoutine(queryAddr queryAddr) {
	for _, knownSocket := range list.listSocket {
		if knownSocket != nil && knownSocket.conn.RemoteAddr() == queryAddr.addr {
			queryAddr.canal <- true
			return
		}
	}
	queryAddr.canal <- false
}

// Return true if the requested addr is already known
func (list *listSocket) knowAddr(addr net.Addr) bool {
	query := newQueryAddr(addr)
	list.inAddr <- query
	rep := <-query.canal
	query.close()
	return rep
}

type querySingle struct {
	single *singleSocket
	canal  chan bool
}

func newQuerySingle(single *singleSocket) querySingle {
	return querySingle{single: single, canal: make(chan bool, 1)}
}

func (query querySingle) close() {
	close(query.canal)
}

func (list *listSocket) appendSingleSocketRoutine(query querySingle) {
	i := list.firstNilSocket()
	if i < 0 {
		list.listSocket = append(list.listSocket, query.single)
		log.Info().Msg("Socket Append")
	} else {
		list.listSocket[i] = query.single
		log.Info().Msg("A place was found")
	}
	query.canal <- true
}

// firstNilSocket select the index of the first nil socket in the list if exist, -1 otherwise
func (list listSocket) firstNilSocket() int {
	for i, socket := range list.listSocket {
		if socket == nil {
			return i
		}
	}
	return -1
}

func (list *listSocket) appendSingleSocket(single *singleSocket) {
	query := newQuerySingle(single)
	list.inAppendSock <- query
	<-query.canal
	query.close()
}

func (list *listSocket) removeSingleRoutine(query querySingle) {
	for i, socket := range list.listSocket {
		if socket == query.single {
			list.listSocket[i] = nil
		}
	}
	query.canal <- true
}

func (list *listSocket) transmitTransaction(message Blockchain.Message) {
	proposer := list.consensus.GetProposer()
	for _, socket := range list.listSocket {
		if socket != nil && bytes.Equal(socket.publicKey, proposer) {
			socket.sendMessage(message)
			return
		}
	}
	go func() {
		list.inMessage <- message
	}()
}

func (list *listSocket) sendIfExist(query *querySingle) {
	single := query.single
	for _, socket := range list.listSocket {
		if socket == single {
			query.canal <- true
			return
		}
	}
	query.canal <- false
}

func (list *listSocket) askForRemoving(single *singleSocket) {
	query := newQuerySingle(single)
	list.inRemoveSock <- query
	<-query.canal
	query.close()
}

func (list *listSocket) askIfExist(socket *singleSocket) bool {
	query := newQuerySingle(socket)
	list.inExist <- query
	variable := <-query.canal
	query.close()
	return variable
}

func (list *listSocket) updateDelay(delay NodeDelay) {
	for _, socket := range list.listSocket {
		if socket != nil {
			socket.inNewDelay <- delay.NewSocketDelay(socket.id)
		}
	}
}
