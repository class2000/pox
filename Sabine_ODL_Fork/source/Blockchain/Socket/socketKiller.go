package Socket

import (
	"github.com/rs/zerolog/log"
)

type killerInter interface {
	kill(single *singleSocket)
	close()
}

type socketKiller struct {
	listSocket listSocketInterf
	contract   chan querySingle
	toClose    chan bool
}

func newSocketKiller(interf listSocketInterf) *socketKiller {
	killer := &socketKiller{
		contract:   make(chan querySingle),
		listSocket: interf,
		toClose:    make(chan bool, 1),
	}
	go killer.handleContract()
	return killer
}

func (killer *socketKiller) close() {
	killer.kill(nil)
	close(killer.contract)
	<-killer.toClose
}

func (killer *socketKiller) killRoutine(task querySingle) {
	single := task.single
	log.Debug().Msg("Killer try")
	if single != nil && killer.listSocket.askIfExist(single) {
		log.Debug().Msgf("Killer kill %d", single.id)
		single.wait.Wait()
		close(single.outcome)
		err := single.conn.Close()
		if err != nil {
			log.Error()
		}

		killer.listSocket.askForRemoving(single)
		close(single.inNewDelay)

		<-single.loopChan
		<-single.loopChan
		close(single.loopChan)
		log.Debug().Msg("Contract terminated")
	}

	task.canal <- true
}

func (killer *socketKiller) kill(single *singleSocket) {
	query := newQuerySingle(single)
	killer.contract <- query
	<-query.canal
	query.close()
}

func (killer *socketKiller) handleContract() {
	for task := range killer.contract {
		killer.killRoutine(task)
	}
	killer.toClose <- true
}
