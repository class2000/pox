package Blockchain

// P2P_PORT declare a p2p server port on which it would listen for messages
// we will pass the port through command line
const P2P_PORT = 5001

type Sockets interface {
	UpdateDelay
	BroadcastMessage(message Message)
	BroadcastMessageNV(message Message)
	TransmitTransaction(message Message)
}

type UpdateDelay interface {
	UpdateDelay(parameter int)
}
