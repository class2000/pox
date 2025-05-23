package Blockchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/rs/zerolog/log"
)

// MessagePool transactions object is mapping that holds a transactions messages for a hash of a block
type MessagePool struct {
	mapHashMessage map[string][]RoundChange
	message        string
}

func NewMessagePool() (messagePool MessagePool) {
	messagePool.mapHashMessage = make(map[string][]RoundChange)
	messagePool.message = "INITIATE NEW ROUND"
	return
}

func (pool MessagePool) GetNbMessageOfHash(hash string) int {
	return len(pool.mapHashMessage[hash])
}

func (p *MessagePool) MapHashMessage() map[string][]RoundChange {
	return p.mapHashMessage
}

type RoundChange struct {
	BlockHash []byte
	PublicKey ed25519.PublicKey
	Signature []byte
	Message   string
}

func (rc RoundChange) GetProposer() ed25519.PublicKey {
	return rc.PublicKey
}

// CreateMessage creates a Message for the given block
func (p *MessagePool) CreateMessage(blockHash []byte, wallet *Wallet) (round *RoundChange) {
	round = &RoundChange{
		BlockHash: blockHash,
		PublicKey: wallet.PublicKey(),
		Signature: wallet.Sign([]byte((p.message + string(blockHash)))),
		Message:   p.message, //ERROR
	}
	//p.mapHashMessage[string(blockHash)] = []RoundChange{*round}
	p.mapHashMessage[string(blockHash)] = append(p.mapHashMessage[string(blockHash)], *round)
	return
}

// AddMessage pushes the Message for a block hash into the transactions
func (p *MessagePool) AddMessage(message RoundChange) {
	p.mapHashMessage[string(message.BlockHash)] = append(p.mapHashMessage[string(message.BlockHash)], message)
}

// ExistingMessage checks if the Message already exists
func (p MessagePool) ExistingMessage(message RoundChange) bool {
	subList, ok := p.mapHashMessage[string(message.BlockHash)]
	if !ok {
		return false
	}
	for _, elem := range subList {
		if bytes.Equal(elem.PublicKey, message.PublicKey) {
			return true
		}
	}
	return false
}

// IsValidMessage checks if the Message is valid or not
func (p RoundChange) IsValidMessage() bool {
	log.Print("in valid here")
	return VerifySignature(p.PublicKey, []byte((p.Message + string(p.BlockHash))), p.Signature)
}

func (message RoundChange) ToByte() []byte {
	byted, err := json.Marshal(message)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return byted
}

func (message RoundChange) GetHashPayload() string {
	return base64.StdEncoding.EncodeToString(message.BlockHash)
}

func ByteToRoundChange(data []byte) (message RoundChange) {
	err := json.Unmarshal(data, &message)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return
}
