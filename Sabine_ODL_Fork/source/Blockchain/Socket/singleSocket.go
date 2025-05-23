package Socket

import (
	"crypto/ed25519"
	"encoding/gob"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"io"
	"net"
	"pbftnode/source/Blockchain"
	"sync"
	"time"
)

type singleSocket struct {
	wait       *sync.WaitGroup
	logger     zerolog.Logger
	conn       net.Conn
	outcome    chan Blockchain.Message
	incoming   *chan Blockchain.Message
	killer     *killerInter
	dec        *gob.Decoder
	encoder    *gob.Encoder
	toClose    *chan bool
	loopChan   chan bool
	id         int
	publicKey  ed25519.PublicKey
	delay      *SocketDelay
	once       sync.Once
	inNewDelay chan *SocketDelay
	socketLock sync.RWMutex
}

func (single *singleSocket) outcomeGoroutine() {
	var nbError int
	for message := range single.outcome {
		err := single.encoder.Encode(message)
		if err != nil {
			log.Error().Msgf("Error gob encoding: %s", err.Error())
			nbError++
		} else {
			nbError = 0
		}
		if nbError > 3 {
			single.close()
		}
	}
	single.loopChan <- true
}

func (single *singleSocket) sendMessage(message Blockchain.Message) {
	single.wait.Add(1)
	go func() {
		defer single.wait.Done()
		time.Sleep(single.getSleepNewDelaySafe())
		single.outcome <- message
	}()
}

func (single *singleSocket) getSleepNewDelaySafe() time.Duration {
	single.socketLock.RLock()
	delay := single.delay.GetSleepNewDelay()
	single.socketLock.RUnlock()
	return delay
}

func (single *singleSocket) updateSocketDelay() {
	for newSocket := range single.inNewDelay {
		single.socketLock.Lock()
		single.delay = newSocket
		single.socketLock.Unlock()
	}
}

func (single *singleSocket) incomeGoroutine() {
	log.Debug().Msg("Launch of the incoming routine")
	for single.conn != nil {
		//single.conn.
		// Listen the socket
		var message Blockchain.Message
		err := single.dec.Decode(&message)
		if err != nil {
			_, ok := err.(*net.OpError)
			if err == io.EOF {
				log.Error().Msgf("EOF reached : deconnection")
			} else if ok {
				log.Error().Msgf("The socket of %d is closed due to the close of the connection", single.id)
			} else {
				log.Error().Msg(err.Error())
			}
			single.loopChan <- true
			if !ok || (len(*single.toClose) == 0) {
				log.Error().Msgf("Closure due to Deconnection: %d", single.id)
				single.close()
			}
			return

		}
		if message.Data != nil {
			if zerolog.GlobalLevel() <= zerolog.TraceLevel {
				single.logger.Trace().
					Int64("at", time.Now().UnixNano()).
					Str("ref", message.Data.GetHashPayload()).
					Str("type", message.Flag.String()).
					Msg("A message is received")
			}
			*single.incoming <- message
		}
	}
	single.loopChan <- true
}

func (single *singleSocket) close() {
	single.once.Do(func() {
		(*single.killer).kill(single)
	})
}
