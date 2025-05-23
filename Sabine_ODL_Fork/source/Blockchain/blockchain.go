package Blockchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/rs/zerolog/log"
)

type Blockchain struct {
	validatorList  ValidatorInterf
	firstBlock     *linkedBlock
	sizeChain      int
	lastBlock      *linkedBlock // Pointer to the last block in the chain
	secLastBlock   *linkedBlock // Pointer to the second to last block
	existingBlock  map[string]struct{}
	executedTx     executedTxSec
	Save           func()
	chainLock      sync.RWMutex // Mutex for chain structure (lastBlock, secLastBlock, sizeChain, firstBlock)
	executedTxLock sync.RWMutex // Mutex for the executedTx map
	metrics        *MetricHandler
	DelayUpdater   UpdateDelay
	selector       ValidatorSelectorUpdateInter
	proposer       ed25519.PublicKey
	proposerID     int
}

type linkedBlock struct {
	block    Block
	nextLink *linkedBlock
}

func newLinkedBlock(block Block) *linkedBlock {
	return &linkedBlock{block: block}
}

func (link *linkedBlock) append(block Block) *linkedBlock {
	link.nextLink = newLinkedBlock(block)
	return link.nextLink
}

type ChainInterface interface {
	GetLastBLoc() Block
	GetSecLastBLoc() Block
	GetBlock(id int) Block
}

// NewBlockchain the constructor takes an argument validators class object
// this is used to create a list of validators
func NewBlockchain(validators ValidatorInterf, selector ValidatorSelectorUpdateInter, socketDelay UpdateDelay) (blockchain *Blockchain) {
	firstBlock := newLinkedBlock(Genesis())
	blockchain = &Blockchain{
		firstBlock:    firstBlock,
		lastBlock:     firstBlock, // Initially lastBlock is genesis
		secLastBlock:  nil,        // secLastBlock is initially nil
		sizeChain:     1,
		existingBlock: make(map[string]struct{}), // existingBlock might need its own lock if heavily contended
		validatorList: validators,
		executedTx:    newMapExecTxSec(), // Or your chosen security level
		Save:          func() {},
		selector:      selector,
		DelayUpdater:  socketDelay,
		// chainLock and executedTxLock are zero-value RWMutex, ready to use
	}
	// existingBlock should be protected if writes can happen concurrently with reads/writes to it.
	// For now, assuming it's managed safely around addBlock.
	blockchain.existingBlock[string(firstBlock.block.Hash)] = struct{}{}
	blockchain.updateProposer() // This reads lastBlock, but at init, it's safe.
	return
}

// pushes confirmed blocks into the chain
func (blockchain *Blockchain) addBlock(block Block) {
	blockchain.chainLock.Lock()         // Lock before modifying chain structure
	defer blockchain.chainLock.Unlock() // Unlock after

	// Before updating lastBlock, current lastBlock becomes secLastBlock
	if blockchain.lastBlock != nil { // Should always be true after genesis
		blockchain.secLastBlock = blockchain.lastBlock
	} else { // Should not happen in normal operation after genesis
		log.Error().Msg("addBlock called when blockchain.lastBlock is nil. This should not happen after genesis.")
		// Potentially re-initialize or handle error. For now, secLastBlock will remain what it was.
	}

	// Append the new block
	if blockchain.lastBlock == nil { // If for some reason chain was empty (e.g. error state)
		log.Error().Msg("addBlock: lastBlock was nil, re-initializing chain with new block as first.")
		blockchain.firstBlock = newLinkedBlock(block)
		blockchain.lastBlock = blockchain.firstBlock
		blockchain.sizeChain = 1
	} else {
		blockchain.lastBlock = blockchain.lastBlock.append(block)
		blockchain.sizeChain++
	}

	// existingBlock updates: ensure thread safety if it's accessed elsewhere concurrently.
	// If only addBlock modifies it, this is fine within chainLock.
	blockchain.existingBlock[string(block.Hash)] = struct{}{}

	// Metrics and logging are generally quick.
	// If AddOneCommittedBlock becomes slow or involves locks that could conflict, reconsider placement.
	if blockchain.metrics != nil { // Check if metrics is initialized
		blockchain.metrics.AddOneCommittedBlock()
	}
	log.Debug().Msgf("NEW BLOCK ADDED TO CHAIN : n°%d", block.SequenceNb)
}

// CreateBlock wrapper function to create blocks
func (blockchain *Blockchain) CreateBlock(transactions []Transaction, wallet Wallet) *Block {
	// GetLastBLoc handles locking for reading lastBlock
	return blockchain.GetLastBLoc().CreateBlock(transactions, wallet)
}

// GetProposer calculates the next proposer
func (blockchain *Blockchain) GetProposer() ed25519.PublicKey {
	blockchain.chainLock.RLock() // Lock for reading proposer field
	defer blockchain.chainLock.RUnlock()
	return blockchain.proposer
}

func (blockchain *Blockchain) GetProposerId() int {
	blockchain.chainLock.RLock() // Lock for reading proposerID field
	defer blockchain.chainLock.RUnlock()
	return blockchain.proposerID
}

func (blockchain *Blockchain) IsValidNewBlock(block Block) bool {
	// GetLastBLoc internally handles locking for reading blockchain.lastBlock
	lastBlock := blockchain.GetLastBLoc()

	// GetProposer internally handles locking for reading blockchain.proposer
	expectedProposer := blockchain.GetProposer()

	cond1_seqValid := (lastBlock.SequenceNb+1 == block.SequenceNb)
	cond2_prevHashValid := bytes.Equal(lastBlock.Hash, block.LastHast)
	cond3_blockSelfValid := block.VerifyBlock() // This internally logs warnings if it fails
	cond4_proposerValid := block.VerifyProposer(expectedProposer)

	if cond1_seqValid && cond2_prevHashValid && cond3_blockSelfValid && cond4_proposerValid {
		log.Debug().
			Int("blockSeq", block.SequenceNb).
			Str("blockHash", block.GetHashPayload()).
			Msg("BLOCK VALID")
		return true
	} else {
		log.Warn().
			Int("blockSeq", block.SequenceNb).
			Str("blockHash", block.GetHashPayload()).
			Str("blockProposerKey", base64.StdEncoding.EncodeToString(block.Proposer)).
			Msg("IsValidNewBlock: BLOCK INVALID - Details follow")

		if !cond1_seqValid {
			log.Warn().
				Int("blockSeq", block.SequenceNb).
				Int("expectedSeq", lastBlock.SequenceNb+1).
				Msgf("  L-- Condition 1 FAILED: Sequence number mismatch.")
		}
		if !cond2_prevHashValid {
			log.Warn().
				Int("blockSeq", block.SequenceNb).
				Str("expectedPrevHash", base64.StdEncoding.EncodeToString(lastBlock.Hash)).
				Str("blockPrevHash", base64.StdEncoding.EncodeToString(block.LastHast)).
				Msgf("  L-- Condition 2 FAILED: Previous hash mismatch.")
		}
		if !cond3_blockSelfValid {
			log.Warn().
				Int("blockSeq", block.SequenceNb).
				Msgf("  L-- Condition 3 FAILED: Block internal verification (VerifyBlock) failed. See prior warnings from VerifyBlock.")
		}
		if !cond4_proposerValid {
			log.Warn().
				Int("blockSeq", block.SequenceNb).
				Str("blockProposerInField", base64.StdEncoding.EncodeToString(block.Proposer)).
				Str("expectedProposerForSeq", base64.StdEncoding.EncodeToString(expectedProposer)).
				Msgf("  L-- Condition 4 FAILED: Proposer verification (VerifyProposer) failed.")
		}
		return false
	}
}

// AddUpdatedBlockCommited Add a block from a commit
func (blockchain *Blockchain) AddUpdatedBlockCommited(hash []byte, blockpool *BlockPool, transacPool TransactionPoolInterf, preparePool PreparePool, commitPool CommitPool) {
	// blockpool.GetBlock is assumed to be thread-safe or called in a safe context
	block := blockpool.GetBlock(hash)
	if block.Hash == nil { // Check if block was actually found
		log.Error().Str("hash", base64.StdEncoding.EncodeToString(hash)).Msg("AddUpdatedBlockCommited: Block not found in blockpool.")
		return
	}
	blockchain.AddUpdatedBlock(block, transacPool)
}

// AddUpdatedBlock Add a block from a block
func (blockchain *Blockchain) AddUpdatedBlock(block Block, transacPool TransactionPoolInterf) {
	blockchain.addBlock(block)                       // Handles locking for chain structure
	blockchain.flushTransaction(&block, transacPool) // Handles its own executedTxLock
	blockchain.updateProposer()                      // Handles locking for chainLock (via GetLastBLoc)
}

func (blockchain *Blockchain) flushTransaction(block *Block, transacPool TransactionPoolInterf) {
	blockchain.executedTxLock.Lock()
	defer blockchain.executedTxLock.Unlock()

	for _, transaction := range block.Transactions {
		commande, ok := transaction.TransaCore.Input.(Commande)
		if ok {
			log.Debug().Msg("The command will be applied")
			// apply might modify validatorList, which could affect GetProposer.
			// Ensure apply and validatorList methods are thread-safe if called concurrently.
			blockchain.apply(commande)
			log.Debug().Msg("The command was applied")
		}
		blockchain.executedTx.Add(transaction.Hash)
		// transacPool.RemoveTransaction is assumed to be thread-safe (uses channels)
		transacPool.RemoveTransaction(transaction)
	}
	if blockchain.metrics != nil {
		blockchain.metrics.AddNCommittedTx(len(block.Transactions))
	}
}

// GetLastBLoc returns a copy of the last block.
func (blockchain *Blockchain) GetLastBLoc() Block {
	blockchain.chainLock.RLock()         // Read Lock before accessing chain structure
	defer blockchain.chainLock.RUnlock() // Unlock after

	if blockchain.lastBlock == nil { // Should not happen after genesis
		log.Panic().Msg("GetLastBLoc called on a blockchain with nil lastBlock pointer!")
		// To prevent panic, return a sensible default or handle error.
		// Returning Genesis() might be misleading if the chain is truly broken.
		return Genesis()
	}
	log.Trace().Int("returningSeq", blockchain.lastBlock.block.SequenceNb).Str("returningHash", blockchain.lastBlock.block.GetHashPayload()).Msg("GetLastBLoc returning")
	return blockchain.lastBlock.block
}

// GetSecLastBLoc returns a copy of the second to last block.
func (blockchain *Blockchain) GetSecLastBLoc() Block {
	blockchain.chainLock.RLock()
	defer blockchain.chainLock.RUnlock()

	if blockchain.secLastBlock == nil {
		// If only genesis block exists, secLastBlock is nil.
		// In this case, conceptually, the "second to last" could be genesis itself.
		if blockchain.lastBlock != nil && blockchain.lastBlock.block.SequenceNb == 0 {
			return blockchain.lastBlock.block
		}
		log.Warn().Msg("GetSecLastBLoc called when secLastBlock is nil and chain has more than one block (or is empty).")
		// Fallback if lastBlock is also nil (e.g. before genesis fully initialized)
		if blockchain.lastBlock == nil {
			return Genesis()
		}
		// If secLast is nil but lastBlock exists (chain length > 1), this is unexpected.
		// For safety, could return lastBlock or a zero block.
		return blockchain.lastBlock.block // Or handle as error
	}
	return blockchain.secLastBlock.block
}

// GetCurrentSeqNb returns the sequence number of the last block.
func (blockchain *Blockchain) GetCurrentSeqNb() int {
	// GetLastBLoc already handles locking
	return blockchain.GetLastBLoc().SequenceNb
}

// GetBlock returns a copy of the block at the given id (0-indexed).
func (blockchain *Blockchain) GetBlock(id int) Block {
	blockchain.chainLock.RLock()
	defer blockchain.chainLock.RUnlock()

	if id < 0 || id >= blockchain.sizeChain {
		log.Panic().Msgf("GetBlock: Invalid block ID %d requested for chain of size %d", id, blockchain.sizeChain)
		return Genesis() // Fallback
	}
	current := blockchain.firstBlock
	for i := 0; i < id; i++ {
		if current.nextLink == nil { // Should not happen if id is within sizeChain
			log.Panic().Msgf("GetBlock: Reached end of chain prematurely while seeking block ID %d (chain size %d)", id, blockchain.sizeChain)
			return Genesis() // Fallback
		}
		current = current.nextLink
	}
	return current.block
}

// GetLenght returns the current length of the blockchain.
func (blockchain *Blockchain) GetLenght() int {
	blockchain.chainLock.RLock()
	defer blockchain.chainLock.RUnlock()
	return blockchain.sizeChain
}

// getIDOf is an internal helper, assuming validatorList access is safe or handled by caller.
func (blockchain *Blockchain) getIDOf(wallet Wallet) int {
	// validatorList methods should be thread-safe if they modify shared state.
	return blockchain.validatorList.GetIndexOfValidator(wallet.publicKey)
}

func (blockchain *Blockchain) apply(commande Commande) {
	// This method modifies validatorList, which is read by updateProposer.
	// Ensure ValidatorInterf methods are thread-safe.
	// If validatorList.SetNewListOfValidator and GetNumberOfValidator are not internally locked,
	// they need to be, or a lock needs to be acquired here if `apply` can be called concurrently
	// with `updateProposer` or other reads of validatorList.
	// Assuming ValidatorInterf methods handle their own concurrency for now.
	switch commande.Order {
	case VarieValid:
		newSize := len(commande.NewValidatorSet)
		if blockchain.validatorList.IsSizeValid(newSize) {
			updated := blockchain.validatorList.SetNewListOfValidator(commande.NewValidatorSet)
			if updated && blockchain.selector != nil { // Check if selector is initialized
				listValIndex := blockchain.validatorList.getIndexOfValidators()
				blockchain.selector.Update(listValIndex)
			}
			log.Debug().Msgf("Number of Active Node %d", blockchain.validatorList.GetNumberOfValidator())
			blockchain.validatorList.logAllValidator()
		} else {
			log.Error().Msgf("The new size of validator set is invalid, new size: %d", newSize)
		}
	case ChangeDelay:
		if blockchain.DelayUpdater != nil {
			blockchain.DelayUpdater.UpdateDelay(commande.Variation)
		}
	default:
		log.Error().Msgf("Unknown command order type %d", commande.Order)
	}
}

func (blockchain *Blockchain) ExistTx(transactionHash []byte) bool {
	blockchain.executedTxLock.RLock()
	defer blockchain.executedTxLock.RUnlock()
	return blockchain.executedTx.Exist(transactionHash)
}

// GetChainJson Return the blockchain in the Json format
func (blockchain *Blockchain) GetChainJson() []byte {
	// GetBlocs already handles locking
	blocs := blockchain.GetBlocs()
	byted, err := json.MarshalIndent(*blocs, "", "  ")
	if err != nil {
		log.Warn().Err(err).Msg("Error marshaling blockchain to JSON")
		return nil
	}
	return byted
}

func (blockchain *Blockchain) GetBlocs() *[]Block {
	blockchain.chainLock.RLock()
	defer blockchain.chainLock.RUnlock()

	size := blockchain.sizeChain
	if size == 0 { // Handle empty chain case
		emptyList := make([]Block, 0)
		return &emptyList
	}
	copie := make([]Block, size)
	pointer := blockchain.firstBlock
	for i := 0; i < size; i++ {
		if pointer == nil { // Safety check, should not happen if sizeChain is correct
			log.Error().Msgf("GetBlocs: Reached nil pointer at index %d with chain size %d", i, size)
			actualCopied := copie[:i] // Return what was copied so far
			return &actualCopied
		}
		copie[i] = pointer.block
		pointer = pointer.nextLink
	}
	return &copie
}

func (blockchain *Blockchain) GetFragmentBlocks(point chan<- *[]*Block) {
	// This method needs to be careful with locking if the chain can be modified
	// while it's iterating. For simplicity, let's lock for the whole duration of preparing fragments.
	// Or, it could copy necessary pointers under lock then release.
	// A safer approach might be to copy the relevant block data under lock.
	blockchain.chainLock.RLock()
	defer blockchain.chainLock.RUnlock()

	size := blockchain.sizeChain
	if size == 0 {
		point <- nil // Signal end if chain is empty
		return
	}

	pointer := blockchain.firstBlock
	var done int
	for done < size {
		blockFragmentSize := min(blocFragmentMax, size-done)
		fragment := make([]*Block, blockFragmentSize)
		for j := 0; j < blockFragmentSize; j++ {
			if pointer == nil { // Should not happen
				log.Error().Msg("GetFragmentBlocks: Nil pointer encountered unexpectedly.")
				point <- nil // Signal error or end
				return
			}
			// Create a copy of the block for the fragment to avoid sending pointers to locked data
			// if the lock were released per fragment. However, with RLock for the whole func,
			// sending pointers is okay, but the receiver must be aware.
			// For safety, let's send copies if this data is used outside the lock by the receiver.
			// However, Saver seems to just MarshalIndent, which should be fine with pointers to copies.
			// The original code sent &pointer.block.
			// Let's assume Saver handles it, but if there are issues, make copies:
			// blockCopy := pointer.block
			// fragment[j] = &blockCopy
			fragment[j] = &(pointer.block) // Sending pointer to the block in the list
			pointer = pointer.nextLink
		}
		point <- &fragment
		done += blockFragmentSize
	}
	point <- nil // Signal end of fragments
}

func (blockchain *Blockchain) ExistBlockOfHash(hash []byte) bool {
	// Assuming existingBlock is protected by chainLock or its own lock if necessary.
	// If only addBlock modifies it, and addBlock is under chainLock, then this is fine.
	blockchain.chainLock.RLock()
	defer blockchain.chainLock.RUnlock()
	_, ok := blockchain.existingBlock[string(hash)]
	return ok
}

func (blockchain *Blockchain) SetMetricHandler(handler *MetricHandler) {
	// This is an assignment, if metrics can be set concurrently, might need a lock.
	// Typically set once at init.
	blockchain.metrics = handler
}

func (blockchain *Blockchain) updateProposer() {
	blockchain.chainLock.Lock() // Acquire write lock because we are reading lastBlock and then writing proposer/proposerID
	defer blockchain.chainLock.Unlock()

	if blockchain.lastBlock == nil {
		log.Error().Msg("updateProposer: lastBlock is nil. Cannot determine proposer.")
		// Set to a known invalid/default state if lastBlock is unexpectedly nil
		blockchain.proposerID = -1
		blockchain.proposer = nil
		return
	}
	// Use the block data from the locked blockchain.lastBlock
	lastBlockForProposerCalc := blockchain.lastBlock.block

	nbVal := blockchain.validatorList.GetNumberOfValidator() // Assumed to be thread-safe or called in a safe context
	index := 0

	if nbVal > 0 {
		if len(lastBlockForProposerCalc.Hash) > 0 {
			// Use the first byte of the last block's hash for pseudo-random selection
			index = int(lastBlockForProposerCalc.Hash[0]) % nbVal
		} else {
			// This case should ideally not happen for a valid, committed block.
			// If hash is empty, it could lead to a predictable proposer (always index 0).
			log.Warn().
				Int("blockSeq", lastBlockForProposerCalc.SequenceNb).
				Msg("updateProposer: Last block hash is empty! Using index 0 for proposer selection. This may lead to skewed proposer distribution.")
			// index remains 0
		}
	} else {
		log.Error().
			Int("blockSeq", lastBlockForProposerCalc.SequenceNb).
			Msg("updateProposer: Number of active validators is zero. Cannot determine proposer.")
		blockchain.proposerID = -1
		blockchain.proposer = nil
		return
	}

	// Get the actual public key and original index of the chosen active validator
	blockchain.proposerID, blockchain.proposer = blockchain.validatorList.GetActiveValidatorIndexOfValue(index)

	// Enhanced log message
	log.Debug().
		Int("basedOnSeq", lastBlockForProposerCalc.SequenceNb).
		Str("basedOnHash", lastBlockForProposerCalc.GetHashPayload()). // Use GetHashPayload for consistent base64
		Int("nextProposerNodeID", blockchain.proposerID).              // This is the original index in the full node list
		Str("nextProposerPubKey", base64.StdEncoding.EncodeToString(blockchain.proposer)).
		Int("forBlockSeq", lastBlockForProposerCalc.SequenceNb+1). // Clarify this is for the *next* block
		Msg("Calculated next proposer")
}
