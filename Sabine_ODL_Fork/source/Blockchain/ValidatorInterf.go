package Blockchain

import (
	"crypto/ed25519"
	"time"
)

type ValidatorInterf interface {
	ValidatorGetterInterf
	ValidatorGetterInterForSelector
	GenerateAddresses(numberOfValidators int)
	IsSizeValid(newSize int) bool
	IsActiveValidator(key ed25519.PublicKey) bool
	IsValidator(key ed25519.PublicKey) bool
	SetNumberOfNode(int) bool
	SetNewListOfValidator(newList []ed25519.PublicKey) bool
	// CheckIfValidatorsAreNodes assert that the provided list of potential validators are known nodes
	CheckIfValidatorsAreNodes(newList []ed25519.PublicKey) bool
	GetIndexOfValidator(key ed25519.PublicKey) int
	GetValidatorOfIndex(int) ed25519.PublicKey
	// GetActiveValidatorIndexOfValue return the original index and the associated public key, of the index th active validator, sorted by index
	GetActiveValidatorIndexOfValue(index int) (int, ed25519.PublicKey)
	logAllValidator()
}

type ValidatorGetterInterForSelector interface {
	GetNodeList() []ed25519.PublicKey
	getIndexOfValidators() []int
}

type ValidatorGetterInterf interface {
	GetNumberOfValidator() int
	IsOlderThan(timestamp time.Duration) bool
}

type MetricGetterInterf interface {
	ValidatorGetterInterf
	GetIncQueueSize() int
}
