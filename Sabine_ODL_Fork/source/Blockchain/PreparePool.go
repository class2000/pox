package Blockchain

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/rs/zerolog/log"
)

// PreparePool transactions object is mapping that holds a transactions prepare messages for a hash of a block
type PreparePool struct {
	mapHashPrepare map[string]*allPrepare
}

type allPrepare struct {
	unconfirmedPrepare map[string]ed25519.PublicKey
	confirmedPrepare   map[string]ed25519.PublicKey
	validated          bool
	nbVal              int
}

func newAllPrepare() *allPrepare {
	return &allPrepare{
		unconfirmedPrepare: make(map[string]ed25519.PublicKey),
		confirmedPrepare:   make(map[string]ed25519.PublicKey),
	}
}

func (all *allPrepare) confirm(validator ValidatorInterf) {
	for key, publicKey := range all.unconfirmedPrepare {
		if validator.IsActiveValidator(publicKey) {
			all.confirmedPrepare[key] = publicKey
		} else {
			log.Warn().Msgf("A Prepare Message is Invalid")
		}
	}
	all.unconfirmedPrepare = make(map[string]ed25519.PublicKey)
}

func (all allPrepare) getNbPrepare() int {
	return len(all.unconfirmedPrepare) + len(all.confirmedPrepare)
}

func (all *allPrepare) validate(validator ValidatorInterf) {
	all.validated = true
	all.nbVal = validator.GetNumberOfValidator()
}

func (all allPrepare) isFullValidated() bool {
	return all.validated && all.getNbPrepare() == all.nbVal
}

func NewPreparePool() (preparePool PreparePool) {
	preparePool.mapHashPrepare = make(map[string]*allPrepare)
	return
}

func (pool PreparePool) GetNbPrepareOfHash(hash string) int {
	mapHash, ok := pool.mapHashPrepare[hash]
	if !ok {
		return 0
	}
	return mapHash.getNbPrepare()
}

func (pool *PreparePool) GetNbPrepareOfHashOfActive(hash string, interf ValidatorInterf) (cmpt int) {
	_, ok := pool.mapHashPrepare[hash]
	if !ok {
		return 0
	}
	pool.mapHashPrepare[hash].confirm(interf)
	return len(pool.mapHashPrepare[hash].confirmedPrepare)
}

func (pool PreparePool) GetNbOfPrepare() int {
	return len(pool.mapHashPrepare)
}

func (pool *PreparePool) Validate(hash string, validator ValidatorInterf) {
	pool.mapHashPrepare[hash].validate(validator)
}

func (pool PreparePool) IsFullValidated(hash string) bool {
	mAllPrepare, ok := pool.mapHashPrepare[hash]
	if !ok {
		return false
	}
	return mAllPrepare.isFullValidated()
}

type Prepare struct {
	BlockHash []byte
	PublicKey ed25519.PublicKey
	Signature []byte
}

const prepareSalt byte = byte(PrepareMess)

//CreatePrepare creates a prepare Message for the given block
func CreatePrepare(block *Block, wallet *Wallet) Prepare {
	return Prepare{
		BlockHash: block.Hash,
		PublicKey: wallet.PublicKey(),
		Signature: wallet.Sign(append(block.Hash, prepareSalt)),
	}
}

// AddPrepare pushes the prepare Message for a block hash into the transactions
func (pool *PreparePool) AddPrepare(prepare Prepare) {
	hash := string(prepare.BlockHash)
	_, ok := pool.mapHashPrepare[hash]
	if !ok {
		pool.mapHashPrepare[hash] = newAllPrepare()
	}
	if _, ok = pool.mapHashPrepare[hash].confirmedPrepare[string(prepare.Signature)]; !ok {
		pool.mapHashPrepare[hash].unconfirmedPrepare[string(prepare.Signature)] = prepare.PublicKey
	}
}

// ExistingPrepare checks if the prepare Message already exists
func (pool PreparePool) ExistingPrepare(prepare Prepare) bool {
	hash := string(prepare.BlockHash)
	_, ok := pool.mapHashPrepare[hash]
	if !ok {
		return false
	}
	if _, ok = pool.mapHashPrepare[hash].confirmedPrepare[string(prepare.Signature)]; ok {
		return true
	}
	_, ok = pool.mapHashPrepare[hash].unconfirmedPrepare[string(prepare.Signature)]
	return ok
}

func (pool *PreparePool) Remove(hash []byte) {
	delete(pool.mapHashPrepare, string(hash))
}

// IsValidPrepare checks if the prepare Message is valid or not
func (p Prepare) IsValidPrepare() bool {
	return VerifySignature(p.PublicKey, append(p.BlockHash, prepareSalt), p.Signature)
}

func (prepare Prepare) ToByte() []byte {
	byted, err := json.Marshal(prepare)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return byted
}

func (prepare Prepare) GetHashPayload() string {
	return base64.StdEncoding.EncodeToString(prepare.BlockHash)
}

func (prepare Prepare) GetProposer() ed25519.PublicKey {
	return prepare.PublicKey
}
