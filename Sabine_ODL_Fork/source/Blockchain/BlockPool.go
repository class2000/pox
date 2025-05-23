package Blockchain

import (
	"github.com/rs/zerolog/log"
)

//BlockPool is equivalent to a PrePreparePool
type BlockPool struct {
	mapBlock map[string]Block
}

func NewBlockPool() *BlockPool {
	return &BlockPool{make(map[string]Block)}
}

// AddBlock pushes block to the chain
func (blockpool *BlockPool) AddBlock(block Block) {
	blockpool.mapBlock[string(block.Hash)] = block
	log.Print("added block to pool")
}

// PoolSize return the pool size
func (blockpool BlockPool) PoolSize() int {
	return len(blockpool.mapBlock)
}

// ExistingBlock check if the block exists or not
func (blockpool BlockPool) ExistingBlock(block Block) bool {
	_, ok := blockpool.mapBlock[string(block.Hash)]
	return ok
}

// GetBlock returns the block of the given hash
func (blockpool BlockPool) GetBlock(hash []byte) Block {
	elem, _ := blockpool.mapBlock[string(hash)]
	return elem
}

func (blockpool BlockPool) GetBlocksOfSeqNb(seq int) []Block {
	var listBloc []Block
	for _, block := range blockpool.mapBlock {
		if block.SequenceNb == seq {
			listBloc = append(listBloc, block)
		}
	}
	return listBloc
}

// RemoveBlock Remove a block to the chain
func (blockpool *BlockPool) RemoveBlock(hash []byte) {
	delete(blockpool.mapBlock, string(hash))
}
