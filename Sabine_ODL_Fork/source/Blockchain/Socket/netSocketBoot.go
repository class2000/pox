package Socket

import (
	"encoding/gob"
	"github.com/rs/zerolog/log"
	"net"
	"pbftnode/source/Blockchain"
	"strings"
	"time"
)

type NetSocketBoot struct {
	NetSocket
	bootstrapIp string
	listAddr    []string
	id          int
}

const nbTry int = 180

func NewNetSocketBoot(consensus Blockchain.Consensus, bootstrap string, listeningPort string, delay *NodeDelay) *NetSocketBoot {
	var try int = nbTry
	var err error
	var conn net.Conn
	for try > 0 && conn == nil {
		conn, err = net.Dial(connType, bootstrap)
		if err != nil {
			log.Error().Msgf("Error Connection to bootstrap Server: %s, test %d/%d", err.Error(), 1+nbTry-try, nbTry)
			time.Sleep(1 * time.Second)
		}
		try--
	}
	if try == 0 && err != nil {
		log.Panic().Msgf("Error Connection to bootstrap Server: %s", err.Error())
	}
	retour := NetSocketBoot{NetSocket: *NewNetSocket(consensus, listeningPort, delay), bootstrapIp: bootstrap}

	//Send the ID of the listening socket
	encoder := gob.NewEncoder(conn)
	var socketListenerAddr string
	{
		addrList := strings.Split(conn.LocalAddr().String(), ":")[0]
		separation := strings.Split(retour.sockListener.Addr().String(), ":")
		port := separation[len(separation)-1]
		socketListenerAddr = addrList + ":" + port
	}
	err = encoder.Encode(socketListenerAddr)
	if err != nil {
		log.Fatal().Msgf("Error encoding listening socket data: %s", err.Error())
	}

	//Receiving the other nodes information
	decoder := gob.NewDecoder(conn)
	err = decoder.Decode(&retour.listAddr)
	if err != nil {
		log.Fatal().Msgf("Error decoding: %s", err.Error())
	}
	log.Info().Msgf("Try to connect with %s", retour.listAddr)

	err = conn.Close()
	if err != nil {
		log.Fatal().Msgf("Error closing socket: %s", err.Error())
	}

	return &retour
}

func (n *NetSocketBoot) InitBootstrapedCo() {
	n.InitialiseConnection(n.listAddr)
}
