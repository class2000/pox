package Blockchain

import (
	"crypto/ed25519"
	"encoding/json"
	"github.com/rs/zerolog/log"
)

type BlockMsg struct {
	Block Block
}

func (blockMsg BlockMsg) GetProposer() ed25519.PublicKey {
	return blockMsg.Block.Proposer
}

func (blockMsg BlockMsg) ToByte() []byte {
	byted, err := json.Marshal(blockMsg)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return byted
}

func (blockMsg BlockMsg) GetBlock() Block {
	return blockMsg.Block
}

func (blockMsg BlockMsg) GetHashPayload() string {
	return blockMsg.Block.GetHashPayload()
}
