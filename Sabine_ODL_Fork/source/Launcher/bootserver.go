package Launcher

import (
	"encoding/gob"
	"github.com/rs/zerolog/log"
	"net"
)

const (
	addr      = "[::]"
	connType  = "tcp"
	strClient = "Client"
)

var listAddr []string

type BootServerArg struct {
	BaseArg
	BootstrapPort string
}

func BootServer(arg BootServerArg) {
	arg.init()

	listAddr = make([]string, 0)
	toClose := false

	conn, err := net.Listen(connType, addr+":"+arg.BootstrapPort)
	if err != nil {
		log.Fatal().Msgf("Error connecting: %s", err.Error())
	}
	log.Info().Msgf("Beginning of the BootStrap Server at : %s", arg.BootstrapPort)
	go func() {
		for {
			accepted, err := conn.Accept()
			log.Debug().Msgf("beginning of connection with %s", accepted.RemoteAddr().String())
			if err != nil {
				if toClose {
					return
				} else {
					log.Fatal().Msgf("Error accepting socket:%s, %T", err.Error(), err)
				}
			}
			var addr string
			decoder := gob.NewDecoder(accepted)
			err = decoder.Decode(&addr)
			if err != nil {
				log.Fatal().Msgf("Error decoding ID:%s", err.Error())
			}
			log.Debug().Msgf("Get addr = %s", addr)
			encoder := gob.NewEncoder(accepted)
			err = encoder.Encode(listAddr)
			if err != nil {
				log.Fatal().Msgf("Error sending list address:%s", err.Error())
			}

			if addr != strClient {
				listAddr = append(listAddr, addr)
			}
			err = accepted.Close()
			if err != nil {
				log.Fatal().Msgf("Error closing socket:%s", err.Error())
			}
			log.Info().Msgf("Connection with %s", addr)
		}
	}()

	<-arg.interruptChan
	defer func() {
		log.Info().Msg("Closure of the Server")
		toClose = true
		err = conn.Close()
		log.Info().Msg("Have a nice day")
		arg.close()
	}()
}
