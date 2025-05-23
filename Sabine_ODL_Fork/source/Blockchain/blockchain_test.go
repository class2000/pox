package Blockchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"pbftnode/source/config"
	"testing"
)

func createListTx(data string, wallet Wallet) []Transaction {
	var nextData = []byte(data)
	return []Transaction{*NewBruteTransaction(nextData, wallet)}
}

func TestGenesis(t *testing.T) {
	block := Genesis()
	switch true {
	case block.SequenceNb != 0:
		t.Errorf("The Sequence number is %d", block.SequenceNb)
	case block.LastHast != nil:
		t.Errorf("The Last Hash is %d", block.LastHast)
	}
}

func TestWallet(t *testing.T) {
	wallet := NewWallet("secret")
	message := "Omae wa mou shindeiru"
	sign := wallet.Sign([]byte(message))
	if !ed25519.Verify(wallet.PublicKey(), []byte(message), sign) {
		t.Errorf("Signature doesn't match")
	}
}

func TestWalletKey(t *testing.T) {
	numberOfvalidator := 5
	validator := Validators{}
	validator.GenerateAddresses(numberOfvalidator)
	for i := 0; i < numberOfvalidator; i++ {
		wallet := NewWallet(fmt.Sprintf("NODE%d", i))
		if !validator.IsActiveValidator(wallet.PublicKey()) {
			t.Error("One of the public key is invalid")
		}
	}

}

func TestTransaction(t *testing.T) {
	wallet := NewWallet("secret")
	dataString := "The path of the righteous man is beset on all sides by the inequities of the selfish and the tyranny of evil men."
	transac := wallet.CreateBruteTransaction([]byte(dataString))
	//byteVersion := transac.ToByte()
	switch {
	case bytes.Compare(transac.TransaCore.Input.(BrutData).Data, []byte(dataString)) != 0:
		t.Errorf("The data are not equals")
	case !transac.VerifyTransaction():
		t.Errorf("The transaction isn't verified\n")
		//case !bytes.Equal(ByteToTx(byteVersion).Input.(BrutData).Data, []byte(dataString)):
		//	t.Error("Conversion failed")
	}
}

func TestValidator_old(t *testing.T) {
	var validator Validators
	validator.GenerateAddresses(5)
	walletExample := NewWallet("example")
	switch {
	case len(validator.GetValidatorList()) != 5:
		t.Errorf("No 5 elements in the validator : %d instead of %d", len(validator.GetValidatorList()), 5)
	case !validator.IsActiveValidator(validator.GetValidatorList()[2]):
		t.Errorf("2nd Element non present in the validator")
	case validator.IsActiveValidator(walletExample.PublicKey()):
		t.Errorf("Mismatch a non existing Wallet")
	}
}

func TestBLock(t *testing.T) {
	genesis := Genesis()
	walletExample := NewWallet("example")
	walletOther := NewWallet("HectorSalamanca")
	var data = "Hasta la vista, baby."
	newblock := genesis.CreateBlock(createListTx(data, *walletExample), *walletExample)
	switch {
	case !bytes.Equal(newblock.Transactions[0].TransaCore.Input.(BrutData).Data, []byte(data)):
		t.Errorf("The data are differents")
	case !newblock.VerifyBlock():
		t.Errorf("The block is not verified")
	case !newblock.VerifyProposer(walletExample.PublicKey()):
		t.Errorf("The proposer isn't recognize")
	case newblock.VerifyProposer(walletOther.PublicKey()):
		t.Errorf("The block proposer is mismatch with an other")
	}
	data2 := "God creates dinosaurs. God destroys dinosaurs. God creates man. Man destroys God. Man creates dinosaurs"
	newnewbloc := newblock.CreateBlock(createListTx(data2, *walletExample), *walletExample)
	switch {
	case !bytes.Equal(newblock.Hash, newnewbloc.LastHast):
		t.Error("There is no chain")
	case newblock.Hash == nil:
		t.Error("The hash have problems")
	case (newblock.SequenceNb != 1) || (newnewbloc.SequenceNb != 2):
		t.Error("Sequence numbers don't match")
	}
}

func TestBlock2(t *testing.T) {
	genesis := Genesis()
	walletExample := NewWallet("example")
	var data = "You see, in this world there's two kinds of people, my friend: those with loaded guns, and those who dig. You dig"
	txList := createListTx(data, *walletExample)
	newblock := genesis.CreateBlock(txList, *walletExample)
	if !newblock.VerifyBlock() {
		t.Error("The Block is invalid")
	}
	byted, _ := json.MarshalIndent(newblock, "", "\t")
	fmt.Println(string(byted))
	newData := BrutData{Data: []byte("Ah ! bah maintenant, elle va marcher beaucoup moins bien, forcément !")}
	newblock.Transactions[0].TransaCore.Input = newData
	byted, _ = json.MarshalIndent(newblock, "", "\t")
	fmt.Println(string(byted))
	if newblock.VerifyBlock() {
		t.Error("The Block is valid instead of being invalid")
	}
	newblock = genesis.CreateBlock(txList, *walletExample)
	if newblock.VerifyBlock() {
		t.Error("The Block is valid instead of being invalid, incorrect transaction")
	}
}

func TestBlockPool(t *testing.T) {
	genesis := Genesis()
	walletExample := NewWallet("example")
	var pool BlockPool = *NewBlockPool()
	bloc := genesis.CreateBlock(createListTx("You talkin' to me?", *walletExample), *walletExample)
	blocConc := genesis.CreateBlock(createListTx("No Luke, I am your father", *walletExample), *walletExample)
	pool.AddBlock(*bloc)
	switch {
	case pool.PoolSize() != 1:
		t.Error("The block hasn't be added")
	case !pool.ExistingBlock(*bloc):
		t.Error("The bloc isn't found in the list")
	case pool.ExistingBlock(*blocConc):
		t.Error("The bloc is mismatch in the list")
	}
}

func TestTxPool(t *testing.T) {
	var pool = NewTransactionPool(nil, Nothing)
	pool.SetMetricHandler(whiteMetric{})
	walletExample := NewWallet("Chuck Norris")
	var isAdded = true
	transactionRef := NewBruteTransaction([]byte(fmt.Sprintf("The Godfather")), *walletExample)
	transactionHost := NewBruteTransaction([]byte(fmt.Sprintf("The GodMother")), *walletExample)
	isAdded = isAdded && pool.AddTransaction(*transactionRef)
	for i := 2; i <= config.TransactionThreshold; i++ {
		transaction := NewBruteTransaction([]byte(fmt.Sprintf("The Godfather %d", i)), *walletExample)
		isAdded = isAdded && pool.AddTransaction(*transaction)
	}
	switch {
	case pool.PoolSize() != config.TransactionThreshold:
		t.Errorf("Transaction haven't be added : %d", pool.PoolSize())
	case !isAdded:
		t.Error("one of the transaction hasn't be added")
	case !pool.ExistingTransaction(*transactionRef):
		t.Error("The transaction isn't found in the list")
	case pool.ExistingTransaction(*transactionHost):
		t.Error("The transaction is mismatch in the list")
		//case !pool.List()[4].VerifyTransaction():
		//	t.Error("One of the transaction have problems")
	}
	isAdded = isAdded && pool.AddTransaction(*transactionHost)
	switch {
	case isAdded:
		t.Error("The transaction is added but shouldn't ")
	case pool.PoolSize() != config.TransactionThreshold:
		t.Errorf("The number of transaction is incorrect : %d", pool.PoolSize())
	}
	pool.Clear()
	if pool.PoolSize() != 0 {
		t.Error("The pool hasn't be cleared")
	}
}

func TestPreparePool(t *testing.T) {
	poolAlice := NewPreparePool()
	walletExample := NewWallet("Jean Pierre")
	genesis := Genesis()
	newBlock := genesis.CreateBlock(nil, *walletExample)
	prepare := CreatePrepare(newBlock, walletExample)
	if poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)) != 0 {
		t.Errorf("The size of the list is incorred expected %d, got %d", 1, poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)))
	}
	poolAlice.AddPrepare(prepare)
	if poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)) != 1 {
		t.Errorf("The size of the list is incorred expected %d, got %d", 2, poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)))
	}
	prepare2 := CreatePrepare(newBlock, walletExample)
	poolAlice.AddPrepare(prepare2)
	if poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)) != 1 {
		t.Errorf("The list isn't reset to 0, expected 1, got %d", poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)))
	}
	if poolAlice.GetNbOfPrepare() != 1 {
		t.Error("Wrong number of element in the map")
	}
	nextBlock := newBlock.CreateBlock(nil, *walletExample)
	prepare3 := CreatePrepare(nextBlock, walletExample)
	poolAlice.AddPrepare(prepare3)
	if poolAlice.GetNbOfPrepare() != 2 {
		t.Error("Wrong number of element in the map ")
	}
}

func TestPreparePool2(t *testing.T) {
	poolAlice := NewPreparePool()
	poolBob := NewPreparePool()
	walletPierre := NewWallet("Jean Pierre")
	walletCharlie := NewWallet("Jean Charlie")
	genesis := Genesis()
	newBlock := genesis.CreateBlock(nil, *walletPierre)
	prepareA := CreatePrepare(newBlock, walletPierre)
	prepareB := CreatePrepare(newBlock, walletCharlie)
	poolAlice.AddPrepare(prepareB)
	poolAlice.AddPrepare(prepareA)
	if !prepareA.IsValidPrepare() {
		t.Error("The check or the signature or prepare is invalid")
	}
	switch {
	case poolAlice.GetNbOfPrepare() != 1:
		t.Error("Wrong number of element in the map")
	case poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)) != 2:
		t.Errorf("The size of the list is incorred expected %d, got %d", 2, poolAlice.GetNbPrepareOfHash(string(newBlock.Hash)))
	case !poolAlice.ExistingPrepare(prepareA):
		t.Error("Didn't found the prepare")
	case poolBob.ExistingPrepare(prepareA):
		t.Error("Found a wrong prepare message")
	}
}

func TestTCMessage(t *testing.T) {
	messagePool := NewMessagePool()
	walletExample := NewWallet("Jean Christophe")
	genesis := Genesis()
	RCMess := messagePool.CreateMessage(genesis.Hash, walletExample)
	if !RCMess.IsValidMessage() {
		t.Error("The RC message is invalid")
	}
}
