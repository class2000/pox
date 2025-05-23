package Blockchain

import (
	"crypto/ed25519"
	"github.com/rs/zerolog/log"
	"math"
	"sort"
)

type SelectionValidatorType uint8

const (
	PivotSelection SelectionValidatorType = iota
	RandomSelection
	CentricSelection
)

var selectionMap = map[string]SelectionValidatorType{
	"Pivot":   PivotSelection,
	"Random":  RandomSelection,
	"Centric": CentricSelection,
}

func ParseSelectionValidatorType(str string) SelectionValidatorType {
	c, ok := selectionMap[str]
	if !ok {
		log.Error().Msgf("The string %s is not a SelectionType, set to Pivot", str)
		return PivotSelection
	}
	return c
}

type ArgsSelector struct {
	MatAdj [][]int
	Gamma  float64
}

func (selectionType SelectionValidatorType) CreateValidatorSelector(args ArgsSelector) ValidatorSelector {
	switch selectionType {
	case PivotSelection:
		return PivotSelector{}
	case RandomSelection:
		return RandomSelector{}
	case CentricSelection:
		return newCentricSelector(args.MatAdj, args.Gamma)
	default:
		log.Error().Msgf("The type %d is unknown", selectionType)
		return PivotSelector{}
	}
}

type ValidatorSelectorUpdateInter interface {
	// Update updates the parameter of the selection. The parameter listIndexValidator is the list of index of validators
	Update(listIndexValidator []int)
}

type ValidatorSelector interface {
	ValidatorSelectorUpdateInter
	GenerateNewValidatorListProposition(validators ValidatorGetterInterForSelector, newSize int, seed int64) []ed25519.PublicKey
	GetType() SelectionValidatorType
}

type PivotSelector struct{}

func (selector PivotSelector) Update(listIndexValidator []int) {}

func (selector PivotSelector) GenerateNewValidatorListProposition(validators ValidatorGetterInterForSelector, newSize int, seed int64) []ed25519.PublicKey {
	validatorNodeL := validators.GetNodeList()
	newSubListTemp := validatorNodeL[:min(newSize, len(validatorNodeL))]
	newSubList := make([]ed25519.PublicKey, newSize)
	copy(newSubList, newSubListTemp)
	return newSubList
}

func (selector PivotSelector) GetType() SelectionValidatorType {
	return PivotSelection
}

type RandomSelector struct{}

func (selector RandomSelector) Update(listIndexValidator []int) {}

func (selector RandomSelector) GenerateNewValidatorListProposition(validators ValidatorGetterInterForSelector, newSize int, seed int64) []ed25519.PublicKey {
	validatorNodeL := validators.GetNodeList()
	newSubListIndex := generateRandomList(newSize, len(validatorNodeL), seed)
	newSubList := make([]ed25519.PublicKey, newSize)
	for i, index := range newSubListIndex {
		newSubList[i] = validatorNodeL[index]
	}
	return newSubList
}

func (selector RandomSelector) GetType() SelectionValidatorType {
	return RandomSelection
}

type CentricSelector struct {
	matAdj       [][]int
	matrixWeight []float64
	gamma        float64
}

func newCentricSelector(matAdj [][]int, gamma float64) *CentricSelector {
	if matAdj == nil {
		log.Fatal().Msg("The matrix is not provided")
	}
	nbNode := len(matAdj)
	return &CentricSelector{
		matAdj,
		make([]float64, nbNode),
		gamma,
	}
}

func (selector *CentricSelector) GenerateNewValidatorListProposition(validators ValidatorGetterInterForSelector, newSize int, seed int64) []ed25519.PublicKey {
	nbNode := len(selector.matAdj)
	listValId := validators.getIndexOfValidators()
	center := selector.selectCenter(listValId)
	listDistanceComp := selector.computeCompDistanceFromCenter(center)
	listIndex := arrange(nbNode)

	sort.Slice(listIndex, func(i, j int) bool {
		return listDistanceComp[listIndex[i]] < listDistanceComp[listIndex[j]]
	})

	lisTSelectedValidatorIndex := listIndex[:newSize]
	newList := make([]ed25519.PublicKey, newSize)
	nodeList := validators.GetNodeList()
	for i, index := range lisTSelectedValidatorIndex {
		newList[i] = nodeList[index]
	}
	return newList
}

func (selector *CentricSelector) computeCompDistanceFromCenter(center int) []float64 {
	nbNode := len(selector.matAdj)
	listDistanceComp := make([]float64, nbNode)
	for i := 0; i < nbNode; i++ {
		listDistanceComp[i] = float64(selector.matAdj[center][i]) * math.Exp(selector.matrixWeight[i]/selector.gamma)
	}
	return listDistanceComp
}

func (selector *CentricSelector) GetType() SelectionValidatorType {
	return CentricSelection
}

func (selector *CentricSelector) Update(listIndexValidator []int) {
	nb_val := len(listIndexValidator)
	nb_node := len(selector.matAdj)
	listIfVal := changeListValToListBool(listIndexValidator, nb_node)
	weightDiffVal := 1 / float64(nb_val)
	weightDiffNVal := 1 / float64(nb_node-nb_val)

	for i := 0; i < nb_node; i++ {
		if listIfVal[i] {
			selector.matrixWeight[i] = selector.matrixWeight[i] + weightDiffVal
		} else {
			selector.matrixWeight[i] = selector.matrixWeight[i] - weightDiffNVal
		}
	}
}

func changeListValToListBool(listIndexValidator []int, nb_node int) []bool {
	listIfVal := make([]bool, nb_node)
	for _, index := range listIndexValidator {
		listIfVal[index] = true
	}
	return listIfVal
}

func (selector *CentricSelector) selectCenter(listIndexValidator []int) int {
	nbNode := len(selector.matAdj)
	listIfVal := changeListValToListBool(listIndexValidator, nbNode)
	minIdVal := 0
	minWeight := math.Inf(+1)
	for i := 0; i < nbNode; i++ {
		var weightF float64
		if listIfVal[i] {
			var weight int
			for j := 0; j < nbNode; j++ {
				if listIfVal[j] {
					weight = weight + selector.matAdj[i][j]
				}
			}
			weightF = float64(weight)
		} else {
			weightF = math.Inf(+1)
		}
		if weightF < minWeight {
			minIdVal = i
			minWeight = weightF
		}
	}
	return minIdVal
}
