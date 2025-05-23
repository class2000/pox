package Blockchain

import (
	"crypto/ed25519"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type Validators struct {
	SelectionType        SelectionValidatorType
	nodeList             []ed25519.PublicKey
	lastChange           int64
	isValidatorMap       map[string]int
	isActiveValidatorMap map[string]struct{}
	activeLock           sync.RWMutex
}

func (valid *Validators) IsSizeValid(newSize int) bool {
	return newSize >= 4 && newSize <= len(valid.nodeList)
}

func (valid *Validators) GetNumberOfValidator() int {
	valid.activeLock.RLock()
	n := len(valid.isActiveValidatorMap)
	valid.activeLock.RUnlock()
	return n
}

func (valid *Validators) SetNumberOfNode(newSize int) bool {
	if valid.IsSizeValid(newSize) {

		if valid.GetNumberOfValidator() != newSize {
			newSubList := valid.nodeList[:min(newSize, len(valid.nodeList))]
			valid.SetNewListOfValidator(newSubList)
		}
		valid.lastChange = time.Now().UnixNano()
		return true
	}
	return false
}

func (valid *Validators) SetNewListOfValidator(newList []ed25519.PublicKey) bool {

	isValid := valid.CheckIfValidatorsAreNodes(newList)
	if !isValid {
		return false
	}

	valid.activeLock.Lock()
	valid.isActiveValidatorMap = map[string]struct{}{}
	for _, publicKey := range newList {
		valid.isActiveValidatorMap[string(publicKey)] = struct{}{}
	}
	valid.activeLock.Unlock()
	valid.lastChange = time.Now().UnixNano()
	return true
}

// CheckIfValidatorsAreNodes assert that the provided list of potential validators are known nodes
func (valid *Validators) CheckIfValidatorsAreNodes(newList []ed25519.PublicKey) bool {
	for _, publicKey := range newList {
		if !valid.IsValidator(publicKey) {
			return false
		}
	}
	return true
}

// IsOlderThan return true if more than timestamp nanosecond have been passed after a new block
func (valid *Validators) IsOlderThan(timestamp time.Duration) bool {
	return time.Now().UnixNano()-valid.lastChange > timestamp.Nanoseconds()
}

func (valid *Validators) GetIndexOfValidator(key ed25519.PublicKey) int {
	id, present := valid.isValidatorMap[string(key)]
	if present {
		return id
	}
	return -1
}

func (valid *Validators) GetValidatorOfIndex(i int) ed25519.PublicKey {
	if i < 0 || i >= len(valid.nodeList) {
		log.Error().Int("requestedIndex", i).Int("nodeListSize", len(valid.nodeList)).Msg("GetValidatorOfIndex: Index out of bounds for nodeList")
		return nil // Or handle more gracefully, but returning nil is better than panic
	}
	return valid.nodeList[i]
}

func (valid *Validators) GenerateAddresses(numberOfValidators int) {
	valid.isValidatorMap = map[string]int{}
	valid.activeLock.Lock()
	valid.isActiveValidatorMap = map[string]struct{}{}
	for id := 0; id < numberOfValidators; id++ {
		newPubKey := GenPubKeyOfId(id)
		valid.nodeList = append(valid.nodeList, newPubKey)
		valid.isValidatorMap[string(newPubKey)] = id
		valid.isActiveValidatorMap[string(newPubKey)] = struct{}{}
	}
	valid.activeLock.Unlock()
	valid.SetNumberOfNode(numberOfValidators)
}

// GenPubKeyOfId generate and returns the public key for the node of given id
func GenPubKeyOfId(id int) ed25519.PublicKey {
	return NewWallet(fmt.Sprintf("NODE%d", id)).PublicKey()
}

// IsActiveValidator Return if the key is an Active validator
func (valid *Validators) IsActiveValidator(key ed25519.PublicKey) bool {
	valid.activeLock.RLock()
	_, present := valid.isActiveValidatorMap[string(key)]
	valid.activeLock.RUnlock()
	return present
}

// IsValidator Return if the key is an Active or Inactive validator
func (valid *Validators) IsValidator(key ed25519.PublicKey) bool {
	_, present := valid.isValidatorMap[string(key)]
	return present
}

// GetValidatorList return the list of actual validator, sorted by their original index
func (valid *Validators) GetValidatorList() []ed25519.PublicKey {
	listIndex := valid.getValidatorIndexList()
	returnList := make([]ed25519.PublicKey, valid.GetNumberOfValidator())
	for j, index := range listIndex {
		returnList[j] = valid.nodeList[index]
	}
	return returnList
}

// GetValidatorList return the list of actual validator index, sorted by their original index
func (valid *Validators) getValidatorIndexList() []int {
	listIndex := make([]int, valid.GetNumberOfValidator())
	var i int
	valid.activeLock.RLock()
	for key, _ := range valid.isActiveValidatorMap {
		validatorId := valid.isValidatorMap[key]
		listIndex[i] = validatorId
		i++
	}
	valid.activeLock.RUnlock()
	sort.Ints(listIndex)
	return listIndex
}

func (valid *Validators) GetNodeList() []ed25519.PublicKey {
	return valid.nodeList
}

func (valid *Validators) getIndexOfValidators() []int {
	nbVal := valid.GetNumberOfValidator()
	listValidator := make([]int, nbVal)
	var i int
	valid.activeLock.RLock()
	for key, _ := range valid.isActiveValidatorMap {
		listValidator[i] = valid.isValidatorMap[key]
		i++
	}
	valid.activeLock.RUnlock()
	return listValidator
}

// GetActiveValidatorIndexOfValue return the original index and the associated public key, of the index th active validator, sorted by index
func (valid *Validators) GetActiveValidatorIndexOfValue(index int) (int, ed25519.PublicKey) {
	listPublTemp := valid.GetValidatorList()
	chosen := listPublTemp[index]
	chosenIndex := valid.GetIndexOfValidator(chosen)
	return chosenIndex, chosen
}

func (valid *Validators) logAllValidator() {
	listIndex := valid.getValidatorIndexList()
	str := ""
	for _, index := range listIndex {
		str = str + "," + strconv.Itoa(index)
	}
	log.Debug().Msgf("New list of validator :%s", str)
}
