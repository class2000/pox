package Blockchain

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/rs/zerolog/log"
)

// CommitPool transactions object is mapping that holds a transactions prepare messages for a hash of a block
type CommitPool struct {
	mapHashCommit map[string]*allCommit
}

type allCommit struct {
	unconfirmedCommit map[string]ed25519.PublicKey
	confirmedCommit   map[string]ed25519.PublicKey
	validated         bool
	nbVal             int
}

func NewCommitPool() (commitPool CommitPool) {
	commitPool.mapHashCommit = make(map[string]*allCommit)
	return
}

func newAllCommit() *allCommit {
	return &allCommit{
		unconfirmedCommit: make(map[string]ed25519.PublicKey),
		confirmedCommit:   make(map[string]ed25519.PublicKey),
	}
}

func (all *allCommit) confirm(validator ValidatorInterf) {
	for key, publicKey := range all.unconfirmedCommit {
		if validator.IsActiveValidator(publicKey) {
			all.confirmedCommit[key] = publicKey
		} else {
			log.Warn().Msgf("A Commit Message is Invalid")
		}
	}
	all.unconfirmedCommit = make(map[string]ed25519.PublicKey)
}

func (all allCommit) getNbCommit() int {
	return len(all.unconfirmedCommit) + len(all.confirmedCommit)
}

func (all *allCommit) validate(validator ValidatorInterf) {
	all.validated = true
	all.nbVal = validator.GetNumberOfValidator()
}

func (all allCommit) isFullValidated() bool {
	return all.validated && all.getNbCommit() == all.nbVal
}

func (pool CommitPool) GetNbCommitsOfHash(hash string) int {
	mapHash, ok := pool.mapHashCommit[hash]
	if !ok {
		return 0
	}
	return mapHash.getNbCommit()
}

type Commit struct {
	BlockHash []byte
	PublicKey ed25519.PublicKey
	Signature []byte
}

const commitSalt byte = byte(CommitMess)

//CreateCommit creates a commit Message for the given block
func CreateCommit(hash []byte, wallet *Wallet) Commit {
	return Commit{
		BlockHash: hash,
		PublicKey: wallet.PublicKey(),
		Signature: wallet.Sign(append(hash, commitSalt)),
	}
}

// Commit commit function initialize a transactions of prepare Message for a block
// and adds the prepare Message for the current node and
// returns it
func (p *CommitPool) Commit(prepare *Prepare, wallet *Wallet) (commit Commit) {
	commit = CreateCommit(prepare.BlockHash, wallet)
	p.AddCommit(commit)
	return
}

//AddCommit pushes the commit Message for a block hash into the transactions
func (pool *CommitPool) AddCommit(commit Commit) {
	hash := string(commit.BlockHash)
	_, ok := pool.mapHashCommit[hash]
	if !ok {
		pool.mapHashCommit[hash] = newAllCommit()
	}
	if _, ok = pool.mapHashCommit[hash].confirmedCommit[string(commit.Signature)]; !ok {
		pool.mapHashCommit[hash].unconfirmedCommit[string(commit.Signature)] = commit.PublicKey
	}
}

func (pool CommitPool) ExistingCommit(commit Commit) bool {
	hash := string(commit.BlockHash)
	_, ok := pool.mapHashCommit[hash]
	if !ok {
		return false
	}
	if _, ok = pool.mapHashCommit[hash].confirmedCommit[string(commit.Signature)]; ok {
		return true
	}
	_, ok = pool.mapHashCommit[hash].unconfirmedCommit[string(commit.Signature)]
	return ok
}

func (p Commit) IsValidCommit() bool {
	return VerifySignature(p.PublicKey, append(p.BlockHash, commitSalt), p.Signature)
}

func (commit Commit) ToByte() []byte {
	byted, err := json.Marshal(commit)
	if err != nil {
		log.Fatal().Msgf("Json Parsing error : %s", err)
	}
	return byted
}

func (commit Commit) GetHashPayload() string {
	return base64.StdEncoding.EncodeToString(commit.BlockHash)
}

func (pool CommitPool) HaveCommitFrom(blockhash []byte, wallet *Wallet) bool {
	hash := string(blockhash)
	_, ok := pool.mapHashCommit[hash]
	if !ok {
		return false
	}
	commit := CreateCommit(blockhash, wallet)
	_, ok = pool.mapHashCommit[hash].confirmedCommit[string(commit.Signature)]
	if ok {
		return true
	}
	_, ok = pool.mapHashCommit[hash].unconfirmedCommit[string(commit.Signature)]
	return ok
}

func (pool *CommitPool) GetNbPrepareOfHashOfActive(hash string, validators ValidatorInterf) (cmpt int) {
	_, ok := pool.mapHashCommit[hash]
	if !ok {
		return 0
	}
	pool.mapHashCommit[hash].confirm(validators)
	return len(pool.mapHashCommit[hash].confirmedCommit)
}

func (commit Commit) GetProposer() ed25519.PublicKey {
	return commit.PublicKey
}

func (pool *CommitPool) Remove(hash []byte) {
	delete(pool.mapHashCommit, string(hash))
}

func (pool *CommitPool) Validate(hash string, validator ValidatorInterf) {
	pool.mapHashCommit[hash].validate(validator)
}

func (pool CommitPool) IsFullValidated(hash string) bool {
	mAllCommit, ok := pool.mapHashCommit[hash]
	if !ok {
		return false
	}
	return mAllCommit.isFullValidated()
}
