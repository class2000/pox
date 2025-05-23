package Socket

import (
	"encoding/gob"
	"github.com/rs/zerolog/log"
	"net"
)

func ExchangeIdServer(netsock *NetSocket, conn net.Conn) int {
	var idSend int = -1
	if netsock != nil {
		idSend = netsock.consensus.GetId()
	}
	sendID(idSend, conn)
	id := receiveId(conn)
	return id
}

func ExchangeIdClient(netsock *NetSocket, conn net.Conn) int {
	id := receiveId(conn)
	var idSend int = -1
	if netsock != nil {
		idSend = netsock.consensus.GetId()
	}
	sendID(idSend, conn)
	return id
}

func receiveId(conn net.Conn) int {
	decoder := gob.NewDecoder(conn)
	var id int
	err := decoder.Decode(&id)
	if err != nil {
		log.Error().Msgf("Error receiving the ID : %s", err)
	}
	return id
}

func sendID(id int, conn net.Conn) {
	encoder := gob.NewEncoder(conn)
	err := encoder.Encode(id)
	if err != nil {
		log.Error().Msgf("Error sending the ID : %s", err)
	}
}
