package Blockchain

import (
	"github.com/rs/zerolog/log"
	"math"
	"pbftnode/source/Blockchain/ControlLoop"
	"time"
)

type Queue interface {
	Append(float642 float64)
	Mean() float64
}

type queue struct {
	value float64
	next  *queue
}

type FixSizeQueue struct {
	size  int
	first *queue
	last  *queue
}

func NewFixSizeQueue(size int) *FixSizeQueue {
	if size < 2 {
		return nil
	}
	fixQueue := &FixSizeQueue{size: size}
	fixQueue.first = &queue{}
	currentQueue := fixQueue.first
	for i := 0; i < size-1; i++ {
		currentQueue.next = &queue{}
		currentQueue = currentQueue.next
	}
	fixQueue.last = currentQueue
	return fixQueue
}

func (fixQueue *FixSizeQueue) Append(item float64) {
	newLast := &queue{value: item}
	fixQueue.last.next = newLast
	fixQueue.last = newLast
	fixQueue.first = fixQueue.first.next
}

func (fixQueue FixSizeQueue) Mean() float64 {
	var sum float64
	currentQueue := fixQueue.first
	for i := 0; i < fixQueue.size; i++ {
		sum += currentQueue.value
		currentQueue = currentQueue.next
	}
	return sum / float64(fixQueue.size)
}

type FixTor struct {
	size         int
	data         []float64
	currentIndex int
	mean_        float64
}

func NewFixTor(size int) *FixTor {
	return &FixTor{
		size:         size,
		data:         make([]float64, size),
		currentIndex: 0,
	}
}

func (tor *FixTor) Append(item float64) {
	tor.data[tor.currentIndex] = item
	tor.currentIndex = (tor.currentIndex + 1) % tor.size
	tor.mean_ = tor.mean()
}

func (tor FixTor) mean() float64 {
	var sum float64
	for _, item := range tor.data {
		sum += item
	}
	return sum / float64(tor.size)
}

func (tor FixTor) Mean() float64 {
	return tor.mean_
}

//const controlPeriod time.Duration = 30 * refreshingPeriod

type ControlType uint8

const (
	OneValidator ControlType = iota
	Hysteresis
	ModelComparison
)

func ControlTypeStr(controlName string) ControlType {
	switch controlName {
	case "OneValidator":
		return OneValidator
	case "Hysteresis":
		return Hysteresis
	case "ModelComparison":
		return ModelComparison
	default:
		log.Error().Msgf("The type %s is unknown, the type is set to OneValidator", controlName)
		return OneValidator
	}
}

type ControlFeedBack struct {
	metric             *MetricHandler
	consensus          Consensus
	chanInstruction    chan bool
	toKillLoop         chan chan<- struct{}
	chanFromMetHandler chan [3]float64
	chanPropo          chan chan<- struct{}
	modelVal           ControlLoop.ModelValInterf
	refreshinPeriod    time.Duration
	controlPeriod      time.Duration
}

func NewControlFeedBack(metric *MetricHandler, consensus Consensus, modelFile string, buffer int) *ControlFeedBack {
	control := &ControlFeedBack{
		metric:             metric,
		consensus:          consensus,
		chanInstruction:    make(chan bool),
		chanFromMetHandler: make(chan [3]float64),
		toKillLoop:         make(chan chan<- struct{}),
		chanPropo:          make(chan chan<- struct{}),
		modelVal:           ControlLoop.NewModelVal(modelFile),
		controlPeriod:      time.Duration(buffer) * metric.refreshingPeriod,
	}
	metric.chanForChanControlHandler <- control.chanFromMetHandler
	log.Debug().Msgf("The buffer is long of %d", buffer)
	go control.controlLoop(control.chanInstruction, control.chanFromMetHandler, control.toKillLoop, control.chanPropo, buffer)
	return control
}

func (control *ControlFeedBack) controlLoop(chanInstruction <-chan bool, chanFromMetHandler <-chan [3]float64, toKillLoop <-chan chan<- struct{}, chanPropo <-chan chan<- struct{}, buffer int) {
	var Instruction bool
	var committedThroughputQueue Queue = NewFixTor(buffer)
	var requestedThroughputQueue Queue = NewFixTor(buffer)
	var blockThroughputQueue Queue = NewFixTor(buffer)
	for {
		select {
		case throughputs := <-chanFromMetHandler:
			committedTx, requestedTx, blockThroughput := throughputs[0], throughputs[1], throughputs[2]
			committedThroughputQueue.Append(committedTx)
			requestedThroughputQueue.Append(requestedTx)
			blockThroughputQueue.Append(blockThroughput)
		case channel := <-chanPropo:
			log.Debug().Msg("Order Time")
			control.makeOrder(committedThroughputQueue, requestedThroughputQueue, blockThroughputQueue, Instruction)
			channel <- struct{}{}
		case instruct := <-chanInstruction:
			log.Info().Msgf("Receive chain Instruc")
			Instruction = instruct
			log.Info().Msgf("Instruction set to %t", instruct)
		case channel := <-toKillLoop:
			Instruction = false
			channel <- struct{}{}
			return
		}
	}
}

func (control *ControlFeedBack) CheckControl() {
	channel := make(chan struct{})
	control.chanPropo <- channel
	<-channel
}

func (control *ControlFeedBack) makeOrder(committedThroughputQueue Queue, requestedThroughputQueue Queue, blockThroughputQueue Queue, Instruction bool) {
	committedThroughput, emittedThroughput := committedThroughputQueue.Mean(), requestedThroughputQueue.Mean()
	blockThroughput := blockThroughputQueue.Mean()
	if Instruction && control.consensus.IsOlderThan(control.controlPeriod) && emittedThroughput != 0 && committedThroughput != 0 {
		log.Info().Msg("Make Order")
		var tx *Transaction
		// tx = oneValidatorControl(committedThroughput, emittedThroughput, control)
		tx = control.consensus.GetControl().Control(committedThroughput, emittedThroughput, blockThroughput, control)
		control.consensus.ReceiveTrustedMess(Message{
			Flag:        TransactionMess,
			Data:        *tx,
			ToBroadcast: AskToBroadcast,
		})
	}
}

func (cType ControlType) Control(committedThroughput float64, requestedThroughput float64, blockThroughput float64, control *ControlFeedBack) (tx *Transaction) {
	switch cType {
	case OneValidator:
		return oneValidatorControl(committedThroughput, requestedThroughput, control)
	case Hysteresis:
		return HysteresisControl(committedThroughput, requestedThroughput, control)
	case ModelComparison:
		return ModelCmpControl(committedThroughput, requestedThroughput, blockThroughput, control)
	default:
		log.Panic().Msg("Unknown ControlType")
	}
	return nil
}

func ModelCmpControl(requestedThroughput float64, emittedThroughput float64, blockThroughput float64, control *ControlFeedBack) (tx *Transaction) {
	idealNbValue := control.modelVal.GetIdealNbVal(emittedThroughput, requestedThroughput, blockThroughput, control.consensus.GetNumberOfValidator())
	actualNbVal := control.consensus.GetNumberOfValidator()
	variation := 0
	if idealNbValue != 0 {
		variation = idealNbValue - actualNbVal
	}
	log.Info().Msgf("Ask for a variation of : %d", variation)
	newListProposal := control.consensus.GenerateNewValidatorListProposition(idealNbValue)
	tx = control.consensus.MakeTransaction(Commande{
		Order:           VarieValid,
		Variation:       variation,
		NewValidatorSet: newListProposal,
	})
	return tx
}

func oneValidatorControl(committedThroughput float64, emittedThroughput float64, control *ControlFeedBack) (tx *Transaction) {
	var variation int
	if math.Abs(committedThroughput-emittedThroughput)/committedThroughput < 0.1 {
		variation = 1
		log.Info().Msg("Ask to increase")
	} else {
		variation = -1
		log.Info().Msg("Ask to decrease")
	}
	actualNbVal := control.consensus.GetNumberOfValidator()
	newListProposal := control.consensus.GenerateNewValidatorListProposition(actualNbVal + variation)
	tx = control.consensus.MakeTransaction(Commande{
		Order:           VarieValid,
		Variation:       variation,
		NewValidatorSet: newListProposal,
	})

	return tx
}

const delta float64 = 0.05

func HysteresisControl(committedThroughput float64, emittedThroughput float64, control *ControlFeedBack) (tx *Transaction) {
	var variation int
	if (emittedThroughput-committedThroughput)/committedThroughput > 0.1+delta {
		variation = 1
		log.Info().Msg("Ask to increase")
	} else if (emittedThroughput-committedThroughput)/committedThroughput < 0.1-delta {
		variation = -1
		log.Info().Msg("Ask to decrease")
	} else {
		variation = 0
		log.Info().Msg("Ask to not change")
	}
	actualNbVal := control.consensus.GetNumberOfValidator()
	newListProposal := control.consensus.GenerateNewValidatorListProposition(actualNbVal + variation)
	tx = control.consensus.MakeTransaction(Commande{
		Order:           VarieValid,
		Variation:       variation,
		NewValidatorSet: newListProposal,
	})
	return tx
}

func (control *ControlFeedBack) Close() {
	control.metric.chanForChanControlHandler <- nil
	channelStop := make(chan struct{})
	control.toKillLoop <- channelStop
	<-channelStop
	close(channelStop)
	close(control.toKillLoop)
	close(control.chanFromMetHandler)
	close(control.chanInstruction)
	close(control.chanPropo)
}

func (control *ControlFeedBack) SetInstruction(instruction bool) {
	control.chanInstruction <- instruction
}
