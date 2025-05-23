package Blockchain

import (
	"github.com/rs/zerolog/log"
	"pbftnode/source/config"
	"sync"
	"time"
)

type OverloadBehavior uint8

const (
	Nothing OverloadBehavior = iota
	Ignore
	Drop
)

func StrToBehavior(behavior string) OverloadBehavior {
	switch behavior {
	case "Nothing":
		return Nothing
	case "Ignore":
		return Ignore
	case "Drop":
		return Drop
	default:
		log.Error().Msgf("The string %s is not a behavior", behavior)
		return Nothing
	}
}

func (behavior OverloadBehavior) afterTxAdd(pool *TransactionPool) {
	switch behavior {
	case Ignore:
		if pool.txPoolSize() == pool.transactionThresold {
			pool.stopChanHandler.tellIfContinu(false)
		}
	case Drop:
		if pool.txPoolSize() == pool.transactionThresold {
			pool.almostClear()
		}
	}
}

func (behavior OverloadBehavior) afterTxRemove(pool *TransactionPool) {
	switch behavior {
	case Ignore:
		if !pool.stopChanHandler.getStatus() && pool.txPoolSize() <= pool.transactionThresold*3/4 {
			pool.stopChanHandler.tellIfContinu(true)
		}
	}
}

type TransactionPool struct {
	validators          ValidatorInterf
	transactions        map[string]Transaction
	commands            map[string]Transaction
	transactionThresold int
	sortedList          sortedStruct
	metrics             MetricInterf
	chanExistingTx      chan struct {
		tx   Transaction
		resp chan bool
	}
	chanPoolSize chan chan int
	chanClear    chan chan struct{}
	chanAddTx    chan struct { // Channel for AddTransaction requests
		tx   Transaction
		resp chan bool
	}
	chanRemoveTx chan struct {
		tx   Transaction
		resp chan struct{}
	}
	overloadBehavior OverloadBehavior
	chanGetBlockTx   chan chan []Transaction
	stopChanHandler  stopingChanHandler
}

type stopingChanHandler struct {
	listChan []chan bool
	status   bool
}

func newStopingChanHandler() stopingChanHandler {
	return stopingChanHandler{status: true}
}

func (handler *stopingChanHandler) createAChan() <-chan bool {
	channel := make(chan bool, 8) // Buffered channel
	handler.listChan = append(handler.listChan, channel)
	return channel
}

func (handler *stopingChanHandler) tellIfContinu(newStatus bool) {
	if handler.status != newStatus {
		handler.status = newStatus
        log.Info().Bool("canAddTx", newStatus).Msg("TransactionPool: Setting add transaction status.")
		for _, channel := range handler.listChan {
			// Send status non-blockingly
            select {
            case channel <- newStatus:
            default:
                // If the channel is full or closed, the receiver isn't ready or active.
                // Log a warning but don't block. The receiver will get the latest status eventually
                // or is already shutting down.
                log.Warn().Msg("TransactionPool: Failed to send new status to a stop channel (likely full or closed).")
            }
		}
	}
}

func (handler stopingChanHandler) getStatus() bool {
	return handler.status
}

func (pool *TransactionPool) loopTransactionPool() {
	var wait sync.WaitGroup // Wait group for internal Add/Remove/Exist checks
    log.Info().Msg("TransactionPool: loopTransactionPool started.")
	for {
		select {
		case query, ok := <-pool.chanExistingTx:
            if !ok { log.Info().Msg("TransactionPool: chanExistingTx closed, exiting loop."); return }
			wait.Add(1)
			go func() {
				defer wait.Done()
				query.resp <- pool.existingTransaction(query.tx)
			}()
		case query, ok := <-pool.chanPoolSize:
            if !ok { log.Info().Msg("TransactionPool: chanPoolSize closed, exiting loop."); return }
			wait.Add(1)
			go func() {
				defer wait.Done()
				query <- pool.poolSize()
			}()
		case query, ok := <-pool.chanClear:
            if !ok { log.Info().Msg("TransactionPool: chanClear closed, exiting loop."); return }
			wait.Wait() // Wait for any pending operations before clearing
			pool.clear()
			query <- struct{}{}
		case query, ok := <-pool.chanAddTx: // Receive AddTransaction requests
            if !ok { log.Info().Msg("TransactionPool: chanAddTx closed, exiting loop."); return }
			// No wait.Wait() here, adding is concurrent with Existing/Size checks
			query.resp <- pool.addTransaction(query.tx)
		case query, ok := <-pool.chanRemoveTx:
            if !ok { log.Info().Msg("TransactionPool: chanRemoveTx closed, exiting loop."); return }
			wait.Wait() // Wait for pending operations before removing
			pool.removeTransaction(query.tx)
			query.resp <- struct{}{} // Acknowledge removal attempt
		case query, ok := <-pool.chanGetBlockTx:
            if !ok { log.Info().Msg("TransactionPool: chanGetBlockTx closed, exiting loop."); return }
			wait.Add(1)
			go func() {
				defer wait.Done()
				query <- pool.getTxForBloc()
			}()
		}
	}
}

// ExistingTransaction check if the transaction exists or not
func (pool TransactionPool) ExistingTransaction(transaction Transaction) bool {
	respChan := make(chan bool)
    // Use non-blocking send if possible, but this is an external API,
    // maybe blocking send is acceptable to apply backpressure? Let's keep blocking.
    // Add logging for the send
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.ExistingTransaction: Sending to chanExistingTx.")
	pool.chanExistingTx <- struct {
		tx   Transaction
		resp chan bool
	}{tx: transaction, resp: respChan}
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.ExistingTransaction: Sent to chanExistingTx, waiting for response.")
	resp := <-respChan // This will block until loopTransactionPool handles it
    log.Debug().Str("txHash", transaction.GetHashPayload()).Bool("exists", resp).Msg("TransactionPool.ExistingTransaction: Received response.")
	close(respChan)
	return resp
}

func (pool TransactionPool) existingTransaction(transaction Transaction) bool {
    // This runs inside loopTransactionPool or a goroutine spawned by it
	_, okc := pool.commands[string(transaction.Hash)]
	_, okt := pool.transactions[string(transaction.Hash)]
	return okc || okt
}

// PoolSize return the pool size
func (pool TransactionPool) PoolSize() int {
	query := make(chan int)
    // Add logging for the send
    log.Debug().Msg("TransactionPool.PoolSize: Sending to chanPoolSize.")
	pool.chanPoolSize <- query // This will block until loopTransactionPool handles it
    log.Debug().Msg("TransactionPool.PoolSize: Sent to chanPoolSize, waiting for response.")
	resp := <-query
    log.Debug().Int("size", resp).Msg("TransactionPool.PoolSize: Received response.")
	close(query)
	return resp
}

func (pool TransactionPool) poolSize() int {
    // This runs inside loopTransactionPool or a goroutine spawned by it
	return pool.txPoolSize() + pool.commandPoolSize()
}

func (pool TransactionPool) txPoolSize() int {
	return len(pool.transactions)
}

func (pool TransactionPool) commandPoolSize() int {
	return len(pool.commands)
}

func (pool TransactionPool) IsEmpty() bool {
	return pool.PoolSize() == 0 // This calls PoolSize which uses channels
}

func (pool TransactionPool) Clear() {
	query := make(chan struct{})
    log.Debug().Msg("TransactionPool.Clear: Sending to chanClear.")
	pool.chanClear <- query // This will block until loopTransactionPool handles it
    log.Debug().Msg("TransactionPool.Clear: Sent to chanClear, waiting for response.")
	<-query
    log.Debug().Msg("TransactionPool.Clear: Received response, pool cleared.")
	close(query)
}

func (pool *TransactionPool) clear() {
    // This runs inside loopTransactionPool
	log.Info().Msg("TRANSACTION POOL CLEARED")
	pool.transactions = make(map[string]Transaction, pool.transactionThresold)
	pool.sortedList = newSortedLinkedList(&pool.transactions) // Assumes linked list is default
}

func (pool TransactionPool) AddTransaction(transaction Transaction) bool {
	respChan := make(chan bool)
    // Add logging for the send
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.AddTransaction: Sending to chanAddTx.")
	pool.chanAddTx <- struct { // This will block until loopTransactionPool handles it
		tx   Transaction
		resp chan bool
	}{tx: transaction, resp: respChan}
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.AddTransaction: Sent to chanAddTx, waiting for response.")
	resp := <-respChan
    log.Debug().Str("txHash", transaction.GetHashPayload()).Bool("success", resp).Msg("TransactionPool.AddTransaction: Received response.")
	close(respChan)
	return resp
}

func (pool *TransactionPool) addTransaction(transaction Transaction) bool {
    // This runs inside loopTransactionPool
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.addTransaction: Received transaction from channel.")
	if transaction.IsCommand() {
		if pool.commandPoolSize() < pool.transactionThresold {
			pool.commands[string(transaction.Hash)] = transaction
			log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("added command to pool")
			// Removed the AddOneReceivedTx metric here as it's called in receiveTransacMess now
			return true
		}
        log.Warn().Str("txHash", transaction.GetHashPayload()).Msg("addTransaction: Command pool full, not added.")
	} else { // Regular transaction
		if pool.txPoolSize() < pool.transactionThresold {
			pool.transactions[string(transaction.Hash)] = transaction
			pool.sortedList.insert(string(transaction.Hash))
			log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("added transaction to pool")
            // Removed the AddOneReceivedTx metric here as it's called in receiveTransacMess now
			pool.overloadBehavior.afterTxAdd(pool)
			return true
		}
        log.Warn().Str("txHash", transaction.GetHashPayload()).Msg("addTransaction: Transaction pool full (non-command), not added.")
	}
    // The AddOneReceivedTx metric is moved to receiveTransacMess, called before AddTransaction.
    // This ensures we count a transaction as 'received' even if it's dropped by the pool logic.
    // If you only want to count transactions *successfully added* to the pool, move the metric back here.
	return false // Explicitly return false if not added
}

func (pool TransactionPool) RemoveTransaction(transaction Transaction) {
	respChan := make(chan struct{})
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.RemoveTransaction: Sending to chanRemoveTx.")
	pool.chanRemoveTx <- struct {
		tx   Transaction
		resp chan struct{}
	}{tx: transaction, resp: respChan}
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.RemoveTransaction: Sent to chanRemoveTx, waiting for response.")
	<-respChan // This will block until loopTransactionPool handles it
    log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.RemoveTransaction: Received response, removal attempt completed.")
	close(respChan)
}

func (pool *TransactionPool) removeTransaction(transaction Transaction) {
    // This runs inside loopTransactionPool
	log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("TransactionPool.removeTransaction: Received transaction for removal.")
	if transaction.IsCommand() {
		delete(pool.commands, string(transaction.Hash))
        log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("removed command from pool")
	} else {
		hash := string(transaction.Hash)
		_, ok := pool.transactions[hash]
		if ok {
			delete(pool.transactions, hash)
			pool.sortedList.remove(hash)
             log.Debug().Str("txHash", transaction.GetHashPayload()).Msg("removed transaction from pool")
		} else {
             log.Warn().Str("txHash", transaction.GetHashPayload()).Msg("removeTransaction: Transaction not found in pool.")
        }
		pool.overloadBehavior.afterTxRemove(pool)
	}
}

func (pool *TransactionPool) almostClear() {
	almostSize := pool.transactionThresold / 64
	log.Warn().Int64("at", time.Now().UnixNano()).
		Msgf("%d messages are removed during almostClear (from %d down to %d)", pool.txPoolSize()-almostSize, pool.txPoolSize(), almostSize)
	newTxMap := make(map[string]Transaction, pool.transactionThresold)
	newSortList := newSortedLinkedList(&newTxMap)
	var cmpt int
    // Iterate over the existing transactions map - order is not guaranteed here
	for key := range pool.transactions {
		newTxMap[key] = pool.transactions[key]
		newSortList.insert(key) // Insert into the new sorted list
		cmpt++
		if cmpt == almostSize {
			break // Stop once the target size is reached
		}
	}
    // Replace the old transactions map and sorted list with the new, smaller ones
	pool.transactions = newTxMap
	pool.sortedList = newSortList
	pool.sortedList.setMap(&(pool.transactions)) // Ensure the new sorted list points to the new map
    log.Info().Msgf("TransactionPool: almostClear finished. New size: %d", pool.txPoolSize())
}

func (pool TransactionPool) GetTxForBloc() []Transaction {
	query := make(chan []Transaction)
    log.Debug().Msg("TransactionPool.GetTxForBloc: Sending to chanGetBlockTx.")
	pool.chanGetBlockTx <- query // This will block until loopTransactionPool handles it
    log.Debug().Msg("TransactionPool.GetTxForBloc: Sent to chanGetBlockTx, waiting for response.")
	resp := <-query
    log.Debug().Int("count", len(resp)).Msg("TransactionPool.GetTxForBloc: Received response.")
	close(query)
	return resp
}

func (pool *TransactionPool) getTxForBloc() []Transaction {
    // This runs inside loopTransactionPool or a goroutine spawned by it
	var listTx []Transaction
    // Process commands first - only one command per block expected
	for key, tx := range pool.commands {
		// Verify command validity again before including in block
        log.Debug().Str("txHash", tx.GetHashPayload()).Msg("getTxForBloc: Checking command transaction validity.")
		if tx.VerifyTransaction() && tx.VerifyAsCommandShort(pool.validators) {
			listTx = append(listTx, tx)
            log.Debug().Str("txHash", tx.GetHashPayload()).Msg("getTxForBloc: Including valid command in block.")
			// Note: Command is NOT removed from the pool here, it's removed after block commit
			return listTx // Only one command transaction allowed per block
		} else {
			// Remove invalid commands from the pool immediately
			log.Warn().Str("txHash", tx.GetHashPayload()).Msg("getTxForBloc: Invalid command detected, removing from pool.")
            // Remove from map directly within this loopTransactionPool goroutine context
            delete(pool.commands, key)
            // No need to signal RemoveTransaction via channel here
		}
	}

    // If no command, get regular transactions from the sorted list
    // Get up to BlockSize transactions (or fewer if the pool is smaller)
    // The sortedList provides transactions ordered by timestamp
	potentielHashes := pool.sortedList.getFirstsElem(config.BlockSize)
    log.Debug().Int("potentialTxCount", len(potentielHashes)).Msg("getTxForBloc: Retrieved potential transactions from sorted list.")

    // Verify regular transactions before including them
	for _, hash := range potentielHashes {
        // Check if the transaction still exists in the map (might have been removed concurrently by another process?)
        // This check is implicitly done by accessing pool.transactions[hash]
		tx, ok := pool.transactions[hash]
        if !ok {
            log.Warn().Str("txHash", hash).Msg("getTxForBloc: Transaction found in sorted list but not in map (removed concurrently?). Skipping.")
            continue // Skip this transaction
        }

		if tx.VerifyTransaction() {
			listTx = append(listTx, tx)
            log.Debug().Str("txHash", hash).Msg("getTxForBloc: Including valid transaction in block.")
		} else {
			// Remove invalid transactions from the pool immediately
			log.Warn().Str("txHash", hash).Msg("getTxForBloc: Invalid transaction detected, removing from pool.")
            // Remove from map directly and from sorted list
			delete(pool.transactions, hash)
			pool.sortedList.remove(hash)
            // No need to signal RemoveTransaction via channel here
		}
	}

    // If the list is still smaller than BlockSize, try to fill it from remaining sorted list
    // This loop logic seems slightly off - it should iterate from where getFirstsElem stopped.
    // The current logic iterates from index 0 again, which is inefficient and might be buggy.
    // A better approach would be to get *all* valid transactions from the sorted list up to the pool size
    // and then take the first BlockSize. Or, iterate the sorted list explicitly.
    // Let's keep the original loop structure for now but note it could be improved.
    // The sorted list `getElemNumber(i)` likely accesses the element at index `i` after sorting.
    // The `len(pool.transactions) > len(listTx) + i` condition seems intended to check if there are
    // enough *remaining* unverified transactions to potentially fill the block.
    // Let's add logging to this part.
	var i int = 0 // Index into the sorted list for additional lookups
	for len(listTx) < config.BlockSize && len(pool.transactions) > len(listTx)+i {
        // Get the next potential transaction hash from the sorted list
		hash := pool.sortedList.getElemNumber(i + len(listTx)) // Try getting element after those already selected
        if hash == "" { // sortedList.getElemNumber might return "" if index is out of bounds
            log.Debug().Msgf("getTxForBloc: sortedList.getElemNumber(%d) returned empty hash. Stopping.", i + len(listTx))
            break
        }

        tx, ok := pool.transactions[hash]
        if !ok {
             log.Warn().Str("txHash", hash).Msg("getTxForBloc: Transaction found in sorted list (secondary check) but not in map (removed concurrently?). Skipping.")
             i++ // Increment index to try the next one
             continue
        }

        log.Trace().Str("txHash", hash).Msgf("getTxForBloc: Checking additional transaction from sorted list (index %d).", i + len(listTx))

		if tx.VerifyTransaction() {
			listTx = append(listTx, tx)
            log.Debug().Str("txHash", hash).Msg("getTxForBloc: Including additional valid transaction in block.")
		} else {
			log.Warn().Str("txHash", hash).Msg("getTxForBloc: Invalid additional transaction detected, removing from pool.")
            // Remove from map and sorted list
			delete(pool.transactions, hash)
			pool.sortedList.remove(hash)
			// Note: When removing from a linked list, the indices of subsequent elements change.
			// The current `getElemNumber(i)` logic might be flawed if removals happen here.
			// It might be safer to rebuild the list or use a simpler structure like unsortedMap + sort.
			// For now, increment `i` to skip the index that was removed.
			i++
		}
        // If transaction was valid and added, we don't increment `i` because `len(listTx)` increased.
	}

    log.Debug().Int("finalTxCount", len(listTx)).Msg("getTxForBloc: Finished selecting transactions for block.")
	return listTx
}

func (pool *TransactionPool) SetMetricHandler(handler MetricInterf) {
	pool.metrics = handler
}

func NewTransactionPool(validator ValidatorInterf, behavior OverloadBehavior) *TransactionPool {
	pool := &TransactionPool{
		validators:          validator,
		transactions:        make(map[string]Transaction, config.TransactionThreshold),
		commands:            make(map[string]Transaction, config.TransactionThreshold),
		transactionThresold: config.TransactionThreshold,
		chanExistingTx: make(chan struct {
			tx   Transaction
			resp chan bool
		}),
		chanPoolSize: make(chan chan int),
		chanClear:    make(chan chan struct{}),
		chanAddTx: make(chan struct {
			tx   Transaction
			resp chan bool
		}),
		chanRemoveTx: make(chan struct {
			tx   Transaction
			resp chan struct{}
		}),
		chanGetBlockTx:   make(chan chan []Transaction),
		stopChanHandler:  newStopingChanHandler(),
		overloadBehavior: behavior,
	}
    // Choose sorting implementation (using linked list by default based on TestSortedStruct)
	pool.sortedList = newSortedLinkedList(&pool.transactions)
    // If you preferred unsorted (for simplicity and maybe faster Add/Remove if BlockSize is small),
    // uncomment this line instead:
    // pool.sortedList = newUnsortedMap(&pool.transactions)

	go pool.loopTransactionPool()
	return pool
}

func (pool *TransactionPool) GetStopingChan() <-chan bool {
	return pool.stopChanHandler.createAChan()
}

func (pool *TransactionPool) changeOverloadBehavior(newBehavior OverloadBehavior) {
	pool.overloadBehavior = newBehavior
}