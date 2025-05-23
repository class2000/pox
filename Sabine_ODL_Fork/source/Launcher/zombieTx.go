package Launcher

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	// "sync" // <<< REMOVED UNUSED IMPORT
	"time"
	// "net" // Removed net import if only connectWith was using it directly
	// "pbftnode/source/Blockchain/Socket" // Removed if only connectWith was using it

	"github.com/rs/zerolog/log"
	"pbftnode/source/Blockchain"
)

type ZombieTxArg struct {
	ZombieArg
	Scenario         []Scenarii
	DelayScenarioStr string
	DelayScenario    []Scenarii
	Multi            bool
}

type Scenarii struct {
	NbTxPS   int
	Duration int
}

func ZombieTx(arg ZombieTxArg) {
	var continu = true
	// var waitGroup sync.WaitGroup // <<< REMOVED UNUSED VARIABLE
	var mySender sender // Use the sender interface
	var listContact []string
	var nContact int

	arg.init()

	if len(arg.DelayScenarioStr) != 0 {
		var errDelay error
		arg.DelayScenario, errDelay = handleDelayScenario(arg.DelayScenarioStr)
		if errDelay != nil {
			log.Fatal().Err(errDelay).Msg("Failed to parse delay scenario")
		}
	}

	Contact := arg.Contact
	if arg.ByBootstrap {
		// Wait until enough nodes are connected via bootstrap
		for nContact < arg.NumberOfNode && continu { // Check continu flag
			Contact, nContact, listContact = getAContact(arg.Contact) // Assuming getAContact is defined elsewhere (e.g., client.go or needs to be moved)
			log.Info().Msgf("%d nodes connected to the bootstrap server (waiting for %d)", nContact, arg.NumberOfNode)
			if nContact < arg.NumberOfNode {
				select {
				case <-time.After(1 * time.Second):
				case <-arg.interruptChan: // Check for interrupt while waiting
					log.Info().Msg("Interrupt received while waiting for nodes.")
					continu = false
				}
			}
		}
		if !continu { // Exit if interrupted
			arg.close()
			return
		}
	} else {
		// If not using bootstrap, assume Contact is direct and listContact might be empty
		// We still need listContact if using multiSend mode
		_, nContact, listContact = getAContact(arg.Contact) // Get contacts even if not waiting
		log.Info().Msgf("Proceeding with %d known node contacts.", nContact)

	}
	// Socket.GobLoader() // GobLoader should be called once, e.g., in main or NetSocket init

	// Initialize sender based on Multi flag
	if arg.Multi {
		if len(listContact) == 0 {
			log.Fatal().Msg("Multi mode selected but no node contacts available from bootstrap.")
		}
		mySender = newMultiSend(listContact, false) // Use random distribution for multi-send
	} else {
		if Contact == "" {
			log.Fatal().Msg("Single node mode selected but no contact address provided.")
		}
		// contacts struct handles single connection (with potential fallback)
		mySender = newContacts(Contact, listContact)
	}

	validator := Blockchain.Validators{}
	validator.GenerateAddresses(arg.NumberOfNode)
	selector := arg.SelectionType.CreateValidatorSelector(Blockchain.ArgsSelector{})

	// Goroutine to handle interrupt signal
	go stopSleep(arg.interruptChan, &continu, nil, nil) // Pass nil connection as sender handles its own

	wallet := Blockchain.NewWallet("NODE" + arg.NodeID)

	// Send initial validator set command
	newListProposal := selector.GenerateNewValidatorListProposition(&validator, arg.NbVal, -1)
	log.Info().Msgf("Sending initial command to set validator count to %d", arg.NbVal)
	mySender.Add(1) // Add count for the command send
	mySender.send(*wallet.CreateTransaction(Blockchain.Commande{
		Order:           Blockchain.VarieValid,
		Variation:       arg.NbVal, // Redundant if NewValidatorSet is primary source of truth
		NewValidatorSet: newListProposal,
	}), true, Blockchain.AskToBroadcast) // Send command with high priority
	mySender.Wait() // Wait for the command send attempt to complete

	time.Sleep(1 * time.Second) // Allow time for command processing
	log.Info().Msgf("Initial validator set command sent. Starting transaction workload.")

	timeStart := time.Now()
	// Start delay change routine if applicable
	if len(arg.DelayScenario) > 0 {
		go delayRoutine(mySender, arg.DelayScenario, wallet)
	}

	// Main transaction sending loop
	loopCounter := 0
	for y, scenarii := range arg.Scenario {
		if !continu { break } // Check for interrupt
		log.Info().Msgf("Starting Scenario %d: %d Tx/s for %d s", y, scenarii.NbTxPS, scenarii.Duration)
		fmt.Printf("Starting Scenario %d: %d Tx/s for %d s\n", y, scenarii.NbTxPS, scenarii.Duration)

		if scenarii.NbTxPS == 0 {
			log.Info().Msgf("Waiting for %d s", scenarii.Duration)
			select {
			case <-time.After(time.Duration(scenarii.Duration) * time.Second):
			case <-arg.interruptChan:
				continu = false
			}
		} else {
			var waitSleep = time.Duration(1e9/float64(scenarii.NbTxPS)) * time.Nanosecond
			var nbTx = scenarii.Duration * scenarii.NbTxPS
			onePercent := nbTx / 100
			if onePercent == 0 { onePercent = 1 }

			fmt.Printf("\rSending transactions: 0 / %d", nbTx)
			ticker := time.NewTicker(waitSleep)
			for i := 0; continu && i < nbTx; i++ {
				select {
				case <-ticker.C:
					if (i+1)%onePercent == 0 || i == nbTx-1 {
						fmt.Printf("\rSending transactions: %d / %d", i+1, nbTx)
					}
					transac := wallet.CreateBruteTransaction([]byte(fmt.Sprintf("Workload transaction n°%d", loopCounter)))
					loopCounter++
					mySender.Add(1) // Increment wait group *before* sending
					go func(tx Blockchain.Transaction) { // Pass transaction by value
						mySender.send(tx, false, Blockchain.AskToBroadcast)
					}(*transac)
				case <-arg.interruptChan: // Check for interrupt within inner loop
					log.Info().Msg("Interrupt during transaction sending.")
					continu = false
					break // Exit inner loop
				}
			}
			ticker.Stop()
			fmt.Println("") // Newline after progress indicator
		}
	}

	if continu {
		log.Info().Msg("All scenarios completed.")
	} else {
		log.Info().Msg("Scenarios interrupted.")
	}

	t1 := time.Now()
	log.Info().Msgf("Transaction sending phase took %v.", t1.Sub(timeStart))
	fmt.Printf("\nTransaction sending phase finished. Took %v.\n", t1.Sub(timeStart))

	log.Info().Msg("Waiting for pending sends to complete...")
	mySender.Wait() // Wait for all Add() calls to be matched by Done()
	log.Info().Msg("All pending sends completed.")
	fmt.Printf("Waiting for pending sends completed. Additional wait time: %v\n", time.Since(t1))

	mySender.close() // Close sender connections
	arg.close()      // Close logger etc.
	log.Info().Msg("ZombieTx finished.")
}


// handleDelayScenario parses the delay scenario string.
func handleDelayScenario(scenarioStr string) ([]Scenarii, error) {
	var delayScenarii []Scenarii
	scenarioStr = strings.ReplaceAll(scenarioStr, "\"", "")
	for _, word := range strings.Split(scenarioStr, " ") {
		if len(word) == 0 {
			continue
		}
		subworld := strings.Split(word, ":")
		if len(subworld) > 2 {
			errorString := fmt.Sprintf("the world %s doesn't respect this format \"delay:duration\"", word)
			return nil, errors.New(errorString)
		}
		var scenarii Scenarii
		var err error
		if len(subworld) == 1 {
			scenarii.NbTxPS, err = strconv.Atoi(subworld[0]) // NbTxPS field holds delay value here
			if err != nil {
				return nil, fmt.Errorf("delay value %s is not an int: %w", subworld[0], err)
			}
			scenarii.Duration = -1 // Indicate indefinite duration
		} else {
			scenarii.NbTxPS, err = strconv.Atoi(subworld[0]) // Delay value
			if err != nil {
				return nil, fmt.Errorf("delay value %s is not an int: %w", subworld[0], err)
			}
			scenarii.Duration, err = strconv.Atoi(subworld[1]) // Duration for this delay
			if err != nil {
				return nil, fmt.Errorf("duration value %s is not an int: %w", subworld[1], err)
			}
		}
		delayScenarii = append(delayScenarii, scenarii)
	}
	return delayScenarii, nil
}

// delayRoutine sends commands to change network delay based on a schedule.
func delayRoutine(mySender sender, delayScenario []Scenarii, wallet *Blockchain.Wallet) {
	log.Info().Msg("Starting delay change routine.")
	for _, scenario := range delayScenario {
		log.Info().Msgf("Setting network delay to %d ms", scenario.NbTxPS) // NbTxPS holds delay value
		cmd := Blockchain.Commande{
			Order:     Blockchain.ChangeDelay,
			Variation: scenario.NbTxPS, // The delay value
		}
		mySender.Add(1)
		go func(command Blockchain.Commande) { // Pass command by value
			mySender.send(*wallet.CreateTransaction(command), true, Blockchain.AskToBroadcast)
		}(cmd)

		if scenario.Duration < 0 {
			log.Info().Msg("Delay set indefinitely. Exiting delay routine.")
			return // Indefinite duration, exit routine
		}
		log.Info().Msgf("Maintaining delay for %d seconds.", scenario.Duration)
		time.Sleep(time.Duration(scenario.Duration) * time.Second)
	}
	log.Info().Msg("Delay change routine finished.")
}
