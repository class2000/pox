package Blockchain

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"os"
	"pbftnode/source/config"
	"sync"
)

const blocFragmentMax int = 1 << 12

type Saver struct {
	saveFile      string
	waitWrite     sync.WaitGroup
	askWrite      chan struct{}
	askMultiWrite chan struct{}
	consensus     writeChainInterf
	MultiPrev     int
}

func NewSaver(saveFile string, consensus writeChainInterf) *Saver {
	saver := &Saver{
		saveFile:      saveFile,
		waitWrite:     sync.WaitGroup{},
		askWrite:      make(chan struct{}, 1),
		askMultiWrite: make(chan struct{}, 1),
		consensus:     consensus,
	}
	go saver.saverLoop()
	return saver
}

func openChainFile(saveFile string) (chainFile *os.File) {
	var err error
	try := config.CreateTest
	for try > 0 && chainFile == nil {
		chainFile, err = os.Create(saveFile)
		try--
	}
	if try == 0 && err != nil {
		log.Error().Msgf("Error opening the file %s : %s", saveFile, err.Error())
		return nil
	}
	return chainFile
}

func (saver *Saver) AskToSave() {
	if saver.saveFile == "" {
		return
	}
	saver.waitWrite.Add(1)
	select {
	case saver.askWrite <- struct{}{}:
	default:
		saver.waitWrite.Done()
	}
}

func (saver *Saver) AskMultiSave() {
	if saver.saveFile == "" {
		return
	}
	saver.waitWrite.Add(1)
	select {
	case saver.askMultiWrite <- struct{}{}:
	default:
		saver.waitWrite.Done()
	}
}

func (saver *Saver) saverLoop() {
	for {
		select {
		case <-saver.askWrite:
			saver.writeOneFile()
		case <-saver.askMultiWrite:
			saver.writeMultipleFile()
		}
	}
}

func (saver *Saver) writeOneFile() {
	attemptASaveForAChain(saver.saveFile, saver.consensus)
	nbTest := config.CreateTest
	for nbTest > 0 && !checkIfNotNulFile(saver.saveFile) {
		attemptASaveForAChain(saver.saveFile, saver.consensus)
		nbTest--
	}
	if nbTest == 0 && !checkIfNotNulFile(saver.saveFile) {
		log.Error().Msgf("The write of the file %s failed", saver.saveFile)
	}
	saver.waitWrite.Done()
}

func attemptASaveForAChain(saveFile string, consensus writeChainInterf) {
	chainFile := openChainFile(saveFile)
	if chainFile != nil {
		var err error
		err = writeChain(consensus, chainFile)
		if err != nil {
			log.Error().Msg(err.Error())
		} else {
			err = chainFile.Close()
		}
	}
}

func (saver *Saver) writeMultipleFile() {
	saver.MultiPrev = writeMultipleFile(saver.consensus, saver.saveFile, saver.MultiPrev)
}

func writeMultipleFile(consensus writeChainInterf, saveFile string, start int) int {
	var chanPoint chan *[]*Block = make(chan *[]*Block)
	go consensus.GetBlockchain().GetFragmentBlocks(chanPoint)
	var N int
	var pointer *[]*Block
	pointer = <-chanPoint
	for N+1 < start {
		pointer = <-chanPoint
		N++
	}
	for pointer != nil {
		var saveFileBro string = fmt.Sprintf("%s_%d", saveFile, N)
		var nbTest = config.CreateTest
		if len(*pointer) == 0 {
			log.Error().Msgf("The pointer is nul size")
		}

		attemptASave(saveFileBro, pointer)
		for nbTest > 0 && !checkIfNotNulFile(saveFileBro) {
			attemptASave(saveFileBro, pointer)
			nbTest--
		}
		if nbTest == 0 && !checkIfNotNulFile(saveFileBro) {
			log.Error().Msgf("The write of the file %s failed", saveFileBro)
		}

		pointer = <-chanPoint
		N++
	}
	log.Info().Msgf("Wrote %d file", N)
	close(chanPoint)
	return N
}

func attemptASave(saveFileBro string, pointer *[]*Block) {
	var err error
	var n int
	chainFile := openChainFile(saveFileBro)
	check(err)
	byted, err := json.MarshalIndent(*getLisOfBlocFromListOfPointer(pointer), "", "  ")
	check(err)
	n, err = chainFile.Write(byted)
	log.Info().Msgf("wrote the file %s with %d bytes instead of %d, for %d bloc", saveFileBro, n, len(byted), len(*pointer))
	if err != nil {
		log.Error().Msgf("Write %d byte instead of %d; error %s", n, len(*pointer), err)
	}
	err = chainFile.Sync()
	check(err)
	err = chainFile.Close()
	check(err)
}

func getLisOfBlocFromListOfPointer(pointer *[]*Block) *[]Block {
	copie := make([]Block, len(*pointer))
	for i, blockPointer := range *pointer {
		copie[i] = *blockPointer
	}
	return &copie
}

// checkIfNotNulFile return true if th file don't exist or if it's a null file
func checkIfNotNulFile(saveFileBro string) bool {
	fi, err := os.Stat(saveFileBro)
	if err != nil {
		return false
	}
	// get the size
	size := fi.Size()
	if size == 0 {
		log.Warn().Msgf("The file %s is empty", saveFileBro)
	}
	return size != 0
}

func writeChain(consensus writeChainInterf, chainFile *os.File) error {
	n, err := chainFile.WriteAt(consensus.GetBlockchain().GetChainJson(), 0)
	if n == 0 {
		return errors.New("Nothing was written in the save file")
	}
	if err == nil {
		err = chainFile.Sync()
	}
	if err != nil {
		return fmt.Errorf("Error writing file : %s", err.Error())
	}
	return nil
}

func (saver *Saver) Wait() {
	saver.waitWrite.Wait()
}
