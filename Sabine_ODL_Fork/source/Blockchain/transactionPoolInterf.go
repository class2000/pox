package Blockchain

import (
	"github.com/rs/zerolog/log"
	"sort"
)

type TransactionPoolInterf interface {
	ExistingTransaction(transaction Transaction) bool
	PoolSize() int
	IsEmpty() bool
	Clear()
	AddTransaction(transaction Transaction) bool
	RemoveTransaction(transaction Transaction)
	GetTxForBloc() []Transaction
	SetMetricHandler(handler MetricInterf)
	GetStopingChan() <-chan bool
}

type sortedStruct interface {
	//insert Insert an element in the list
	insert(hash string)
	//getFirstsElem returns the nbElem first elements of the list
	getFirstsElem(nbElem int) []string
	//getElemNumber returns the nbElem nth element of the list
	getElemNumber(nbElem int) string
	remove(hash string)
	setMap(*map[string]Transaction)
}

type sortedList struct {
	listHash []string
	mapTx    *map[string]Transaction
}

func newSortedList(mapTx *map[string]Transaction) *sortedList {
	return &sortedList{mapTx: mapTx}
}

func (list *sortedList) insert(hash string) {
	i := sort.Search(len(list.listHash), func(i int) bool {
		return (*list.mapTx)[list.listHash[i]].TransaCore.Timestamp > (*list.mapTx)[hash].TransaCore.Timestamp
	})
	temp := append(list.listHash, "")
	copy(temp[i+1:], temp[i:])
	temp[i] = hash
	list.listHash = temp
}

func (list sortedList) getFirstsElem(nbElem int) []string {
	nbElem = min(nbElem, len(list.listHash))
	temp := list.listHash[:nbElem]
	return temp
}

func (list sortedList) getElemNumber(nbElem int) string {
	nbElem = min(nbElem, len(list.listHash))
	return list.listHash[nbElem]
}

func (list *sortedList) remove(hash string) {
	for i, key := range list.listHash {
		if key == hash {
			copy(list.listHash[i:], list.listHash[i+1:])
			list.listHash = list.listHash[:len(list.listHash)-1]
			return
		}
	}
	log.Error().Msg("The transaction should be present")
}

func (list *sortedList) setMap(mapTx *map[string]Transaction) {
	list.mapTx = mapTx
}

type sortedLinkedList struct {
	mapTx *map[string]Transaction
	start *linkedHash
	end   *linkedHash
}

type linkedHash struct {
	hash     string
	prevLink *linkedHash
	postLink *linkedHash
}

func newSortedLinkedList(mapTx *map[string]Transaction) *sortedLinkedList {
	return &sortedLinkedList{mapTx, nil, nil}
}

func (linkedList *sortedLinkedList) insert(hash string) {
	if linkedList.start == nil {
		link := linkedHash{hash: hash}
		linkedList.start = &link
		linkedList.end = &link
		return
	}
	currentLink := linkedList.end
	timestamp := (*linkedList.mapTx)[hash].TransaCore.Timestamp
	for currentLink != nil && (*linkedList.mapTx)[currentLink.hash].TransaCore.Timestamp > timestamp {
		currentLink = currentLink.prevLink
	}
	if currentLink == nil {
		old_first := linkedList.start
		linkedList.start = &linkedHash{
			hash:     hash,
			prevLink: nil,
			postLink: old_first,
		}
		old_first.prevLink = linkedList.start
		return
	}

	old_next := currentLink.postLink
	currentLink.postLink = &linkedHash{
		hash:     hash,
		prevLink: currentLink,
		postLink: old_next,
	}
	if old_next != nil {
		old_next.prevLink = currentLink.postLink
	} else {
		linkedList.end = currentLink.postLink
	}
}

func (linkedList sortedLinkedList) getFirstsElem(nbElem int) []string {
	temp := make([]string, nbElem)
	var n int
	start := linkedList.start
	for start != nil && n < nbElem {
		temp[n] = start.hash
		n++
		start = start.postLink
	}
	return temp[:n]
}

func (linkedList sortedLinkedList) getElemNumber(nbElem int) string {
	var n int
	start := linkedList.start
	for start != nil && n < nbElem {
		n++
		start = start.postLink
	}
	return start.hash
}

func (linkedList *sortedLinkedList) remove(hash string) {
	start := linkedList.start
	if start == nil {
		log.Error().Msg("The transaction should be present")
		return
	}
	for start.hash != hash {
		start = start.postLink
		if start == nil {
			log.Error().Msg("The transaction should be present")
			return
		}
	}
	if start.prevLink == nil {
		linkedList.start = start.postLink
	} else {
		start.prevLink.postLink = start.postLink
	}
	if start.postLink == nil {
		linkedList.end = start.prevLink
	} else {
		start.postLink.prevLink = start.prevLink
	}
}

func (list *sortedLinkedList) setMap(mapTx *map[string]Transaction) {
	list.mapTx = mapTx
}

type unsortedMap struct {
	hashMap map[string]struct{}
}

func newUnsortedMap(mapTx *map[string]Transaction) *unsortedMap {
	return &unsortedMap{hashMap: make(map[string]struct{})}
}

func (uMap *unsortedMap) insert(hash string) {
	uMap.hashMap[hash] = struct{}{}
}

func (uMap *unsortedMap) getFirstsElem(nbElem int) []string {
	list := make([]string, nbElem)
	var n int
	for hash, _ := range uMap.hashMap {
		list[n] = hash
		n++
		if n >= nbElem {
			break
		}
	}
	return list[:n]
}

func (uMap *unsortedMap) getElemNumber(nbElem int) string {
	var hash string
	var n int
	for hashTemp, _ := range uMap.hashMap {
		hash = hashTemp
		n++
		if n >= nbElem {
			break
		}
	}
	return hash
}

func (uMap *unsortedMap) remove(hash string) {
	delete(uMap.hashMap, hash)
}

func (uMap *unsortedMap) setMap(m *map[string]Transaction) {}
