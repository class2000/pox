package Socket

import (
	"crypto/ed25519"
	"encoding/gob"
	"github.com/rs/zerolog/log"
	"net"
	"pbftnode/source/Blockchain"
	"sync"
)

const (
	addr     = "[::]"
	connType = "tcp"
)

type NetSocket struct {
	listSocket   *listSocket
	incoming     chan Blockchain.Message
	sockListener net.Listener
	killer       killerInter
	toClose      chan bool
	loopChan     chan bool
	consensus    Blockchain.Consensus
	nodeDelay    *NodeDelay
	stop         bool
}

type queryBroadcast struct {
	message Blockchain.Message
	back    chan bool
}

func (netsock *NetSocket) BroadcastMessageNV(message Blockchain.Message) {
	if !netsock.stop {
		query := queryBroadcast{
			message: message,
			back:    make(chan bool, 1),
		}
		netsock.listSocket.inMessageNV <- query
		<-query.back
		close(query.back)
	}
}

func NewNetSocket(consensus Blockchain.Consensus, port string, delay *NodeDelay) *NetSocket {
	GobLoader()
	var netSocket = NetSocket{
		incoming:  make(chan Blockchain.Message),
		toClose:   make(chan bool, 1),
		loopChan:  make(chan bool, 2),
		consensus: consensus,
		nodeDelay: delay,
	}
	//NetSocket.toClose = false
	netSocket.listSocket = newListSocket(consensus)
	netSocket.killer = newSocketKiller(netSocket.listSocket)
	var err error
	netSocket.sockListener, err = net.Listen(connType, addr+":"+port)
	if err != nil {
		log.Error().Msgf("Socket Error init: %s", err.Error())
	}
	log.Info().Msgf("The selected port is %s", netSocket.sockListener.Addr().String())

	go netSocket.listSocket.handleListSocket()
	go netSocket.serverLoop()
	go netSocket.manageMessage()
	return &netSocket
}

func GobLoader() {
	gob.Register(Blockchain.Transaction{})
	gob.Register(Blockchain.Message{})
	gob.Register(Blockchain.Block{})
	gob.Register(Blockchain.Prepare{})
	gob.Register(Blockchain.Commit{})
	gob.Register(Blockchain.RoundChange{})
	gob.Register(Blockchain.BrutData{})
	gob.Register(Blockchain.Commande{})
	gob.Register(Blockchain.BlockMsg{})
	gob.Register(ed25519.PublicKey{})
	gob.Register(Blockchain.SdnControlInput{})
	gob.Register(Blockchain.ConsensusResult{})
}

func (netsock *NetSocket) serverLoop() {
	for netsock.sockListener != nil {
		conn, err := netsock.sockListener.Accept()
		if err != nil {
			if len(netsock.toClose) > 0 {
				log.Debug().Msg("Should be closed")
				netsock.loopChan <- true
				return
			} else {
				log.Error().Msgf("Accept Error: %s", err.Error(), len(netsock.toClose))
			}
		} else {
			log.Info().Msgf("Accepting connection from %s", conn.RemoteAddr().String())
			//Send its ID
			id := ExchangeIdServer(netsock, conn)
			netsock.handleNewConnection(conn, id)
		}
	}
	netsock.loopChan <- true
}

func (netsock *NetSocket) manageMessage() {
	for message := range netsock.incoming {
		//log.Tracef("Received,\tfrom: %d,\ttype: %s,\tref: %s",1, Blockchain.String(message.Flag), message.Data.GetHashPayload())
		netsock.consensus.MessageHandler(message)
	}
	netsock.loopChan <- true
}

func (netsock *NetSocket) InitialiseConnection(listAddrPort []string) {

	for _, addrPort := range listAddrPort {
		conn, err := net.Dial(connType, addrPort)
		if err != nil {
			log.Error().Msgf("Error connecting: %s", err.Error())
		} else {
			id := ExchangeIdClient(netsock, conn)
			netsock.handleNewConnection(conn, id)
		}
	}
}

func (netsock *NetSocket) BroadcastMessage(message Blockchain.Message) {
	if !netsock.stop {
		netsock.listSocket.inMessage <- message
	}
}

func (netsock *NetSocket) newSingleSocket(conn *net.Conn, id int) *singleSocket {
	var waitGroup sync.WaitGroup
	var socket = singleSocket{
		wait:       &waitGroup,
		conn:       *conn,
		outcome:    make(chan Blockchain.Message),
		incoming:   &(netsock.incoming),
		killer:     &(netsock.killer),
		dec:        gob.NewDecoder(*conn),
		encoder:    gob.NewEncoder(*conn),
		toClose:    &netsock.toClose,
		loopChan:   make(chan bool, 2),
		id:         id,
		publicKey:  netsock.consensus.GetPubKeyofId(id),
		logger:     log.With().Str("Status", "Msg Received").Int("From", id).Logger(),
		delay:      netsock.nodeDelay.NewSocketDelay(id),
		inNewDelay: make(chan *SocketDelay, 1),
	}
	go socket.outcomeGoroutine()
	go socket.incomeGoroutine()
	go socket.updateSocketDelay()
	return &socket
}

// handleNewConnection checks if the connection already exist, otherwise inserts in the first available element of the list
func (netsock *NetSocket) handleNewConnection(socket net.Conn, id int) {

	if netsock.listSocket.knowAddr(socket.RemoteAddr()) {
		log.Info().Msg("socket is already known")
		err := socket.Close()
		if err != nil {
			log.Error().Msgf("Socket Error: %s", err.Error())
		}
		return
	}
	single := netsock.newSingleSocket(&socket, id)
	netsock.listSocket.appendSingleSocket(single)

	log.Printf("connected with %s from %s", socket.RemoteAddr(), socket.LocalAddr())
}

// Close Will close all existing socket
func (netsock *NetSocket) Close() {
	netsock.stop = true
	netsock.toClose <- true

	fullList := make(chan []*singleSocket)
	netsock.listSocket.inGetList <- fullList
	theList := <-fullList
	for _, socket := range theList {
		if socket != nil {
			log.Debug().Msgf("Try to close %d", socket.id)
			socket.close()
		}
	}
	close(fullList)

	netsock.killer.close()
	netsock.listSocket.close()

	err := netsock.sockListener.Close()
	if err != nil {
		log.Error().Msgf("Error socket closing: %s", err.Error())
	}
	netsock.sockListener = nil

	close(netsock.incoming)
	<-netsock.loopChan
	<-netsock.loopChan
	close(netsock.loopChan)

	close(netsock.toClose)
	log.Debug().Msg("Socket Closed")
}

func (netsocket *NetSocket) TransmitTransaction(message Blockchain.Message) {
	if !netsocket.stop {
		netsocket.listSocket.inTransac <- message
	}
}

func (netsock *NetSocket) UpdateDelay(parameter int) {
	netsock.nodeDelay.AvgDelay = parameter
	netsock.listSocket.inNewDelay <- *netsock.nodeDelay
}
