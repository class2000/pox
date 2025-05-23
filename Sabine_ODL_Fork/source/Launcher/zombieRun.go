package Launcher

import (
	// "encoding/gob" // Likely not needed directly anymore
	"fmt"
	// "sync" // <<< REMOVED UNUSED IMPORT
	"time"

	"github.com/rs/zerolog/log"
	"pbftnode/source/Blockchain"
	// "pbftnode/source/Blockchain/Socket" // Likely not needed directly anymore
)

type ZombieRunArg struct {
	ZombieArg
	Throughput     int
	DelayPerChange int
	NbValPerChange int
}

func ZombieRun(arg ZombieRunArg) {
	// var err error // Removed unused err
	var mySender sender // Use sender interface
	var listContact []string
	var nContact int
	var continu = true
	channelLoop := make(chan bool) // Keep for stopSleep compatibility? Or remove?

	arg.init()

	Contact := arg.Contact
	if arg.ByBootstrap {
		// Wait until enough nodes are connected via bootstrap
		for nContact < arg.NumberOfNode && continu {
			Contact, nContact, listContact = getAContact(arg.Contact)
			log.Info().Msgf("%d nodes connected to the bootstrap server (waiting for %d)", nContact, arg.NumberOfNode)
			if nContact < arg.NumberOfNode {
				select {
				case <-time.After(1 * time.Second):
				case <-arg.interruptChan:
					log.Info().Msg("Interrupt received while waiting for nodes.")
					continu = false
				}
			}
		}
		if !continu {
			arg.close()
			return
		}
		if len(listContact) == 0 {
			log.Fatal().Msg("Bootstrap provided no node contacts.")
		}
		// Use multiSend if connecting via bootstrap (usually implies multiple nodes)
		mySender = newMultiSend(listContact, false) // Random distribution among nodes
	} else {
		// Use single connection mode (contacts struct) if not using bootstrap
		if Contact == "" {
			log.Fatal().Msg("Direct contact mode requires a contact address.")
		}
		_, _, listContact = getAContact(Contact) // Get list for potential fallback
		mySender = newContacts(Contact, listContact)
	}
	// Socket.GobLoader() // Called elsewhere

	wallet := Blockchain.NewWallet("NODE" + arg.NodeID)
	validator := Blockchain.Validators{}
	validator.GenerateAddresses(arg.NumberOfNode)
	selector := arg.SelectionType.CreateValidatorSelector(Blockchain.ArgsSelector{})

	// Goroutine for interrupt handling
	go stopSleep(arg.interruptChan, &continu, nil, channelLoop) // Pass nil connection

	// Send initial validator set command
	log.Info().Msgf("Sending initial command to set validator count to %d", arg.NbVal)
	newListProposal := selector.GenerateNewValidatorListProposition(&validator, arg.NbVal, -1)
	mySender.Add(1) // Add count for the command
	mySender.send(*wallet.CreateTransaction(Blockchain.Commande{
		Order:           Blockchain.VarieValid,
		Variation:       arg.NbVal,
		NewValidatorSet: newListProposal,
	}), true, Blockchain.AskToBroadcast) // High priority
	mySender.Wait() // Wait for command send attempt

	// Start continuous transaction sending goroutine
	toKillSender := make(chan chan<- struct{})
	log.Info().Msgf("Starting continuous throughput at %d Tx/s", arg.Throughput)
	go sendTxAtThroughput(arg.Throughput, wallet, mySender, toKillSender, &continu) // Pass sender interface

	// --- Validator Change Loop ---
	tickDuration := time.Duration(arg.DelayPerChange) * time.Second
	ticker := time.NewTicker(tickDuration)
	defer ticker.Stop()

	nbSteps := 0
	if arg.NbValPerChange > 0 { // Avoid division by zero
		nbSteps = (arg.NumberOfNode - 4) / arg.NbValPerChange // Max possible steps down to 4 validators
	}
	currentNbVal := arg.NbVal
	log.Info().Msgf("Starting validator reduction loop. Tick 0/%d (%d s interval)", nbSteps+1, arg.DelayPerChange)

	for i := 0; i < nbSteps && continu; i++ {
		select {
		case <-ticker.C:
			log.Info().Msgf("Tick %d/%d (%d s interval)", i+1, nbSteps+1, arg.DelayPerChange)
			targetNbVal := currentNbVal - arg.NbValPerChange
			if targetNbVal < 4 { // Ensure minimum validators
				targetNbVal = 4
			}
			if targetNbVal == currentNbVal { // No change needed
				log.Info().Msgf("Already at minimum validator count (%d).", currentNbVal)
				continue // Skip sending command if no change
			}

			log.Info().Msgf("Sending command to change validator count from %d to %d", currentNbVal, targetNbVal)
			newListProposalIt := selector.GenerateNewValidatorListProposition(&validator, targetNbVal, -1)
			mySender.Add(1) // Add count for command
			go func(cmd Blockchain.Commande) { // Send command asynchronously
				mySender.send(*wallet.CreateTransaction(cmd), true, Blockchain.AskToBroadcast)
			}(Blockchain.Commande{
				Order:           Blockchain.VarieValid,
				Variation:       targetNbVal, // Or targetNbVal - currentNbVal? Use target size.
				NewValidatorSet: newListProposalIt,
			})
			currentNbVal = targetNbVal // Update current count *after* sending command

		case <-arg.interruptChan: // Check interrupt explicitly (redundant?)
			continu = false
		case <-channelLoop: // Handle potential signal from stopSleep?
			log.Debug().Msg("Received signal on channelLoop during validator change loop.")
			// This channel might not be necessary anymore if stopSleep doesn't use it
		}
		if currentNbVal == 4 {
			log.Info().Msg("Reached minimum validator count (4).")
			break // Exit loop early if minimum is reached
		}
	}
	// --- End Validator Change Loop ---

	// Wait for one final tick duration if loop finished normally
	if continu {
		log.Info().Msgf("Waiting for final tick duration (%d s)...", arg.DelayPerChange)
		select {
		case <-ticker.C: // Wait for the next tick
			log.Info().Msgf("Final tick %d/%d completed.", nbSteps+1, nbSteps+1)
		case <-arg.interruptChan: // Or interrupt
			log.Info().Msg("Interrupted during final wait.")
			continu = false
		}
	}

	// Stop the transaction sender goroutine
	log.Info().Msg("Stopping transaction sender...")
	killerChan := make(chan struct{})
	toKillSender <- killerChan // Signal sender to stop
	<-killerChan              // Wait for sender to acknowledge stop
	log.Info().Msg("Transaction sender stopped.")

	// Wait for any remaining sends triggered by commands
	log.Info().Msg("Waiting for final command sends...")
	mySender.Wait()
	log.Info().Msg("Final command sends complete.")

	// Cleanup
	mySender.close()
	arg.close()
	log.Info().Msg("ZombieRun finished.")
	// close(channelLoop) // Close if used
	close(toKillSender)
}

// sendTxAtThroughput continuously sends transactions at a target rate.
func sendTxAtThroughput(throughput int, wallet *Blockchain.Wallet, sender sender, toKill <-chan chan<- struct{}, continu *bool) {
	if throughput <= 0 {
		log.Warn().Msg("Throughput is zero or negative, sender goroutine will not send transactions.")
		// Still need to handle shutdown signal
		killAckChan := <-toKill
		killAckChan <- struct{}{}
		return
	}

	tickDuration := time.Duration(1e9/float64(throughput)) * time.Nanosecond
	ticker := time.NewTicker(tickDuration)
	defer ticker.Stop()

	var i int64 = 0 // Use int64 for potentially long runs
	log.Info().Msgf("Transaction sender goroutine started (Rate: %d Tx/s, Interval: %v).", throughput, tickDuration)

	for {
		select {
		case <-ticker.C:
			if !*continu { // Check if main loop signaled stop
				log.Info().Msg("Sender goroutine stopping due to 'continu' flag.")
				return // Exit loop cleanly, acknowledge will happen below
			}
			transac := wallet.CreateBruteTransaction([]byte(fmt.Sprintf("Continuous transaction n°%d", i)))
			i++
			sender.Add(1) // Increment count *before* sending
			go func(tx Blockchain.Transaction) { // Pass by value
				sender.send(tx, false, Blockchain.AskToBroadcast)
			}(*transac)

		case killAckChan := <-toKill: // Receive shutdown signal
			log.Info().Msg("Sender goroutine received stop signal.")
			// Wait for any sends currently in progress via the sender's Wait()
			log.Debug().Msg("Sender waiting for pending sends before acknowledging stop...")
			sender.Wait()
			log.Debug().Msg("Pending sends complete.")
			killAckChan <- struct{}{} // Acknowledge shutdown
			log.Info().Msg("Sender goroutine finished.")
			return // Exit goroutine
		}
	}
}

