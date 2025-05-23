package Blockchain

import (
	"github.com/bits-and-blooms/bloom/v3"
	"github.com/dghubble/trie"
	"github.com/rs/zerolog/log"
)

type SecurityExecutedTx uint8

const (
	NoExecTxSec SecurityExecutedTx = iota
	MapExecTxSec
	BloomExecTxSec
	RadixExecTxSec
)

type executedTxSec interface {
	Exist([]byte) bool
	Add([]byte)
}

func (security SecurityExecutedTx) newSecurityTxSec() executedTxSec {
	switch security {
	case NoExecTxSec:
		return newNothingExecTxSec()
	case MapExecTxSec:
		return newMapExecTxSec()
	case BloomExecTxSec:
		return newBloomExecTxSex()
	case RadixExecTxSec:
		return newTriesExecTxSec()
	default:
		log.Error().Msgf("Unknown Security %d", security)
		return newNothingExecTxSec()
	}
}

type mapExecTxSec struct {
	theMap map[string]struct{}
}

func newMapExecTxSec() *mapExecTxSec {
	return &mapExecTxSec{make(map[string]struct{})}
}

func (txSec mapExecTxSec) Exist(hash []byte) bool {
	_, ok := txSec.theMap[string(hash)]
	return ok
}

func (txSec *mapExecTxSec) Add(hash []byte) {
	txSec.theMap[string(hash)] = struct{}{}
}

type nothingExecTxSec struct {
}

func (txSec nothingExecTxSec) Exist(_ []byte) bool {
	return false
}

func (txSec *nothingExecTxSec) Add(_ []byte) {
}

func newNothingExecTxSec() *nothingExecTxSec {
	return &nothingExecTxSec{}
}

type bloomExecTxSec struct {
	bloom *bloom.BloomFilter
}

func newBloomExecTxSex() *bloomExecTxSec {
	return &bloomExecTxSec{bloom.NewWithEstimates(1000000, 0.001)}
}

func (txSec bloomExecTxSec) Exist(hash []byte) bool {
	return txSec.bloom.Test(hash)
}

func (txSec *bloomExecTxSec) Add(hash []byte) {
	txSec.bloom.Add(hash)
}

type triesExecTxSec struct {
	trie *trie.PathTrie
}

func newTriesExecTxSec() *triesExecTxSec {
	return &triesExecTxSec{trie.NewPathTrie()}
}

func (txSec triesExecTxSec) Exist(hash []byte) bool {
	return txSec.trie.Get(string(hash)) != nil
}

func (txSec triesExecTxSec) Add(hash []byte) {
	txSec.trie.Put(string(hash), struct{}{})
}
