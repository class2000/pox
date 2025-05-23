package ControlLoop

import (
	"encoding/csv"
	"fmt"
	"github.com/rs/zerolog/log"
	"os"
	"pbftnode/source/config"
	"sort"
	"strconv"
)

type ModelValInterf interface {
	importCSV(csvlines [][]string)
	GetIdealNbVal(requestedThroughput float64, committedThroughput float64, blockThroughput float64, nbVal int) int
}
type ModelNul struct{}

func (model ModelNul) importCSV([][]string) {
	log.Info().Msg("It's a Nul Model")
}

func (model ModelNul) GetIdealNbVal(float64, float64, float64, int) int {
	return 0
}

type ModelVal2D struct {
	model []sample
}

type sample struct {
	nbValid    int
	througputs float64
}

func NewModelVal(modelFile string) ModelValInterf {
	if modelFile == "" {
		return ModelNul{}
	}
	csvFile, err := os.Open(modelFile)
	if err != nil {
		log.Fatal().Msgf("Cannot open the CSV file, %s", err.Error())
	}
	log.Info().Msg("Successfully Opened CSV file")

	csvlines, err := csv.NewReader(csvFile).ReadAll()
	if err != nil {
		log.Fatal().Msgf("Cannot read the CSV file, %s", err.Error())
	}
	var model ModelValInterf
	if csvlines[0][1] == "lag" || (len(csvlines[0]) > 2 && csvlines[0][2] == "lag") {
		model = &ModelVal3D{}
	} else {
		model = &ModelVal2D{}
	}

	model.importCSV(csvlines)

	err = csvFile.Close()
	if err != nil {
		log.Error().Msgf("Error closing the CSV file : %s", err.Error())
	}

	return model
}

func (model *ModelVal2D) importCSV(csvlines [][]string) {
	index := false
	if csvlines[0][0] == "" {
		index = true
	}

	for i, csvline := range csvlines {
		if i > 0 {
			model.addLineColumn(index, csvline)
		}
	}
	model.postImport()
	log.Info().Msg("2D Model Successfully Imported")
}

func (model *ModelVal2D) addLineColumn(index bool, csvline []string) {
	deca := 0
	if index {
		deca = 1
	}
	nbValid, errVal := strconv.Atoi(csvline[deca])
	throughput, errThrougput := strconv.ParseFloat(csvline[1+deca], 64)
	if errVal != nil || errThrougput != nil {
		log.Fatal().Msgf("One of the value cannot be cast : %s or %s", csvline[deca], csvline[1+deca])
	}
	model.model = append(model.model, sample{
		nbValid:    nbValid,
		througputs: throughput,
	})
}

func (model *ModelVal2D) postImport() {
	lastIndex := len(model.model) - 1
	throughput := 0.
	for index := lastIndex; index >= 0; index-- {
		if model.model[index].througputs > throughput {
			throughput = model.model[index].througputs
		} else {
			model.model[index].nbValid = 0
		}
	}
	tempList := []sample{}
	for _, sampleElem := range model.model {
		if sampleElem.nbValid != 0 {
			tempList = append(tempList, sampleElem)
		}
	}
	model.model = tempList
	log.Debug().Msgf("Import Successful. Size of the model : %d", len(model.model))
}

func (model ModelVal2D) GetIdealNbVal(requestedThroughput float64, committedThroughput float64, blockThroughput float64, nbVal int) int {
	lastIndex := len(model.model) - 1
	switch {
	case len(model.model) == 0:
		return 0
	case requestedThroughput <= model.model[lastIndex].througputs:
		return model.model[lastIndex].nbValid
	case requestedThroughput >= model.model[0].througputs:
		return model.model[0].nbValid
	}
	var debut int
	{
		fin := lastIndex
		for debut+1 < fin {
			middle := (debut + fin) / 2
			if model.model[middle].througputs > requestedThroughput {
				debut = middle
			} else {
				fin = middle
			}
		}
	}
	return model.model[debut].nbValid
}

type ModelVal3D struct {
	// de la forme throughput = map[nbVal][lag]
	mapThrougputs [][]float64
	listLag       sortedIntList
	listVal       sortedIntList
}

func (model *ModelVal3D) importCSV(csvlines [][]string) {
	deca := 0
	if csvlines[0][0] == "" {
		deca = 1
	}
	// Prepare the mesh
	for i, csvline := range csvlines {
		if i > 0 {
			nbValid, errVal := strconv.Atoi(csvline[deca])
			model.listVal = model.listVal.Insert(nbValid)
			lag, errLag := strconv.Atoi(csvline[1+deca])
			model.listLag = model.listLag.Insert(lag)
			if errVal != nil || errLag != nil {
				log.Fatal().Msgf("One of the value cannot be cast : %s or %s", csvline[deca], csvline[1+deca])
			}
		}
	}
	// Create the mesh
	model.mapThrougputs = make([][]float64, len(model.listVal))
	for i := range model.mapThrougputs {
		model.mapThrougputs[i] = make([]float64, len(model.listLag))
	}
	// Fill the mesh
	for i, csvline := range csvlines {
		if i > 0 {
			nbValid, _ := strconv.Atoi(csvline[deca])
			lag, _ := strconv.Atoi(csvline[1+deca])
			througput, errThroughput := strconv.ParseFloat(csvline[2+deca], 64)
			if errThroughput != nil {
				log.Fatal().Msgf("The value cannot be cast : %s", csvline[2+deca])
			}
			model.mapThrougputs[model.listVal.getIndex(nbValid)][model.listLag.getIndex(lag)] = througput
		}
	}

	// Optimize it
	model.postImport()
	log.Info().Msg("3D Model Successfully Imported")
}

func (modele ModelVal3D) GetIdealNbVal(requestedThroughput float64, committedThroughput float64, blockThroughput float64, nbVal int) int {
	nbValIndex := modele.getNbValIndex(nbVal)
	latencyIndex := modele.getLatencyIndexFrom(nbValIndex, blockThroughput*config.BlockSize)
	idealNbvalIndex := modele.getNbValIndexFrom(latencyIndex, requestedThroughput)
	return modele.listVal[idealNbvalIndex]
}

func (model ModelVal3D) getNbValIndex(nbVal int) (index int) {
	index = sort.SearchInts(model.listVal, nbVal)
	if index > 0 && model.listVal[index-1] == nbVal {
		index--
	}
	return index
}

func (model ModelVal3D) getLatencylIndex(lag int) (index int) {
	index = sort.SearchInts(model.listLag, lag)
	if index > 0 && model.listLag[index-1] == lag {
		index--
	}
	return index
}

func (model ModelVal3D) getLatencyIndexFrom(nbValIndex int, throughputCommitted float64) (latencyIndex int) {
	latencyIndex = sort.Search(len(model.listLag), func(i int) bool {
		return model.mapThrougputs[nbValIndex][i] < throughputCommitted
	})
	if latencyIndex > 0 {
		latencyIndex--
	}
	for latencyIndex+1 < len(model.listLag) &&
		model.mapThrougputs[nbValIndex][latencyIndex+1] == model.mapThrougputs[nbValIndex][latencyIndex] {
		latencyIndex++
	}
	return latencyIndex
}

func (model ModelVal3D) getNbValIndexFrom(latencyIndex int, throughput float64) (nbValIndex int) {
	nbValIndex = sort.Search(len(model.listVal), func(i int) bool {
		return model.mapThrougputs[i][latencyIndex] < throughput
	})
	if nbValIndex > 0 {
		nbValIndex--
	}
	for nbValIndex+1 < len(model.listVal) &&
		model.mapThrougputs[nbValIndex+1][latencyIndex] == model.mapThrougputs[nbValIndex][latencyIndex] {
		nbValIndex++
	}
	return nbValIndex
}

type sortedIntList []int

func (list sortedIntList) Insert(s int) sortedIntList {
	i := sort.SearchInts(list, s)
	if i == len(list) {
		list = append(list, s)
	} else if list[i] != s {
		list = append(list, 0)
		copy(list[i+1:], list[i:])
		list[i] = s
	}
	return list
}

func (list sortedIntList) getIndex(x int) int {
	return sort.SearchInts(list, x)
}

func (model ModelVal3D) print() {
	fmt.Printf(" \t: ")
	for _, lag := range model.listLag {
		fmt.Printf("\t\t%d\t", lag)
	}
	fmt.Printf("\n")
	for i, throughputList := range model.mapThrougputs {
		fmt.Printf("%d\t:", model.listVal[i])
		for _, throughput := range throughputList {
			fmt.Printf("\t%f", throughput)
		}
		fmt.Printf("\n")
	}
}

func (model *ModelVal3D) postImport() {
	var bubble bool = true
	for bubble {
		bubble = false

		// nbValIndex opt
		for lagIndex := 0; lagIndex < len(model.listLag); lagIndex++ {
			lastIndex := len(model.listVal) - 1
			var throughput float64 = 0
			for nbValIndex := lastIndex; nbValIndex >= 0; nbValIndex-- {
				if model.mapThrougputs[nbValIndex][lagIndex] >= throughput {
					throughput = model.mapThrougputs[nbValIndex][lagIndex]
				} else {
					model.mapThrougputs[nbValIndex][lagIndex] = throughput
					bubble = true
				}
			}
		}

		// lag opt
		for nbValIndex := 0; nbValIndex < len(model.listVal); nbValIndex++ {
			lastIndex := len(model.listLag) - 1
			var throughput float64 = 0
			for lagIndex := lastIndex; lagIndex >= 0; lagIndex-- {
				if model.mapThrougputs[nbValIndex][lagIndex] >= throughput {
					throughput = model.mapThrougputs[nbValIndex][lagIndex]
				} else {
					model.mapThrougputs[nbValIndex][lagIndex] = throughput
					bubble = true
				}
			}
		}

	}
}
