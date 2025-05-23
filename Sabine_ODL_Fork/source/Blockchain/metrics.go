package Blockchain

import (
	"encoding/json"
	"github.com/rs/zerolog/log"
	"math"
	"net/http"
	"os"
	"pbftnode/source/config"
	"sync"
	"time"
)

type Metrics struct {
	NbOfTxInLastBlock   int     `json:"nb_of_tx_in_last_block"`
	LastBlockTime       int64   `json:"last_block_time"`
	CommittedThroughput float64 `json:"committed_throughput"`
	ReceivedThroughput  float64 `json:"received_throughput"`
	BlockThroughput     float64 `json:"committed_block_throughput"`
	DerivThroughput     float64 `json:"deriv_throughput"`
	NumberOfValidator   int     `json:"number_of_validator"`
	Blocktime           int64   `json:"blocktime"`
	BlockOccupation     float64 `json:"block_occupation"`
}

type MetricInterf interface {
	SetHTTPViewer(port string)
	GetThroughputs() (float64, float64, float64, float64)
	AddOneCommittedTx()
	AddNCommittedTx(int)
	AddOneReceivedTx()
	AddOneCommittedBlock()
	GetMetrics() Metrics
}

type MetricHandler struct {
	chain                     *Blockchain
	validator                 ValidatorGetterInterf
	metricServer              *http.Server
	tickerSave                int
	refreshingPeriod          time.Duration
	addComittedTx             chan uint
	addReceivedTx             chan struct{}
	addComittedBlock          chan struct{}
	getSumTx                  chan chan<- [3]uint
	throughputChan            chan chan<- [4]float64
	toKill                    chan chan<- bool
	toKillTicker              chan chan<- bool
	chanForChanControlHandler chan chan<- [3]float64
	ToSave                    bool
	MetricSave                metricStack
}

func NewMetricHandler(chainInterface *Blockchain, validatorGetterInterf ValidatorGetterInterf, metricSaveFile string, tickerSave int, refreshingPeriod int) *MetricHandler {
	metricHandler := &MetricHandler{
		chain:                     chainInterface,
		validator:                 validatorGetterInterf,
		tickerSave:                tickerSave,
		refreshingPeriod:          time.Duration(refreshingPeriod) * time.Second,
		addComittedTx:             make(chan uint, 100),
		addReceivedTx:             make(chan struct{}, 100),
		addComittedBlock:          make(chan struct{}, 100),
		getSumTx:                  make(chan chan<- [3]uint),
		toKill:                    make(chan chan<- bool),
		throughputChan:            make(chan chan<- [4]float64, 2),
		toKillTicker:              make(chan chan<- bool),
		chanForChanControlHandler: make(chan chan<- [3]float64),
	}
	if metricSaveFile == "" {
		metricHandler.MetricSave = whiteMetricSatck{}
	} else {
		metricHandler.MetricSave = newMetricStackSave(metricHandler, metricSaveFile)
	}
	go metricHandler.loopSumTx(metricHandler.addComittedTx, metricHandler.addReceivedTx, metricHandler.addComittedBlock, metricHandler.getSumTx, metricHandler.toKill)
	go metricHandler.loopTicker(metricHandler.chanForChanControlHandler, metricHandler.throughputChan, metricHandler.toKillTicker)
	return metricHandler
}

func (handler *MetricHandler) SetHTTPViewer(port string) {
	if port != "" {
		handler.metricServer = HttpMetricViewer(handler, port)
	}
}

func (handler *MetricHandler) loopTicker(chanForChanControlHandler <-chan chan<- [3]float64, getThroughput <-chan chan<- [4]float64, toKillTicker <-chan chan<- bool) {
	ticker := time.NewTicker(handler.refreshingPeriod)
	var chanToControler chan<- [3]float64
	var OldCommittedThroughput float64
	var OldBlockCommittedThroughput float64
	var OldRequestedThroughput float64
	var OldCommittedBlockOccupation float64
	var chanSaveTicker <-chan time.Time
	if handler.tickerSave > 0 {
		var saveTicker *time.Ticker
		saveTicker = time.NewTicker(time.Duration(handler.tickerSave) * time.Minute)
		chanSaveTicker = saveTicker.C
	}
	for {
		select {
		case <-ticker.C:
			period := float64(handler.refreshingPeriod.Nanoseconds()) / float64(time.Second.Nanoseconds())
			committedTx, receivedTx, committedBlock := handler.getSumOfTx()
			OldCommittedThroughput = float64(committedTx) / period
			OldRequestedThroughput = float64(receivedTx) / period
			OldBlockCommittedThroughput = float64(committedBlock) / period
			if committedBlock == 0 {
				OldCommittedBlockOccupation = 0
			} else {
				OldCommittedBlockOccupation = float64(committedTx) / float64(committedBlock) / config.BlockSize
			}
			if chanToControler != nil {
				chanToControler <- [3]float64{OldCommittedThroughput, OldRequestedThroughput, OldBlockCommittedThroughput}
			}
			handler.MetricSave.update()
		case channel := <-chanForChanControlHandler:
			log.Info().Msg("Metrics: Channel Updated")
			chanToControler = channel
		case channel := <-getThroughput:
			channel <- [4]float64{OldCommittedThroughput, OldRequestedThroughput, OldCommittedBlockOccupation, OldBlockCommittedThroughput}
		case <-chanSaveTicker:
			go func() { handler.MetricSave.Save() }()
		case channel := <-toKillTicker:
			ticker.Stop()
			channel <- true
			return
		}
	}
}

func (handler *MetricHandler) loopSumTx(addCommittedTx <-chan uint, addReceivedTx <-chan struct{}, addCommittedBlock <-chan struct{}, getSumTx <-chan chan<- [3]uint, toKill <-chan chan<- bool) {
	var SumOfTxCommitted uint
	var SumOfTxReceived uint
	var SumOfBlockCommitted uint
	for {
		select {
		case n := <-addCommittedTx:
			SumOfTxCommitted = SumOfTxCommitted + n
		case <-addReceivedTx:
			SumOfTxReceived++
		case <-addCommittedBlock:
			SumOfBlockCommitted++
		case channel := <-getSumTx:
			channel <- [3]uint{SumOfTxCommitted, SumOfTxReceived, SumOfBlockCommitted}
			SumOfTxCommitted = 0
			SumOfTxReceived = 0
			SumOfBlockCommitted = 0
		case channel := <-toKill:
			channel <- true
			return
		}
	}
}

// getSumOfTx return the sum of committed Tx and the sum of received Tx
func (handler *MetricHandler) getSumOfTx() (uint, uint, uint) {
	channel := make(chan [3]uint)
	handler.getSumTx <- channel
	sum := <-channel
	close(channel)
	return sum[0], sum[1], sum[2]
}

// GetThroughputs return the committed throughput and the received throughput
func (handler *MetricHandler) GetThroughputs() (float64, float64, float64, float64) {
	channel := make(chan [4]float64)
	handler.throughputChan <- channel
	throughput := <-channel
	close(channel)
	return throughput[0], throughput[1], throughput[2], throughput[3]
}

func (handler *MetricHandler) AddOneCommittedTx() {
	handler.AddNCommittedTx(1)
}

func (handler *MetricHandler) AddNCommittedTx(n int) {
	handler.addComittedTx <- uint(n)
}

func (handler *MetricHandler) AddOneReceivedTx() {
	handler.addReceivedTx <- struct{}{}
}

func (handler *MetricHandler) AddOneCommittedBlock() {
	handler.addComittedBlock <- struct{}{}
}

func (handler *MetricHandler) Close() {
	handler.MetricSave.Stop()
	handler.MetricSave.Save()
	queryToKillTicker := make(chan bool)
	handler.toKillTicker <- queryToKillTicker
	queryToKill := make(chan bool)
	handler.toKill <- queryToKill
	<-queryToKillTicker
	<-queryToKill
	//close(handler.addComittedTx)
	//close(handler.addReceivedTx)
	close(handler.getSumTx)
	close(handler.throughputChan)
	close(handler.toKill)
	close(handler.toKillTicker)
	close(queryToKill)
	close(queryToKillTicker)
	if handler.metricServer != nil {
		err := handler.metricServer.Close()
		check(err)
	}
}

func (handler MetricHandler) GetMetrics() Metrics {
	block := handler.chain.GetLastBLoc()
	var blocktime int64
	NbOfTxInLastBlock := len(block.Transactions)
	var derivatifThrough float64
	var startBlock int64
	if block.SequenceNb > 1 {
		//previousBlock := handler.chain.GetBlock(block.SequenceNb - 1)
		previousBlock := handler.chain.GetSecLastBLoc()
		startBlock = previousBlock.Timestamp
	}
	var minTxTimestamp int64
	for _, tx := range block.Transactions {
		if minTxTimestamp == 0 || tx.TransaCore.Timestamp < minTxTimestamp {
			minTxTimestamp = tx.TransaCore.Timestamp
		}
	}
	startBlock = max64(startBlock, minTxTimestamp)
	blocktime = block.Timestamp - startBlock
	derivatifThrough = float64(NbOfTxInLastBlock) / float64(blocktime) * math.Pow(10, 9)
	committedThrougput, receivedThrougput, blockOccupation, blockThroughput := handler.GetThroughputs()
	return Metrics{
		Blocktime:           blocktime,
		NbOfTxInLastBlock:   NbOfTxInLastBlock,
		LastBlockTime:       block.Timestamp,
		CommittedThroughput: committedThrougput,
		ReceivedThroughput:  receivedThrougput,
		NumberOfValidator:   handler.validator.GetNumberOfValidator(),
		DerivThroughput:     derivatifThrough,
		BlockOccupation:     blockOccupation,
		BlockThroughput:     blockThroughput,
	}
}

type metricStack interface {
	update()
	Save()
	Stop()
}

type metricStacked struct {
	Metrics
	Timestamp int64 `json:"timestamp"`
}

type metricStackSave struct {
	list     []metricStacked
	savefile string
	handler  *MetricHandler
	mutex    sync.RWMutex
	wait     sync.WaitGroup
	stop     bool
}

func newMetricStackSave(handler *MetricHandler, savefile string) *metricStackSave {
	return &metricStackSave{handler: handler, savefile: savefile}
}

func (stack *metricStackSave) update() {
	if !stack.stop {
		stack.wait.Add(1)
		go func() {
			log.Debug().Msg("ask for metric")
			metric := stack.handler.GetMetrics()
			log.Debug().Msg("Get the metrics")
			stack.mutex.Lock()
			stack.list = append(stack.list, metricStacked{metric, time.Now().UnixNano()})
			log.Debug().Msgf("Number of Metric %d", len(stack.list))
			stack.mutex.Unlock()
			stack.wait.Done()
			log.Debug().Msg("End of Metric add")
		}()
	}
}

func (metric *metricStackSave) Save() {
	log.Debug().Msg("Save ask")
	var err error
	var metricFile *os.File
	try := config.CreateTest
	for try > 0 && metricFile == nil {
		metricFile, err = os.Create(metric.savefile)
		try--
	}
	if try == 0 && err != nil {
		log.Error().Msgf("Error opening the file %s : %s", metric.savefile, err.Error())
	}
	metric.mutex.RLock()
	log.Debug().Msgf("Size of the save : %d", len(metric.list))
	bytes, err := json.MarshalIndent(metric.list, "", "  ")
	check(err)
	metric.mutex.RUnlock()
	_, err = metricFile.WriteAt(bytes, 0)
	check(err)
	err = metricFile.Close()
	check(err)
	log.Debug().Msg("Save done")
}

func (stack *metricStackSave) Stop() {
	stack.stop = true
	stack.wait.Wait()
}

type whiteMetricSatck struct{}

func (w whiteMetricSatck) update() {}

func (w whiteMetricSatck) Save() {}

func (w whiteMetricSatck) Stop() {}
