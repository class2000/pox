package Blockchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/rs/zerolog/log"
	"time"
)

type Block struct {
	LastHast     []byte            `json:"lasthash"`
	Hash         []byte            `json:"Hash"`
	Proposer     ed25519.PublicKey `json:"proposer"`
	Timestamp    int64             `json:"Timestamp"`
	SequenceNb   int               `json:"sequence_nb"`
	Signature    []byte            `json:"Signature"`
	Transactions []Transaction     `json:"Transactions"`
}

// A function to print the block
func (block Block) String() []byte {
	jsonString, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return jsonString
}

// Genesis The first block by default will the genesis block
// this function generates the genesis block with random values
func Genesis() Block {
	genesis := Block{
		LastHast:     nil,
		Hash:         nil,
		Proposer:     nil,
		Timestamp:    time.Time{}.UnixNano(),
		SequenceNb:   0,
		Signature:    nil,
		Transactions: nil,
	}
	genesis.Hash = hashBlock(genesis.Timestamp, genesis.LastHast, genesis.Transactions)
	return genesis
}

// CreateBlock creates a block using the passed lastblock, transactions and wallet instance
func (block Block) CreateBlock(data []Transaction, wallet Wallet) (newBloc *Block) {
	newBloc = &Block{
		LastHast:     block.Hash,
		Proposer:     wallet.PublicKey(),
		Timestamp:    time.Now().UnixNano(),
		SequenceNb:   block.SequenceNb + 1,
		Transactions: data,
	}
	newBloc.Hash = hashBlock(newBloc.Timestamp, newBloc.LastHast, newBloc.Transactions)
	newBloc.Signature = signBlockHash(newBloc.Hash, wallet)
	log.Print("A new block is created")
	return
}

// returns the hash of a block
func hashBlock(timestamp int64, lastHash []byte, transactions []Transaction) []byte {
	data := struct {
		Timestamp   int64
		LastHash    []byte
		Transaction []Transaction
	}{timestamp, lastHash, transactions}
	dataJson, err := json.Marshal(data)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return Hash(dataJson)
}

// signs the passed block using the passed wallet instance
func signBlockHash(hash []byte, wallet Wallet) []byte {
	return wallet.Sign(hash)
}

// VerifyBlock checks if the block is valid
func (block Block) VerifyBlock() bool {
	estimatedHash := hashBlock(block.Timestamp, block.LastHast, block.Transactions)
	switch {
	case !bytes.Equal(block.Hash, estimatedHash):
		log.Warn().Msg("The hash of the block is incorrect")
		return false
	case !VerifySignature(block.Proposer, estimatedHash, block.Signature):
		log.Warn().Msg("The signature mismatch the proposer")
		return false
	}
	for _, transac := range block.Transactions {
		if !transac.VerifyTransaction() {
			log.Printf("One of the transaction is incorrect:%s", transac.TransaCore.Ids)
			return false
		}
	}
	return true
}

// VerifyProposer verifies the proposer of the block with the passed public key
func (block Block) VerifyProposer(proposer ed25519.PublicKey) bool {
	return bytes.Equal(block.Proposer, proposer)
}

func (block Block) ToByte() []byte {
	byted, err := json.Marshal(block)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return byted
}

func ByteToBlock(data []byte) (block Block) {
	err := json.Unmarshal(data, &block)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return
}

func (block Block) GetHashPayload() string {
	return base64.StdEncoding.EncodeToString(block.Hash)
}

func (block Block) GetProposer() ed25519.PublicKey {
	return block.Proposer
}
