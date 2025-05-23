package Blockchain

import (
	"crypto/ed25519"
	"fmt"
)

type Wallet struct {
	keyPair   ed25519.PrivateKey
	publicKey ed25519.PublicKey
}

func (w Wallet) PublicKey() ed25519.PublicKey {
	return w.publicKey
}

func NewWallet(secret string) (w *Wallet) {
	w = &Wallet{keyPair: GenKeyPair([]byte(secret))}
	w.publicKey = PublicLocal(w.keyPair)
	return
}

func (w Wallet) Sign(hash []byte) []byte {
	return ed25519.Sign(w.keyPair, hash)
}

func (w Wallet) ToString() string {
	return fmt.Sprintf("Wallet - publicKey: %s", w.publicKey)
}

func (w Wallet) CreateBruteTransaction(data []byte) *Transaction {
	return NewBruteTransaction(data, w)
}

func (w Wallet) CreateTransaction(commande Commande) *Transaction {
	return NewTransaction(commande, w)
}
