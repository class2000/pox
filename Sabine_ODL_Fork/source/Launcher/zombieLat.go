package Launcher

import (
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"pbftnode/source/Blockchain"
	"pbftnode/source/Blockchain/Socket"
)

type ZombieArg struct {
	ClientArg
	Reducing int
	NbVal    int
}

type ZombieLatArg struct {
	ZombieArg
	NbTx int
}

func ZombieLat(arg ZombieLatArg) {
	var channel_loop chan bool
	var toKill chan bool
	var continu = true

	arg.init()

	Contact := arg.Contact
	var nbOfNode int
	if arg.ByBootstrap {
		Contact, nbOfNode, _ = getAContact(arg.Contact)
		if arg.NumberOfNode != 0 && nbOfNode != arg.NumberOfNode {
			log.Panic().Msgf("Wrong number of node connected, expected %d, got %d", arg.NumberOfNode, nbOfNode)
			arg.close()
			return
		}
	} else {
		nbOfNode = arg.NumberOfNode
	}

	if nbOfNode == 0 {
		log.Panic().Msgf("The number of node should not be equal to 0")
	}

	log.Print("Try to connect with ", Contact)
	conn, err := net.Dial("tcp", Contact)
	if err != nil {
		log.Fatal().Msgf("Error connecting: %s", err.Error())
	}
	log.Debug().Msg("Connected")
	Socket.ExchangeIdClient(nil, conn)
	log.Debug().Msg("Id are exchanged")
	Socket.GobLoader()

	channel_loop = make(chan bool, 10)
	toKill = make(chan bool, 1)
	go stopSleep(arg.interruptChan, &continu, conn, channel_loop)
	encoder := gob.NewEncoder(conn)
	decoder := gob.NewDecoder(conn)
	wallet := Blockchain.NewWallet("NODE" + arg.NodeID)
	validator := Blockchain.Validators{}
	validator.GenerateAddresses(arg.NumberOfNode)
	selector := arg.SelectionType.CreateValidatorSelector(Blockchain.ArgsSelector{})
	go handleCommitReturn(decoder, arg.interruptChan, channel_loop, toKill)

	newListProposal := selector.GenerateNewValidatorListProposition(&validator, arg.NbVal, -1)
	sendTxWaitCommit(wallet.CreateTransaction(Blockchain.Commande{
		Order:           Blockchain.VarieValid,
		Variation:       arg.NbVal, // Variation might be redundant if NewValidatorSet is used
		NewValidatorSet: newListProposal,
	}), encoder, channel_loop)
	time.Sleep(500 * time.Millisecond) // Allow time for validator change to propagate/apply
	fmt.Printf("Validator set potentially updated to size %d\n", arg.NbVal)

	timeStart := time.Now()
	var toSleep time.Duration
	onePercent := arg.NbTx / 100
	if onePercent == 0 {
		onePercent = 1 // Avoid division by zero if NbTx < 100
	}

	for i := 0; continu && i < arg.NbTx; i++ {
		if (i+1)%onePercent == 0 || i == 0 || i == arg.NbTx-1 { // Print progress periodically
			fmt.Printf("\rSending transaction: %d / %d", i+1, arg.NbTx)
		}
		toSleep = sendTxWaitCommit(wallet.CreateBruteTransaction([]byte(fmt.Sprintf("Latency test transaction n°%d", i))), encoder, channel_loop)
		// Optional: Add a small fixed sleep if needed, but wait-for-commit is primary control
		// time.Sleep(1 * time.Millisecond)
	}
	fmt.Println("\nTransaction sending finished.")
	fmt.Printf("Total time for sending %d transactions: %v\n", arg.NbTx, time.Since(timeStart))

	// Wait a bit longer to ensure final commits are processed before closing
	if toSleep > 0 {
		log.Info().Msgf("Waiting %v for final commits...", 10*toSleep)
		time.Sleep(10 * toSleep)
	} else {
		time.Sleep(2 * time.Second) // Default wait if no commits were received
	}

	err = conn.Close()
	if err != nil {
		// Log non-critical error on close
		log.Error().Err(err).Msg("Error closing connection")
	}
	arg.close() // Close logger etc.
	<-toKill    // Wait for handleCommitReturn goroutine to exit
	log.Info().Msg("ZombieLat finished.")
	close(channel_loop)
	close(toKill)
}

// stopSleep handles interrupt signals and closes the connection.
func stopSleep(channel <-chan os.Signal, continu *bool, conn net.Conn, channelLoop chan<- bool) {
	<-channel // Wait for SIGINT (Ctrl+C)
	log.Warn().Msg("Interrupt signal received, stopping...")
	*continu = false // Signal other loops to stop

	if conn != nil {
		log.Debug().Msg("Closing connection due to interrupt...")
		// Close the connection; ignore "use of closed network connection" errors
		if err := conn.Close(); err != nil && err.Error() != "use of closed network connection" {
			log.Error().Err(err).Msg("Error closing connection on interrupt")
		}
	}

	// Signal potentially waiting loops (like sendTxWaitCommit)
	if channelLoop != nil {
		// Use non-blocking send in case receiver is already gone
		select {
		case channelLoop <- true: // Signal loop to break
		default:
		}
	}
	log.Debug().Msg("Stop signal processed.")
}

// sendTxWaitCommit sends a transaction and waits for a signal on channel_loop (indicating commit received).
func sendTxWaitCommit(transaction *Blockchain.Transaction, encoder *gob.Encoder, channel_loop chan bool) time.Duration {
	t0 := time.Now()
	err := encoder.Encode(Blockchain.Message{
		Flag:        Blockchain.TransactionMess,
		Data:        *transaction,
		ToBroadcast: Blockchain.AskToBroadcast, // Ask nodes to broadcast
	})
	if err != nil {
		// Log error but don't panic immediately, maybe connection dropped
		log.Error().Err(err).Str("txHash", transaction.GetHashPayload()).Msg("Failed to encode/send transaction")
		// Signal loop to potentially stop or retry? For now, return 0 duration.
		select {
		case channel_loop <- false: // Send false to indicate failure?
		default:
		}
		return 0
	}
	log.Debug().Msgf("Transaction sent %s", transaction.GetHashPayload())

	// Wait for commit signal or timeout
	select {
	case <-channel_loop:
		// Commit received
	case <-time.After(10 * time.Second): // Add a timeout
		log.Warn().Str("txHash", transaction.GetHashPayload()).Msg("Timeout waiting for commit signal")
		// Return a large duration or specific indicator?
		return 10 * time.Second // Indicate timeout occurred
	}

	t1 := time.Now()
	diff := t1.Sub(t0)
	log.Debug().Msgf("Commit signal received after %s", diff)
	return diff
}

// handleCommitReturn listens for incoming messages and signals when a commit is received.
func handleCommitReturn(decoder *gob.Decoder, c chan os.Signal, channel_loop chan bool, toKill chan<- bool) {
	defer func() {
		log.Debug().Msg("handleCommitReturn goroutine exiting.")
		toKill <- true // Signal main goroutine that this one is done
	}()

	var hashMap = make(map[string]struct{}) // Track processed block hashes

	for {
		// Check for interrupt signal before decoding
		select {
		case <-c:
			log.Info().Msg("handleCommitReturn: Interrupt received, exiting.")
			return
		default:
			// Continue to decode
		}

		var message Blockchain.Message
		err := decoder.Decode(&message)
		if err != nil {
			// Check if the error is due to connection closing or interrupt
			if opErr, ok := err.(*net.OpError); ok && (opErr.Err.Error() == "use of closed network connection" || opErr.Err.Error() == "connection reset by peer") {
				log.Info().Msg("handleCommitReturn: Connection closed, exiting.")
			} else if err == io.EOF {
				log.Info().Msg("handleCommitReturn: Connection EOF, exiting.")
			} else {
				log.Error().Err(err).Msg("handleCommitReturn: Error decoding message")
			}
			return // Exit goroutine on any decode error or EOF
		}

		if message.Flag == Blockchain.CommitMess {
			commitMess, ok := message.Data.(Blockchain.Commit)
			if !ok {
				log.Error().Msg("Received CommitMess flag but data is not Commit type")
				continue
			}
			hashStr := string(commitMess.BlockHash)
			log.Trace().Str("blockHash", commitMess.GetHashPayload()).Msg("Commit received")

			// Check if we've already signaled for this block hash
			if _, processed := hashMap[hashStr]; !processed {
				hashMap[hashStr] = struct{}{} // Mark as processed
				log.Debug().Str("blockHash", commitMess.GetHashPayload()).Msg("Signaling commit for new block hash")
				// Send signal non-blockingly
				select {
				case channel_loop <- true:
				default:
					log.Warn().Str("blockHash", commitMess.GetHashPayload()).Msg("Commit signal channel full, skipping signal")
				}
			}
		} else {
			// Log other received messages if needed for debugging
			// log.Trace().Str("type", message.Flag.String()).Msg("Received non-commit message")
		}
	}
}

// --- REMOVED DUPLICATE check FUNCTION ---
// func check(err error) {
//     if err != nil {
//         log.Error().Msgf("error %t \n-> %s", err, err)
//         panic(err)
//     }
// }
// --- /REMOVED ---
