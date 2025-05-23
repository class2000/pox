package Launcher

import (
	"encoding/gob"
	"math/rand"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Socket"
)

const nbMessChan = 128
const reconnectDelay = 500 * time.Millisecond

// --- contacts struct and methods (remain the same) ---
type contacts struct {
	listContact []string
	idContact   int
	conn        *net.Conn
	encoder     *gob.Encoder
	sync.WaitGroup
}
func newContacts(Contact string, listContact []string) *contacts {
	found := false
	for _, addr := range listContact {
		if addr == Contact {
			found = true
			break
		}
	}
	if !found && Contact != "" {
		listContact = append([]string{Contact}, listContact...)
	}
	c := &contacts{
		listContact: listContact,
		idContact:   0,
	}
	c.tryConnect()
	return c
}
func (contact *contacts) tryConnect() bool {
	if len(contact.listContact) == 0 {
		log.Error().Msg("contacts: No contacts available.")
		return false
	}
	if contact.conn != nil && *contact.conn != nil {
		(*contact.conn).Close()
		contact.conn = nil
		contact.encoder = nil
	}
	startId := contact.idContact
	for {
		addr := contact.listContact[contact.idContact]
		log.Info().Msgf("contacts: Attempting connection to node %d: %s", contact.idContact, addr)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			log.Info().Msgf("contacts: Successfully connected to %s", addr)
			contact.conn = &conn
			contact.encoder = gob.NewEncoder(conn)
			return true
		}
		log.Error().Err(err).Msgf("contacts: Failed connection to node %d: %s", contact.idContact, addr)
		contact.idContact = (contact.idContact + 1) % len(contact.listContact)
		if contact.idContact == startId {
			log.Error().Msg("contacts: Failed to connect to any node in list.")
			return false
		}
		time.Sleep(reconnectDelay)
	}
}
type sender interface {
	close()
	send(transac Blockchain.Transaction, priority bool, broadcastType Blockchain.BroadcastType)
	Wait()
	Add(int)
}
func (contact *contacts) send(transac Blockchain.Transaction, priority bool, broadcastType Blockchain.BroadcastType) {
	defer contact.Done()
	for attempt := 0; attempt < 2; attempt++ {
		if contact.conn == nil || *contact.conn == nil || contact.encoder == nil {
			log.Warn().Msg("contacts.send: No active connection, attempting reconnect...")
			if !contact.tryConnect() {
				log.Error().Str("txHash", transac.GetHashPayload()).Msg("contacts.send: Failed to send: No connection.")
				return
			}
		}
		err := contact.encoder.Encode(Blockchain.Message{
			Priority:    priority,
			Flag:        Blockchain.TransactionMess,
			Data:        transac,
			ToBroadcast: broadcastType,
		})
		if err == nil {
			log.Debug().Str("txHash", transac.GetHashPayload()).Msg("contacts.send: Transaction sent.")
			return
		}
		log.Error().Err(err).Str("txHash", transac.GetHashPayload()).Msg("contacts.send: Failed to send.")
		opErr, ok := err.(*net.OpError)
		if ok && (opErr.Err == syscall.ECONNRESET || opErr.Err == syscall.EPIPE || opErr.Timeout()) {
			log.Warn().Msg("contacts.send: Network error, attempting reconnect...")
			contact.tryConnect()
		} else {
			log.Error().Err(err).Msg("contacts.send: Unrecoverable send error.")
			return
		}
	}
	log.Error().Str("txHash", transac.GetHashPayload()).Msg("contacts.send: Failed after reconnect attempt.")
}
func (contact *contacts) close() {
	if contact.conn != nil && *contact.conn != nil {
		log.Info().Msgf("contacts: Closing connection to %s", (*contact.conn).RemoteAddr())
		(*contact.conn).Close()
		contact.conn = nil
		contact.encoder = nil
	}
}
// --- /contacts struct and methods ---


// --- multiSend Implementation ---
type singleSocket struct {
	contact    string
	id         int
	conn       net.Conn
	encoder    *gob.Encoder
	chanMsg    chan Blockchain.Message
	chanRemove chan query
	closeTx    chan chan struct{}
	wgLoop     sync.WaitGroup
}

func newSingleSocket(contact string, wait *sync.WaitGroup, chanRemove chan query) *singleSocket {
	conn, id := connectWith(contact)
	// --- ADDED LOG ---
	if conn == nil {
		log.Error().Str("contact", contact).Msg("newSingleSocket: Initial connection FAILED.")
		return nil // Return nil if connection failed
	}
	log.Info().Str("contact", contact).Int("nodeId", id).Msg("newSingleSocket: Initial connection SUCCESSFUL.")
	// --- /ADDED LOG ---

	socket := &singleSocket{
		contact:    contact,
		id:         id,
		conn:       conn,
		encoder:    gob.NewEncoder(conn),
		chanMsg:    make(chan Blockchain.Message, nbMessChan),
		closeTx:    make(chan chan struct{}),
		chanRemove: chanRemove,
	}
	socket.wgLoop.Add(1)
	go socket.loopSocket(wait)
	return socket
}

func (socket *singleSocket) loopSocket(wait *sync.WaitGroup) {
	defer socket.wgLoop.Done()
	log.Debug().Str("contact", socket.contact).Int("id", socket.id).Msg("singleSocket loop started.")

	for {
		select {
		case msg, ok := <-socket.chanMsg:
			if !ok {
				log.Debug().Str("contact", socket.contact).Msg("singleSocket message channel closed.")
				if socket.conn != nil { socket.conn.Close() }
				return
			}

			// --- ADDED LOG ---
			log.Trace().Str("contact", socket.contact).Int("id", socket.id).Str("msgType", msg.Flag.String()).Msg("singleSocket received message on chanMsg, attempting encode/send...")
			// --- /ADDED LOG ---

			if socket.conn == nil || socket.encoder == nil {
				log.Error().Str("contact", socket.contact).Msg("singleSocket: Attempted send on nil connection/encoder.")
				wait.Done()
				continue
			}

			err := socket.encoder.Encode(msg)
			// --- ADDED LOG ---
			if err != nil {
				log.Error().Err(err).Str("contact", socket.contact).Int("id", socket.id).Msg("singleSocket: FAILED to encode/send message.")
			} else {
				log.Trace().Int("toId", socket.id).Str("type", msg.Flag.String()).Str("txHash", msg.Data.GetHashPayload()).Msg("singleSocket: Message sent successfully.")
			}
			// --- /ADDED LOG ---
			wait.Done() // Decrement sender waitgroup *after* attempting send

			if err != nil {
				// Signal removal and exit loop on error
				query := newQuery(socket.contact)
				select { // Non-blocking send for removal request
				case socket.chanRemove <- query:
					query.wait()
				default:
					log.Warn().Str("contact", socket.contact).Msg("singleSocket: Failed removal request (channel full/closed).")
				}
				if socket.conn != nil { socket.conn.Close() }
				return // Exit loop
			}

		case closeAckChan := <-socket.closeTx:
			log.Debug().Str("contact", socket.contact).Msg("singleSocket received close signal.")
			close(socket.chanMsg)
			for range socket.chanMsg { // Drain buffer
				wait.Done()
			}
			if socket.conn != nil { socket.conn.Close() }
			closeAckChan <- struct{}{}
			log.Debug().Str("contact", socket.contact).Msg("singleSocket loop finished.")
			return
		}
	}
}

type query struct {
	contact string
	channel chan struct{}
}
func newQuery(contact string) query {
	return query{contact, make(chan struct{}, 1)}
}
func (q *query) wait() {
	<-q.channel
	close(q.channel)
}

type multiSend struct {
	chanMsg        chan Blockchain.Message
	closeChan      chan chan struct{}
	waiter         sync.WaitGroup
	uniformDistrib bool
	nodeCount      int
	sockets        map[string]*singleSocket
	socketMutex    sync.RWMutex
}
func (send *multiSend) close() {
	log.Info().Msg("Closing multiSend...")
	closeAckChan := make(chan struct{})
	select {
	case send.closeChan <- closeAckChan:
		<-closeAckChan
	default:
		log.Warn().Msg("multiSend closeChan blocked or closed already.")
	}
	close(send.closeChan)
	log.Info().Msg("multiSend closed.")
}
func (send *multiSend) send(transac Blockchain.Transaction, priority bool, broadcastType Blockchain.BroadcastType) {
	msg := Blockchain.Message{
		Priority:    priority,
		Flag:        Blockchain.TransactionMess,
		Data:        transac,
		ToBroadcast: broadcastType,
	}
	select {
	case send.chanMsg <- msg:
	default:
		log.Warn().Str("txHash", transac.GetHashPayload()).Msg("multiSend message channel full, dropping transaction.")
		send.waiter.Done()
	}
}
func (send *multiSend) Wait() { send.waiter.Wait() }
func (send *multiSend) Add(id int) { send.waiter.Add(id) }

func newMultiSend(listContact []string, uniformDistrib bool) *multiSend {
	multisend := &multiSend{
		chanMsg:        make(chan Blockchain.Message, nbMessChan*len(listContact)),
		closeChan:      make(chan chan struct{}),
		uniformDistrib: uniformDistrib,
		sockets:        make(map[string]*singleSocket),
		nodeCount:      0,
	}
	go multisend.loopSendTxMulti(listContact)
	return multisend
}

func (send *multiSend) loopSendTxMulti(initialContacts []string) {
	log.Info().Msg("multiSend distribution loop started.")
	chanRemove := make(chan query)

	// Initialize sockets
	send.socketMutex.Lock()
	for _, contact := range initialContacts {
		// --- ADDED LOG ---
		log.Debug().Str("contact", contact).Msg("multiSend: Attempting initial socket creation.")
		// --- /ADDED LOG ---
		if sock := newSingleSocket(contact, &send.waiter, chanRemove); sock != nil {
			send.sockets[contact] = sock
		}
		// No else needed, count based on len(sockets) below
	}
	send.nodeCount = len(send.sockets) // Set count AFTER attempting all connections
	send.socketMutex.Unlock()
	log.Info().Int("initialAttempt", len(initialContacts)).Int("activeSockets", send.nodeCount).Msg("multiSend: Initial socket connections established.")


	// Main distribution loop
	for {
		select {
		case queryRemove := <-chanRemove:
			send.socketMutex.Lock()
			log.Warn().Str("contact", queryRemove.contact).Msg("multiSend: Removing socket due to error.")
			if _, exists := send.sockets[queryRemove.contact]; exists {
				delete(send.sockets, queryRemove.contact)
				send.nodeCount = len(send.sockets)
				log.Info().Int("activeSockets", send.nodeCount).Msg("multiSend: Socket removed.")
			} else {
				log.Debug().Str("contact", queryRemove.contact).Msg("multiSend: Socket already removed.")
			}
			send.socketMutex.Unlock()
			select{
			case queryRemove.channel <- struct{}{}:
			default: log.Warn().Str("contact", queryRemove.contact).Msg("multiSend: Failed removal ack.")
			}


		case msg, ok := <-send.chanMsg:
			if !ok {
				log.Info().Msg("multiSend message channel closed, exiting loop.")
				return
			}

			// --- ADDED LOG ---
			log.Debug().Str("msgType", msg.Flag.String()).Str("txHash", msg.Data.GetHashPayload()).Msg("multiSend received message for distribution.")
			// --- /ADDED LOG ---

			send.socketMutex.RLock()
			currentSocketCount := send.nodeCount
			if currentSocketCount == 0 {
				log.Warn().Str("type", msg.Flag.String()).Msg("multiSend: No active sockets, dropping message.")
				send.waiter.Done()
				send.socketMutex.RUnlock()
				continue
			}

			if send.uniformDistrib {
				log.Trace().Str("type", msg.Flag.String()).Int("count", currentSocketCount).Msg("multiSend: Broadcasting message.")
				broadcastMsg := msg
				broadcastMsg.ToBroadcast = Blockchain.DontBroadcast

				activeSockets := make([]*singleSocket, 0, currentSocketCount)
				for _, socket := range send.sockets {
					activeSockets = append(activeSockets, socket)
				}
				send.socketMutex.RUnlock() // Unlock before sending

				for _, socket := range activeSockets {
					// --- ADDED LOG ---
					log.Trace().Str("contact", socket.contact).Int("id", socket.id).Msg("multiSend: Sending broadcast to socket.")
					// --- /ADDED LOG ---
					select {
					case socket.chanMsg <- broadcastMsg:
					default:
						log.Warn().Str("contact", socket.contact).Msg("multiSend: Socket chan full during broadcast, skipping.")
						send.waiter.Done()
					}
				}
			} else {
				// Send to a random active socket
				socketsList := make([]*singleSocket, 0, currentSocketCount)
				for _, s := range send.sockets {
					socketsList = append(socketsList, s)
				}
				send.socketMutex.RUnlock() // Unlock before sending

				if len(socketsList) > 0 {
					targetSocket := socketsList[rand.Intn(len(socketsList))]
					// --- ADDED LOG ---
					log.Trace().Str("type", msg.Flag.String()).Int("targetId", targetSocket.id).Msg("multiSend: Sending message randomly.")
					// --- /ADDED LOG ---
					select {
					case targetSocket.chanMsg <- msg:
					default:
						log.Warn().Str("contact", targetSocket.contact).Msg("multiSend: Target socket chan full, dropping message.")
						send.waiter.Done()
					}
				} else {
					log.Warn().Str("type", msg.Flag.String()).Msg("multiSend: No sockets available for random send, dropping.")
					send.waiter.Done()
				}
			}


		case closeAckChan := <-send.closeChan:
			log.Info().Msg("multiSend received shutdown signal.")
			close(send.chanMsg)

			send.socketMutex.Lock()
			log.Info().Int("count", len(send.sockets)).Msg("multiSend: Closing all active sockets...")
			socketCloseAcks := make([]chan struct{}, 0, len(send.sockets))
			for contact, socket := range send.sockets {
				log.Debug().Str("contact", contact).Msg("multiSend: Sending close signal to socket.")
				ack := make(chan struct{})
				socketCloseAcks = append(socketCloseAcks, ack)
				select {
				case socket.closeTx <- ack:
				default:
					log.Warn().Str("contact", contact).Msg("multiSend: Could not send close signal (already exiting?).")
					close(ack)
				}
				delete(send.sockets, contact)
			}
			send.nodeCount = 0
			send.socketMutex.Unlock()

			log.Debug().Int("count", len(socketCloseAcks)).Msg("multiSend: Waiting for socket acknowledgements...")
			var wgClose sync.WaitGroup
			wgClose.Add(len(socketCloseAcks))
			for _, ack := range socketCloseAcks {
				go func(c chan struct{}){
					<- c
					wgClose.Done()
				}(ack)
			}
			wgClose.Wait()

			log.Info().Msg("multiSend: All sockets closed. Acknowledging shutdown.")
			closeAckChan <- struct{}{}
			return
		}
	}
}


// connectWith establishes a connection and performs ID exchange.
func connectWith(Contact string) (net.Conn, int) {
	log.Debug().Str("contact", Contact).Msg("Attempting connectWith")
	conn, err := net.DialTimeout("tcp", Contact, 5*time.Second)
	if err != nil {
		log.Error().Err(err).Str("contact", Contact).Msg("connectWith: Failed to dial")
		return nil, -1
	}
	log.Debug().Str("contact", Contact).Msg("connectWith: Dial successful")

	id := Socket.ExchangeIdClient(nil, conn)
	log.Debug().Str("contact", Contact).Int("receivedId", id).Msg("connectWith: ID exchanged")

	if id < -1 {
		log.Warn().Str("contact", Contact).Int("receivedId", id).Msg("connectWith: Received unexpected ID during exchange.")
	}

	return conn, id
}