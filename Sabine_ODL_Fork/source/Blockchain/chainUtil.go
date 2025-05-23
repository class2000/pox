package Blockchain

import (
	"crypto/ed25519"
	"crypto/sha256"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"math/rand"
	"time"
)

func GenKeyPair(seed []byte) ed25519.PrivateKey {
	if len(seed) != ed25519.SeedSize {
		newSeed := string(seed)
		for len(newSeed) < ed25519.SeedSize {
			newSeed = newSeed + string(seed)
		}
		seed = make([]byte, ed25519.SeedSize)
		copy(seed, newSeed[:ed25519.SeedSize])
	}
	return ed25519.NewKeyFromSeed(seed)
}

func Hash(data []byte) []byte {
	h := sha256.New()
	h.Write(data)
	return h.Sum(nil)
}

func VerifySignature(publicKey ed25519.PublicKey, message, sig []byte) bool {
	return ed25519.Verify(publicKey, message, sig)
}

func Ids() uuid.UUID {
	return uuid.New()
}

// PublicLocal Public returns the publicKey corresponding to priv.
func PublicLocal(priv ed25519.PrivateKey) ed25519.PublicKey {
	publicKey := make([]byte, ed25519.PublicKeySize)
	copy(publicKey, priv[32:])
	return publicKey
}

// Return the min between two int
func min(x, y int) int {
	if x > y {
		return y
	}
	return x
}

// Return the max between two int
func max(x, y int) int {
	if x < y {
		return y
	}
	return x
}

// Return the max between two int64
func max64(x, y int64) int64 {
	if x < y {
		return y
	}
	return x
}

type CloseHandler struct {
	ToClose chan bool
	dead    chan bool
}

func NewCloseHandler() CloseHandler {
	return CloseHandler{ToClose: make(chan bool, 1), dead: make(chan bool, 1)}
}

func (handler *CloseHandler) StopLoop() {
	handler.ToClose <- true
	<-handler.dead
	close(handler.ToClose)
	close(handler.dead)
}

func (handler *CloseHandler) StopLoopRoutine() {
	handler.dead <- true
	return
}

func check(err error) {
	if err != nil {
		log.Error().Msgf("error %t \n-> %s", err, err)
		log.Panic().Msg(err.Error())
	}
}

// generateRandomList return a slide of dimension Size of different int lower than maxValue
func generateRandomList(Size int, maxValue int, seed int64) []int {
	randomList := arrange(maxValue)
	if seed < 0 {
		rand.Seed(time.Now().Unix())
	} else {
		rand.Seed(seed)
	}
	rand.Shuffle(len(randomList), func(i, j int) {
		randomList[i], randomList[j] = randomList[j], randomList[i]
	})
	listValIndex := randomList[:Size]
	return listValIndex
}

func arrange(size int) []int {
	newList := make([]int, size)
	for i := 0; i < size; i++ {
		newList[i] = i
	}
	return newList
}
